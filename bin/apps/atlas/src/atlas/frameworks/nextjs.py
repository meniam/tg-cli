"""Файловая маршрутизация Next.js: App Router и Pages Router.

Маршрут выводится из пути файла: ``app/(app)/suppliers/[id]/page.tsx`` →
``/suppliers/:id``. Группы ``(x)`` и слоты ``@x`` выбрасываются из URL, приватные каталоги
``_x`` маршрутов не дают. ``route.ts`` даёт по маршруту на каждый экспортированный метод HTTP.
"""

import json
import posixpath
import re
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from atlas.frameworks.routes import RouteSpec, Target
from atlas.model import EdgeKind

if TYPE_CHECKING:
    from collections.abc import Iterable, Iterator
    from pathlib import Path

_HTTP_METHODS = ("GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS")
_PAGE_EXTENSIONS = frozenset({".tsx", ".ts", ".jsx", ".js"})
_APP_ROOTS = ("app", "src/app")
_PAGES_ROOTS = ("pages", "src/pages")
_INTERCEPT = re.compile(r"^\((\.{1,3}|\.\.\)\(\.\.)\)")


@dataclass(frozen=True, slots=True)
class SourceFile:
    """Файл кодовой базы Next.js: ключ файла, экспорты и объявленные в нём символы.

    ``symbols`` — полное имя символа верхнего уровня → ключ узла и псевдоним из
    ``export const GET = handle``.
    """

    path: str
    key: str
    exports: dict[str, Any]
    symbols: dict[str, tuple[str, str]]


def detect(codebase_dir: Path) -> bool:
    """Есть ли Next.js в кодовой базе: пакет ``next`` в ``package.json``."""
    try:
        data = json.loads((codebase_dir / "package.json").read_text(encoding="utf-8"))
    except OSError, ValueError:
        return False
    if not isinstance(data, dict):
        return False
    dependencies = {**data.get("dependencies", {}), **data.get("devDependencies", {})}
    return "next" in dependencies


def input_files(codebase_dir: Path) -> list[Path]:
    """Файлы, изменение которых меняет маршруты."""
    return [codebase_dir / "package.json"]


def routes(codebase: str, files: Iterable[SourceFile]) -> list[RouteSpec]:
    """Маршруты App Router и Pages Router из файлов кодовой базы."""
    base = "" if codebase in {"", "."} else f"{codebase}/"
    found: list[RouteSpec] = []
    for source in files:
        relative = source.path.removeprefix(base)
        for root in _APP_ROOTS:
            if relative.startswith(f"{root}/"):
                found += _app_routes(source, relative.removeprefix(f"{root}/"))
                break
        else:
            for root in _PAGES_ROOTS:
                if relative.startswith(f"{root}/"):
                    found += _pages_routes(source, relative.removeprefix(f"{root}/"))
                    break
    return found


def _app_routes(source: SourceFile, relative: str) -> Iterator[RouteSpec]:
    directory, _, filename = relative.rpartition("/")
    stem, extension = posixpath.splitext(filename)
    if extension not in _PAGE_EXTENSIONS or stem not in {"page", "route"}:
        return
    path = _url(directory.split("/") if directory else [])
    if path is None:
        return
    if stem == "page":
        yield _page(source, path, "next.page")
        return
    for method in _HTTP_METHODS:
        export = source.exports.get(method)
        if not isinstance(export, dict):
            continue
        local = str(export.get("local", method))
        symbol = source.symbols.get(local)
        keys: tuple[str, ...] = ()
        if symbol is not None:
            key, alias = symbol
            aliased = source.symbols.get(alias) if alias else None
            keys = (aliased[0], key) if aliased is not None else (key,)
        yield RouteSpec(
            framework="nextjs",
            path=path,
            methods=(method,),
            name="",
            owner=source.key,
            line=1,
            targets=(Target(EdgeKind.ROUTES_TO, "next.route-handler", keys=keys),)
            if keys
            else (),
            meta={"type": "route"},
        )


def _pages_routes(source: SourceFile, relative: str) -> Iterator[RouteSpec]:
    stem, extension = posixpath.splitext(relative)
    if extension not in _PAGE_EXTENSIONS:
        return
    parts = stem.split("/")
    if parts[-1] == "index":
        parts = parts[:-1]
    if parts and parts[0] in {"_app", "_document", "_error"}:
        return
    path = _url(parts)
    if path is None:
        return
    if parts and parts[0] == "api":
        default = source.exports.get("default")
        local = str(default.get("local", "")) if isinstance(default, dict) else ""
        symbol = source.symbols.get(local)
        yield RouteSpec(
            framework="nextjs",
            path=path,
            methods=(),
            name="",
            owner=source.key,
            line=1,
            targets=(Target(EdgeKind.ROUTES_TO, "next.api-route", keys=(symbol[0],)),)
            if symbol is not None
            else (),
            meta={"type": "route"},
        )
        return
    yield _page(source, path, "next.pages-page")


def _page(source: SourceFile, path: str, via: str) -> RouteSpec:
    default = source.exports.get("default")
    local = str(default.get("local", "")) if isinstance(default, dict) else ""
    symbol = source.symbols.get(local)
    return RouteSpec(
        framework="nextjs",
        path=path,
        methods=("GET",),
        name="",
        owner=source.key,
        line=1,
        targets=(Target(EdgeKind.ROUTES_TO, via, keys=(symbol[0],)),)
        if symbol is not None
        else (),
        meta={"type": "page"},
    )


def _url(segments: list[str]) -> str | None:
    """URL-шаблон из сегментов каталога или ``None`` для приватного и перехватывающего пути."""
    parts: list[str] = []
    for segment in segments:
        if not segment or (segment.startswith("(") and segment.endswith(")")):
            continue
        if segment.startswith("@"):
            continue
        if segment.startswith("_") or _INTERCEPT.match(segment):
            return None
        name = segment.replace("%5F", "_")
        if name.startswith("[[...") and name.endswith("]]"):
            parts.append(f":{name[5:-2]}*")
        elif name.startswith("[...") and name.endswith("]"):
            parts.append(f":{name[4:-1]}+")
        elif name.startswith("[") and name.endswith("]"):
            parts.append(f":{name[1:-1]}")
        else:
            parts.append(name)
    return "/" + "/".join(parts)
