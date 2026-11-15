"""Пересборка слоя фреймворков в базе: чтение графа, правила, запись маршрутов и рёбер.

Слой строится целиком заново после каждой синхронизации, которая что-то изменила: узлы
``route`` удаляются вместе с их рёбрами, затем правила Symfony, API Platform и Next.js
создают их снова, а HTTP-мост связывает вызовы с маршрутами. Ключ маршрута стабилен
(``route:путь-файла:имя``), поэтому ответы между пересборками не меняются.
"""

import hashlib
import json
import posixpath
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from atlas.frameworks import http, nextjs, symfony
from atlas.model import EdgeKind, NodeKind, Provenance
from atlas.store.db import dumps

if TYPE_CHECKING:
    from collections.abc import Iterable, Iterator
    from pathlib import Path

    from atlas.config import Config
    from atlas.frameworks.routes import RouteSpec
    from atlas.store.db import Store

_SQL_BATCH = 500


@dataclass(frozen=True, slots=True)
class LayerReport:
    """Что построил слой: маршруты, их рёбра и итог моста по вызовам.

    ``requests`` — вызовы с однозначным маршрутом, ``guessed_requests`` — со связью-догадкой
    к нескольким маршрутам, ``ambiguous_requests`` и ``unmatched_requests`` — без связи.
    """

    routes: int = 0
    edges: int = 0
    requests: int = 0
    guessed_requests: int = 0
    ambiguous_requests: int = 0
    unmatched_requests: int = 0

    def as_meta(self) -> str:
        """Итог для ``meta`` базы: его показывает ``status``."""
        return json.dumps(
            {
                "routes": self.routes,
                "route_edges": self.edges,
                "requests": self.requests,
                "guessed_requests": self.guessed_requests,
                "ambiguous_requests": self.ambiguous_requests,
                "unmatched_requests": self.unmatched_requests,
            }
        )


def codebases(markers: Iterable[str]) -> list[str]:
    """Каталоги кодовых баз по маркерам (``composer.json``, ``package.json``)."""
    return sorted({posixpath.dirname(marker) or "." for marker in markers} | {"."})


def input_digest(root: Path, bases: Iterable[str]) -> str:
    """Отпечаток конфигурации фреймворков: размер и mtime каждого входного файла."""
    parts: list[str] = []
    for base in bases:
        directory = root / base
        for path in (*symfony.input_files(directory), *nextjs.input_files(directory)):
            try:
                stat = path.stat()
            except OSError:
                continue
            parts.append(
                f"{path.relative_to(root).as_posix()}:{stat.st_size}:{stat.st_mtime_ns}"
            )
    return hashlib.sha256("\n".join(sorted(parts)).encode()).hexdigest()


def rebuild(store: Store, config: Config, bases: Iterable[str]) -> LayerReport:
    """Удалить и заново построить маршруты и рёбра фреймворков."""
    store.execute("DELETE FROM nodes WHERE kind = ?", (NodeKind.ROUTE,))
    store.execute("DELETE FROM edges WHERE provenance = ?", (Provenance.FRAMEWORK,))
    # WHY: догадки моста — эвристические рёбра; их цели-маршруты уже удалены каскадом,
    # удаление по правилу страхует от ребра, пережившего смену схемы слоя.
    store.delete_edges_by_via("http.template-candidates")
    specs: list[tuple[str, RouteSpec]] = []
    for base in bases:
        directory = config.root / base
        if symfony.detect(directory):
            contours = symfony.read_contours(directory, config.symfony_apps)
            classes, constants = _php_classes(store, base, contours)
            specs += [
                (base, spec)
                for spec in symfony.routes(contours, classes, constants, base)
            ]
        if nextjs.detect(directory):
            specs += [
                (base, spec) for spec in nextjs.routes(base, _next_files(store, base))
            ]
    written = _write_routes(store, specs)
    edges = _write_targets(store, written)
    report = http.link(
        _calls(store),
        [
            http.Endpoint(
                node=node,
                path=spec.path,
                methods=spec.methods,
                codebase=base,
                apps=spec.apps,
            )
            for node, _, base, spec in written
            if spec.meta.get("type") != "page"
        ],
        config.http_bridges,
        config.http_wrappers,
    )
    store.insert_edge_rows(
        (
            item.call.source,
            item.endpoint.node,
            EdgeKind.REQUESTS,
            item.call.line,
            item.call.col,
            Provenance.HEURISTIC if item.guess else Provenance.FRAMEWORK,
            item.confidence,
            item.via,
            item.call.file_id,
        )
        for item in report.links
    )
    return LayerReport(
        routes=len(written),
        edges=edges,
        requests=sum(1 for item in report.links if not item.guess),
        guessed_requests=report.guessed,
        ambiguous_requests=report.ambiguous,
        unmatched_requests=report.unmatched,
    )


