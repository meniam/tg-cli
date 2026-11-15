"""Запросы к графу: поиск символа, карточка узла, обходы вызовов, состояние индекса."""

import fnmatch
import json
import re
from collections import deque
from typing import TYPE_CHECKING, Any

from atlas.errors import AtlasError, ExitCode
from atlas.frameworks import routes

if TYPE_CHECKING:
    from collections.abc import Sequence
    from pathlib import Path

    from atlas.store.db import NodeRow, Store

MAX_CODE_LINES = 400
MAX_INLINE_CANDIDATES = 5
CALL_KINDS = ("calls", "instantiates", "renders", "routes_to", "requests")
_ROUTE_QUERY = re.compile(r"(?:([A-Z]+)\s+)?(/\S*)")
_ROUTE_CARD_KEYS = (
    "framework",
    "path",
    "methods",
    "route_name",
    "apps",
    "controller",
    "host",
    "condition",
    "operation",
    "origin",
)

# Доказательство ребра: строка, где оно записано, происхождение и уверенность.
type Evidence = tuple[int | None, str, float]


def find(store: Store, query: str) -> list[NodeRow]:
    r"""Узлы по запросу агента.

    Понимает идентификатор узла, ``путь``, ``путь:строка``, ``путь:начало-конец`` из поля
    ``file`` ответа, полное имя (``App\\Foo``,
    ``\\App\\Foo``), ``Класс::метод`` или ``Класс.метод`` с коротким именем класса и
    короткое имя.

    Returns:
        Все подходящие узлы в порядке поиска.

    """
    query = query.strip()
    node = store.node_by_key(query)
    if node is not None:
        return [node]
    path, _, lines = query.rpartition(":")
    line_text = lines.partition("-")[0]
    if path and line_text.isdigit() and path in store.file_paths():
        return [_innermost(store.nodes_in_file(path), int(line_text))]
    if query in store.file_paths():
        return [n for n in store.nodes_in_file(query) if n.kind == "file"]

    by_url = _routes_by_url(store, query)
    if by_url:
        return by_url
    qualified = query.lstrip("\\")
    exact = store.nodes_by_lower_qualified_name(qualified)
    if exact:
        return exact
    tail = qualified
    for separator in ("::", "\\", "."):
        tail = tail.rsplit(separator, 1)[-1]
    tail = tail.lstrip("$")
    candidates = store.nodes_by_name(tail)
    if tail != qualified:
        suffix = qualified.lower()
        return [
            n
            for n in candidates
            if n.qualified_name.lower().endswith(suffix)
            and n.qualified_name[: len(n.qualified_name) - len(suffix)][-1:]
            in {"", "\\", "/", "."}
        ]
    return [n for n in candidates if n.kind != "file"]


def _routes_by_url(store: Store, query: str) -> list[NodeRow]:
    """Маршруты, которые обслуживают URL: ``/api/v1/x``, ``POST /api/v1/x``; лучшие по специфичности."""
    found = _ROUTE_QUERY.fullmatch(query)
    if found is None:
        return []
    method, path = found.group(1) or "", found.group(2)
    exact = [n for n in store.nodes_by_name(query) if n.kind == "route"]
    if exact:
        return exact
    best: list[NodeRow] = []
    best_score = -1
    for node in store.nodes_by_kind("route"):
        methods = tuple(str(m) for m in node.meta.get("methods", []))
        if not routes.allows(methods, method):
            continue
        matched = routes.match(
            routes.compile_path(str(node.meta.get("path", ""))), path
        )
        if matched is None or matched.score < best_score:
            continue
        if matched.score > best_score:
            best, best_score = [], matched.score
        best.append(node)
    return best


def find_one(store: Store, query: str) -> NodeRow:
    """Ровно один узел по запросу.

    Returns:
        Единственный найденный узел.

    Raises:
        AtlasError: Узел не найден (код 3) или имя неоднозначно (код 4, с кандидатами).
    """
    found = find(store, query)
    if not found:
        raise AtlasError(f"Symbol not found: {query}", ExitCode.NOT_FOUND)
    if len(found) > 1:
        raise AtlasError(
            f"Ambiguous name: {query}. Narrow it down as Class::method, full name or path:line",
            ExitCode.AMBIGUOUS,
            candidates=[_with_scope(store, summary(n), n) for n in found[:50]],
        )
    return found[0]


def location(path: str, start: int, end: int | None = None) -> str:
    """Место в файле одной строкой: ``путь:строка`` или ``путь:начало-конец``.

    >>> location("src/Acl.php", 1, 23), location("src/Acl.php", 7, 7)
    ('src/Acl.php:1-23', 'src/Acl.php:7')

    Returns:
        Путь с номером строки или диапазоном строк.
    """
    # WHY: ответ читает только агент; одна строка вместо трёх полей экономит токены,
    # а дефис не спутать с записью «строка:колонка», которую понимают редакторы.
    return f"{path}:{start}" if end is None or end == start else f"{path}:{start}-{end}"


def summary(node: NodeRow) -> dict[str, Any]:
    """Краткая карточка узла для списков: вид, имя, место; кодовая база, если не корневая."""
    result: dict[str, Any] = {
        "kind": node.kind,
        # WHY: у маршрута агенту нужен метод и путь, а не внутреннее имя фреймворка.
        "name": node.name if node.kind == "route" else node.qualified_name,
        "file": location(node.path, node.start_line, node.end_line),
    }
    if node.codebase != ".":
        result["codebase"] = node.codebase
    return result


def _with_scope(store: Store, card: dict[str, Any], node: NodeRow) -> dict[str, Any]:
    """Добавить область файла, если она не ``source``, и пометку сгенерированного файла."""
    row = store.execute(
        "SELECT scope, generated FROM files WHERE id = ?", (node.file_id,)
    ).fetchone()
    if row is not None and row[0] != "source":
        card["scope"] = str(row[0])
    if row is not None and row[1]:
        card["generated"] = True
    return card


def node_card(
    store: Store, root: Path, node: NodeRow, *, code: bool, limit: int
) -> dict[str, Any]:
    """Всё о символе за один вызов: где он, что внутри, кто его вызывает и что вызывает он."""
    card: dict[str, Any] = _with_scope(store, summary(node), node)
    if node.signature:
        card["signature"] = node.signature
    for key in (
        "extends",
        "implements",
        "traits",
        "returns",
        "directives",
        "wrapped_by",
    ):
        if key in node.meta:
            card[key] = node.meta[key]
    if node.kind == "route":
        card["route"] = {k: node.meta[k] for k in _ROUTE_CARD_KEYS if node.meta.get(k)}
    parent = store.execute(
        "SELECT source FROM edges WHERE target = ? AND kind = 'contains' LIMIT 1",
        (node.id,),
    ).fetchone()
    parent_node = store.node(int(parent[0])) if parent is not None else None
    # WHY: файл-родитель повторяет путь из ``file``; осмыслен только объемлющий символ.
    if parent_node is not None and parent_node.kind != "file":
        card["parent"] = {
            "name": parent_node.qualified_name,
            "file": location(
                parent_node.path, parent_node.start_line, parent_node.end_line
            ),
        }
    members = store.execute(
        "SELECT target FROM edges WHERE source = ? AND kind = 'contains' ORDER BY line",
        (node.id,),
    ).fetchall()
    if members:
        member_nodes = store.nodes_by_ids(member_id for (member_id,) in members)
        by_kind: dict[str, dict[str, int]] = {}
        for (member_id,) in members:
            member = member_nodes.get(int(member_id))
            if member is not None:
                by_kind.setdefault(member.kind, {}).setdefault(
                    member.name, member.start_line
                )
        card["members"] = by_kind
    for key, incoming in (("incoming", True), ("outgoing", False)):
        edges = _edges(store, node.id, incoming=incoming, limit=limit)
        if edges:
            card[key] = edges
    unresolved_refs = _unresolved_from(store, node.id)
    if unresolved_refs:
        card["unresolved"] = unresolved_refs
    if code:
        card["code"] = read_lines(root, node.path, node.start_line, node.end_line)
    return card