# --- чтение графа ---------------------------------------------------------------------


def _php_classes(
    store: Store, base: str, contours: list[symfony.Contour]
) -> tuple[list[symfony.PhpClass], dict[str, object]]:
    """Классы с атрибутами и контроллеры YAML, их методы-обработчики и константы атрибутов."""
    controllers = {
        route.controller.lstrip("\\").partition("::")[0].lower()
        for contour in contours
        for route in contour.routes
    }
    rows = store.execute(
        "SELECT key, qualified_name, path, start_line, attributes FROM nodes "
        "WHERE codebase = ? AND language = 'php' AND kind IN ('class', 'enum') "
        "AND attributes IS NOT NULL",
        (base,),
    ).fetchall()
    classes: dict[str, dict[str, Any]] = {
        str(qualified).lower(): {
            "key": key,
            "fqcn": qualified,
            "path": path,
            "line": line,
            "attributes": tuple(json.loads(attributes)),
            "methods": {},
        }
        for key, qualified, path, line, attributes in rows
    }
    missing = sorted(controllers - set(classes))
    for key, qualified, path, line in _in_batches(
        store,
        "SELECT key, qualified_name, path, start_line FROM nodes WHERE language = 'php' "
        "AND kind = 'class' AND lower(qualified_name) IN ({marks})",
        missing,
    ):
        classes[str(qualified).lower()] = {
            "key": key,
            "fqcn": qualified,
            "path": path,
            "line": line,
            "attributes": (),
            "methods": {},
        }
    for key, qualified, attributes in store.execute(
        "SELECT key, qualified_name, attributes FROM nodes WHERE codebase = ? "
        "AND language = 'php' AND kind = 'method' "
        "AND (name = '__invoke' OR attributes LIKE '%Route%' OR attributes LIKE '%route%')",
        (base,),
    ):
        owner, _, name = str(qualified).rpartition("::")
        found = classes.get(owner.lower())
        if found is not None:
            found["methods"][name.lower()] = (
                key,
                name,
                tuple(json.loads(attributes)) if attributes else (),
            )
    for controller in controllers:
        found = classes.get(controller)
        if found is None:
            continue
        for key, qualified in store.execute(
            "SELECT key, qualified_name FROM nodes WHERE kind = 'method' AND path = ? "
            "AND qualified_name LIKE ?",
            (found["path"], f"{found['fqcn']}::%"),
        ):
            name = str(qualified).rpartition("::")[2]
            found["methods"].setdefault(name.lower(), (key, name, ()))
    result = [symfony.PhpClass(**values) for values in classes.values()]
    return result, _constants(store, result)


def _constants(store: Store, classes: Iterable[symfony.PhpClass]) -> dict[str, object]:
    names: set[str] = set()
    for php_class in classes:
        _const_names(php_class.attributes, names)
        for _, _, attributes in php_class.methods.values():
            _const_names(attributes, names)
    values: dict[str, object] = {}
    for qualified, meta in _in_batches(
        store,
        "SELECT qualified_name, meta FROM nodes WHERE kind = 'constant' "
        "AND lower(qualified_name) IN ({marks})",
        sorted(names),
    ):
        value = json.loads(meta).get("value") if meta else None
        if value is not None:
            values[str(qualified).lower()] = value
    return values


def _const_names(value: object, names: set[str]) -> None:
    if isinstance(value, dict):
        const = value.get("const")
        if isinstance(const, str):
            names.add(const.lower())
        for item in value.values():
            _const_names(item, names)
    elif isinstance(value, list | tuple):
        for item in value:
            _const_names(item, names)


def _next_files(store: Store, base: str) -> list[nextjs.SourceFile]:
    prefix = "" if base in {"", "."} else f"{base}/"
    roots = ("app/", "src/app/", "pages/", "src/pages/")
    files = [
        (file_id, key, path, meta)
        for file_id, key, path, meta in store.execute(
            "SELECT file_id, key, path, meta FROM nodes WHERE kind = 'file' AND codebase = ? "
            "AND language IN ('typescript', 'tsx', 'javascript')",
            (base,),
        )
        if str(path).removeprefix(prefix).startswith(roots)
    ]
    symbols: dict[int, dict[str, tuple[str, str]]] = {}
    for file_id, key, qualified, meta in _in_batches(
        store,
        "SELECT file_id, key, qualified_name, meta FROM nodes WHERE file_id IN ({marks}) "
        "AND kind IN ('function', 'constant', 'class')",
        [file_id for file_id, *_ in files],
    ):
        alias = json.loads(meta).get("alias", "") if meta else ""
        symbols.setdefault(file_id, {})[str(qualified)] = (str(key), str(alias))
    return [
        nextjs.SourceFile(
            path=str(path),
            key=str(key),
            exports=(json.loads(meta).get("exports", {}) if meta else {}),
            symbols=symbols.get(file_id, {}),
        )
        for file_id, key, path, meta in files
    ]


def _calls(store: Store) -> list[http.Call]:
    return [
        http.Call(
            source=source,
            file_id=file_id,
            codebase=codebase,
            callee=callee,
            method=method,
            path=path,
            line=line,
            col=col,
        )
        for source, file_id, codebase, callee, method, path, line, col in store.execute(
            "SELECT h.source, h.file_id, f.codebase, h.callee, h.method, h.path, h.line, "
            "h.col FROM http_calls h JOIN files f ON f.id = h.file_id"
        )
    ]


# --- запись ---------------------------------------------------------------------------


def _write_routes(
    store: Store, specs: list[tuple[str, RouteSpec]]
) -> list[tuple[int, int, str, RouteSpec]]:
    """Записать узлы маршрутов; вернуть id узла, id файла, кодовую базу и маршрут."""
    owners = _owners(store, {spec.owner for _, spec in specs})
    written: list[tuple[int, int, str, RouteSpec]] = []
    used: set[str] = set()
    for base, spec in sorted(
        specs,
        key=lambda item: (item[1].owner, item[1].line, item[1].title, item[1].name),
    ):
        owner = owners.get(spec.owner)
        if owner is None:
            continue
        file_id, path, language = owner
        qualified = spec.name or spec.title
        key = f"{NodeKind.ROUTE}:{path}:{qualified}"
        suffix = 2
        while key in used:
            key = f"{NodeKind.ROUTE}:{path}:{qualified}#{suffix}"
            suffix += 1
        used.add(key)
        meta: dict[str, Any] = {
            "framework": spec.framework,
            "path": spec.path,
            "methods": list(spec.methods),
            **spec.meta,
        }
        if spec.name:
            meta["route_name"] = spec.name
        if spec.apps:
            meta["apps"] = sorted(spec.apps)
        cursor = store.execute(
            "INSERT INTO nodes (key, file_id, kind, name, qualified_name, path, codebase, "
            "language, start_line, end_line, flags, meta) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)",
            (
                key,
                file_id,
                NodeKind.ROUTE,
                spec.title,
                qualified,
                path,
                base,
                language,
                spec.line,
                spec.line,
                dumps(meta),
            ),
        )
        written.append((int(cursor.lastrowid or 0), file_id, base, spec))
    return written


def _owners(store: Store, keys: set[str]) -> dict[str, tuple[int, str, str]]:
    found: dict[str, tuple[int, str, str]] = {}
    for key, file_id, path, language in _in_batches(
        store,
        "SELECT key, file_id, path, language FROM nodes WHERE key IN ({marks})",
        sorted(keys),
    ):
        found[str(key)] = (int(file_id), str(path), str(language))
    return found


def _write_targets(store: Store, written: list[tuple[int, int, str, RouteSpec]]) -> int:
    """Рёбра от маршрутов к обработчикам и классам; цель ищется по ключу, затем по имени."""
    keys = {
        key for *_, spec in written for target in spec.targets for key in target.keys
    }
    names = {
        name.lower()
        for *_, spec in written
        for target in spec.targets
        for name in target.names
    }
    by_key = _ids_by(store, "key", keys)
    by_name = _ids_by(store, "lower(qualified_name)", names)
    rows: list[tuple[Any, ...]] = []
    for node, file_id, _, spec in written:
        for target in spec.targets:
            found = next((by_key[key] for key in target.keys if key in by_key), None)
            if found is None:
                found = next(
                    (
                        by_name[name.lower()]
                        for name in target.names
                        if name.lower() in by_name
                    ),
                    None,
                )
            if found is None:
                continue
            rows.append(
                (
                    node,
                    found,
                    target.edge,
                    spec.line,
                    0,
                    Provenance.FRAMEWORK,
                    1.0,
                    target.via,
                    file_id,
                )
            )
    store.insert_edge_rows(rows)
    return len(rows)


def _ids_by(store: Store, column: str, values: set[str]) -> dict[str, int]:
    found: dict[str, int] = {}
    template = f"SELECT {column}, id FROM nodes WHERE {column} IN ({{marks}}) AND kind != 'route'"  # noqa: S608  # колонка — литерал вызывающего
    for value, node in _in_batches(store, template, sorted(values)):
        found.setdefault(str(value), int(node))
    return found


def _in_batches(store: Store, template: str, values: list[Any]) -> Iterator[Any]:
    """Строки запроса с ``IN ({marks})`` по пачкам значений: у SQLite предел параметров."""
    for start in range(0, len(values), _SQL_BATCH):
        chunk = values[start : start + _SQL_BATCH]
        sql = template.format(marks=",".join("?" * len(chunk)))
        yield from store.execute(sql, chunk)