def traverse(
    store: Store, node: NodeRow, *, incoming: bool, depth: int, limit: int
) -> list[dict[str, Any]]:
    """Обход вызовов вширь: кто вызывает узел (``incoming``) или кого вызывает он.

    Запись — символ на пути обхода, его место и строки вызова ``at``. ``depth`` выводится с
    второго уровня, вид связи ``edge`` — если это не ``calls``.

    Returns:
        Символы на пути обхода в порядке обхода вширь.
    """
    placeholders = ", ".join("?" for _ in CALL_KINDS)
    near, far = ("target", "source") if incoming else ("source", "target")
    sql = (
        f"SELECT {far}, kind, line, provenance, confidence FROM edges "  # noqa: S608  # имена колонок — литералы
        f"WHERE {near} = ? AND kind IN ({placeholders}) ORDER BY {far}, line"
    )
    result: list[dict[str, Any]] = []
    seen = {node.id}
    queue: deque[tuple[int, int]] = deque([(node.id, 0)])
    while queue and len(result) < limit:
        current, level = queue.popleft()
        if level >= depth:
            continue
        evidence: dict[int, list[Evidence]] = {}
        kinds: dict[int, str] = {}
        for other_id, kind, line, provenance, confidence in store.execute(
            sql, (current, *CALL_KINDS)
        ):
            if int(other_id) in seen:
                continue
            evidence.setdefault(int(other_id), []).append(
                (line, str(provenance), float(confidence))
            )
            kinds.setdefault(int(other_id), str(kind))
        neighbors = store.nodes_by_ids(evidence)
        for other_id, proofs in evidence.items():
            seen.add(other_id)
            other = neighbors.get(other_id)
            if other is None:
                continue
            entry = _linked(other, proofs)
            if level > 0:
                entry["depth"] = level + 1
            if kinds[other_id] != "calls":
                entry["edge"] = kinds[other_id]
            result.append(entry)
            queue.append((other_id, level + 1))
            if len(result) >= limit:
                break
    return result


def unresolved(
    store: Store, *, path: str | None, statuses: list[str], limit: int
) -> dict[str, Any]:
    """Нерешённые и неоднозначные ссылки: сводка по видам и первые записи."""
    placeholders = ", ".join("?" for _ in statuses)
    where = f"r.status IN ({placeholders})"
    params: list[Any] = list(statuses)
    if path:
        where += " AND (f.path = ? OR f.path LIKE ?)"
        params += [path, path.rstrip("/") + "/%"]
    base = "FROM refs r JOIN files f ON f.id = r.file_id"
    counts = store.execute(
        f"SELECT r.status, r.kind, count(*) {base} WHERE {where} GROUP BY 1, 2 ORDER BY 3 DESC",  # фрагменты из литералов
        params,
    ).fetchall()
    rows = store.execute(
        f"SELECT f.path, r.line, r.kind, r.name, r.status, r.candidates {base} WHERE {where} "  # фрагменты из литералов
        "ORDER BY f.path, r.line LIMIT ?",
        [*params, limit],
    ).fetchall()
    return {
        "counts": [{"status": s, "kind": k, "count": c} for s, k, c in counts],
        "refs": [
            {
                "file": location(p, ln),
                "kind": k,
                "name": n,
                "status": s,
                **({"candidates": candidate_files(store, c)} if c else {}),
            }
            for p, ln, k, n, s, c in rows
        ],
    }


def status(store: Store) -> dict[str, Any]:
    """Состояние индекса по группам: файлы, граф, ссылки, поиск, фреймворки."""
    # WHY: группы и разбивки в единственном числе — ключ называет измерение (``file.scope``,
    # ``search.kind``). Группу ``index`` добавляет CLI: это синхронизация, а не содержимое базы.

    def grouped(sql: str) -> dict[str, int]:
        return {str(key): int(count) for key, count in store.execute(sql)}

    def count(sql: str) -> int:
        return int(store.execute(sql).fetchone()[0])

    graph: dict[str, Any] = {
        "node": grouped("SELECT kind, count(*) FROM nodes GROUP BY kind"),
        "edge": grouped("SELECT kind, count(*) FROM edges GROUP BY kind"),
        "provenance": grouped(
            "SELECT provenance, count(*) FROM edges GROUP BY provenance"
        ),
    }
    if covered := coverage(store):
        graph["coverage"] = covered
    statuses = grouped("SELECT status, count(*) FROM refs GROUP BY status")
    refs: dict[str, Any] = {"status": statuses}
    if total := sum(statuses.values()):
        refs["resolved_share"] = round(statuses.get("resolved", 0) / total, 3)
    return {
        "file": {
            "language": grouped(
                "SELECT language, count(*) FROM files GROUP BY language"
            ),
            "codebase": grouped(
                "SELECT codebase, count(*) FROM files GROUP BY codebase"
            ),
            "scope": grouped("SELECT scope, count(*) FROM files GROUP BY scope"),
            "generated": count("SELECT count(*) FROM files WHERE generated"),
            "parse_errors": count(
                "SELECT count(*) FROM files WHERE parse_errors IS NOT NULL"
            ),
        },
        "graph": graph,
        "ref": refs,
        "search": {
            "records": count("SELECT count(*) FROM chunks"),
            "kind": grouped("SELECT kind, count(*) FROM chunks GROUP BY kind"),
        },
        "framework": json.loads(store.get_meta("frameworks") or "{}"),
    }


def coverage(store: Store) -> dict[str, dict[str, Any]]:
    """Покрытие графа по языкам: доля файлов с символами, от которых зависит другой файл.

    Файл покрыт, если хотя бы одно доказанное ребро из другого файла ведёт в его символ:
    его вызывают, создают, наследуют или импортируют. Непокрытый файл — либо точка входа
    (контроллер, команда), либо место, где граф потерял связи. Число неразрешённых ссылок
    показывает размер дыр, а эта доля — насколько граф полон в целом. Догадки не
    засчитываются, тесты и документы не считаются.

    Returns:
        По языку: число файлов с символами, число покрытых и их доля.
    """
    rows = store.execute(
        """
        SELECT f.language, count(*), sum(EXISTS (
            SELECT 1 FROM nodes t
            JOIN edges e ON e.target = t.id
            JOIN nodes s ON s.id = e.source
            WHERE t.file_id = f.id AND s.file_id != f.id
              AND e.kind != 'contains' AND e.provenance != 'heuristic'
        ))
        FROM files f
        WHERE f.scope = 'source' AND f.language != 'markdown' AND NOT f.generated
          AND EXISTS (SELECT 1 FROM nodes n WHERE n.file_id = f.id AND n.kind != 'file')
        GROUP BY f.language ORDER BY f.language
        """
    ).fetchall()
    return {
        str(language): {
            "files": int(files),
            "covered": int(covered),
            "share": round(int(covered) / int(files), 3),
        }
        for language, files, covered in rows
    }


def affected(
    store: Store, paths: Sequence[str], *, depth: int, pattern: str | None
) -> dict[str, Any]:
    """Тесты, которых касаются изменённые файлы: обход обратных рёбер по файлам.

    Файл зависит от изменённого, если любое его ребро, кроме ``contains``, ведёт в символ
    изменённого файла. Догадки тоже засчитываются: лишний прогон теста дешевле
    пропущенного. Тестом считается файл области ``test`` или, с ``pattern``, файл,
    чей путь подходит под маску.

    Returns:
        Затронутые тесты по близости к изменениям и пути, которых нет в индексе.
    """
    known = {
        str(path): int(file_id)
        for path, file_id in store.execute("SELECT path, id FROM files")
    }
    unknown = [path for path in paths if path not in known]
    frontier = {known[path] for path in paths if path in known}
    distance = dict.fromkeys(frontier, 0)
    for level in range(1, depth + 1):
        if not frontier:
            break
        found: set[int] = set()
        batch = sorted(frontier)
        for offset in range(0, len(batch), 500):
            part = batch[offset : offset + 500]
            placeholders = ",".join("?" for _ in part)
            found.update(
                int(file_id)
                for (file_id,) in store.execute(
                    "SELECT DISTINCT s.file_id FROM nodes t "  # noqa: S608  # подставляются только знаки ?
                    "JOIN edges e ON e.target = t.id AND e.kind != 'contains' "
                    f"JOIN nodes s ON s.id = e.source WHERE t.file_id IN ({placeholders})",
                    part,
                )
            )
        frontier = found - set(distance)
        distance.update(dict.fromkeys(frontier, level))
    files = {
        int(file_id): (str(path), str(scope))
        for file_id, path, scope in store.execute("SELECT id, path, scope FROM files")
    }
    tests = sorted(
        (level, files[file_id][0])
        for file_id, level in distance.items()
        if file_id in files and _is_test(*files[file_id], pattern)
    )
    result: dict[str, Any] = {"files": [path for _, path in tests]}
    if unknown:
        result["unknown"] = unknown
    return result


def _is_test(path: str, scope: str, pattern: str | None) -> bool:
    if pattern is None:
        return scope == "test"
    return fnmatch.fnmatchcase(path, pattern)


def read_lines(root: Path, path: str, start: int, end: int) -> str:
    """Строки файла с номерами, как их показывает чтение файла; прямо с диска."""
    try:
        lines = (root / path).read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError as error:
        return f"<cannot read file: {error}>"
    last = min(end, start + MAX_CODE_LINES - 1, len(lines))
    numbered = [f"{number}\t{lines[number - 1]}" for number in range(start, last + 1)]
    if end > last:
        numbered.append(f"... truncated: {end - last} more lines")
    return "\n".join(numbered)


def _innermost(nodes: list[NodeRow], line: int) -> NodeRow:
    containing = [n for n in nodes if n.start_line <= line <= n.end_line]
    return min(containing, key=lambda n: (n.end_line - n.start_line, n.kind == "file"))


def _edges(store: Store, node_id: int, *, incoming: bool, limit: int) -> dict[str, Any]:
    """Связи узла по видам: символ на том конце, строки связи ``at``, пометка догадки.

    Вызовы из одного символа в нескольких строках — одна запись. ``limit`` считает символы;
    не показанные сверх него — в ``more`` по видам.

    Returns:
        Связи по видам и число не показанных; пустой словарь, если связей нет.
    """
    near, far = ("target", "source") if incoming else ("source", "target")
    rows = store.execute(
        f"SELECT {far}, kind, line, provenance, confidence FROM edges "  # noqa: S608  # имена колонок — литералы
        f"WHERE {near} = ? AND kind != 'contains' ORDER BY kind, line",
        (node_id,),
    ).fetchall()
    grouped: dict[str, dict[int, list[Evidence]]] = {}
    for other_id, kind, line, provenance, confidence in rows:
        grouped.setdefault(str(kind), {}).setdefault(int(other_id), []).append(
            (line, str(provenance), float(confidence))
        )
    shown = {kind: list(others.items())[:limit] for kind, others in grouped.items()}
    nodes = store.nodes_by_ids(other for items in shown.values() for other, _ in items)
    result: dict[str, Any] = {}
    for kind, items in shown.items():
        linked = [
            _linked(nodes[other], proofs) for other, proofs in items if other in nodes
        ]
        if linked:
            result[kind] = linked
    more = {
        kind: len(others) - limit
        for kind, others in grouped.items()
        if len(others) > limit
    }
    if more:
        result["more"] = more
    return result


def _linked(node: NodeRow, proofs: list[Evidence]) -> dict[str, Any]:
    """Символ на том конце связи; ``guess`` — только если все его связи угаданы по имени."""
    entry = summary(node)
    lines = sorted({line for line, _, _ in proofs if line is not None})
    if lines:
        entry["at"] = lines
    # WHY: агенту важно одно — можно ли верить ребру. Доказанная связь идёт без пометки,
    # догадка несёт свою уверенность, чтобы её проверили чтением кода.
    if all(provenance == "heuristic" for _, provenance, _ in proofs):
        entry["guess"] = max(confidence for _, _, confidence in proofs)
    return entry


def candidate_files(store: Store, raw: str) -> list[str]:
    """Места кандидатов неоднозначной ссылки: их можно сразу передать в ``atlas node``."""
    ids = [int(node_id) for node_id in _json_list(raw)]
    nodes = store.nodes_by_ids(ids)
    return [
        location(node.path, node.start_line, node.end_line)
        for node_id in ids
        if (node := nodes.get(node_id)) is not None
    ]


def _unresolved_from(store: Store, node_id: int) -> list[dict[str, Any]]:
    rows = store.execute(
        "SELECT line, kind, name, status, candidates FROM refs "
        "WHERE source = ? AND status IN ('unresolved', 'ambiguous') ORDER BY line LIMIT 30",
        (node_id,),
    ).fetchall()
    return [
        {
            "line": ln,
            "kind": k,
            "name": n,
            "status": s,
            **({"candidates": candidate_files(store, c)} if c else {}),
        }
        for ln, k, n, s, c in rows
    ]


def _json_list(raw: str) -> list[Any]:
    value = json.loads(raw)
    return value if isinstance(value, list) else []
