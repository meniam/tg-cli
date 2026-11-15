"""Разрешение спецификаторов ES-модулей в файлы проекта.

Порядок как у TypeScript: относительный путь, ``paths`` и ``baseUrl`` из ближайшего
``tsconfig.json`` с учётом ``extends``, иначе внешний пакет.
"""

import posixpath
import re
from dataclasses import dataclass
from functools import cache
from pathlib import PurePosixPath
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from collections.abc import Collection, Mapping

EXTERNAL = "external"
_EXTENSIONS = (".ts", ".tsx", ".mts", ".cts", ".d.ts", ".js", ".jsx", ".mjs", ".cjs")
_JS_TO_TS = {
    ".js": (".ts", ".tsx"),
    ".jsx": (".tsx",),
    ".mjs": (".mts",),
    ".cjs": (".cts",),
}
_CONFIG_NAMES = ("tsconfig.json", "jsconfig.json")
_LINE_COMMENT = re.compile(r'("(?:\\.|[^"\\])*")|//[^\n]*|/\*.*?\*/', re.DOTALL)
_TRAILING_COMMA = re.compile(r",(\s*[}\]])")


@dataclass(frozen=True, slots=True)
class _TsConfig:
    directory: str
    base_url: str | None
    paths: tuple[tuple[str, tuple[str, ...]], ...]


class ModuleResolver:
    """Находит файл модуля по спецификатору импорта из файла ``from_path``."""

    def __init__(
        self, files: Collection[str], configs: Mapping[str, dict[str, Any]]
    ) -> None:
        self._configs = configs
        self._files = files
        self._config_for_dir = cache(self._config_for_dir_uncached)
        self.resolve = cache(self._resolve_uncached)

    def _resolve_uncached(self, specifier: str, from_path: str) -> str | None:
        """Путь файла, ``EXTERNAL`` для пакета или ``None``, если файл не найден."""
        if specifier.startswith((".", "/")):
            base = posixpath.normpath(
                posixpath.join(posixpath.dirname(from_path), specifier)
            )
            return self._probe(base)
        config = self._config_for_dir(posixpath.dirname(from_path))
        if config is not None:
            for pattern, targets in config.paths:
                captured = _match(pattern, specifier)
                if captured is None:
                    continue
                base_dir = (
                    config.base_url if config.base_url is not None else config.directory
                )
                for target in targets:
                    candidate = posixpath.normpath(
                        posixpath.join(base_dir, target.replace("*", captured))
                    )
                    found = self._probe(candidate)
                    if found is not None:
                        return found
                return None
            if config.base_url is not None:
                found = self._probe(
                    posixpath.normpath(posixpath.join(config.base_url, specifier))
                )
                if found is not None:
                    return found
        return EXTERNAL

    def _probe(self, base: str) -> str | None:
        if base.startswith("../") or base == "..":
            return None
        base = base.removeprefix("./")
        if base in self._files:
            return base
        suffix = PurePosixPath(base).suffix
        if suffix in _JS_TO_TS:
            stem = base[: -len(suffix)]
            for replacement in _JS_TO_TS[suffix]:
                if stem + replacement in self._files:
                    return stem + replacement
        for extension in _EXTENSIONS:
            if base + extension in self._files:
                return base + extension
        for extension in _EXTENSIONS:
            index = f"{base}/index{extension}"
            if index in self._files:
                return index
        return None

    def _config_for_dir_uncached(self, directory: str) -> _TsConfig | None:
        current = PurePosixPath(directory)
        while True:
            for name in _CONFIG_NAMES:
                candidate = current / name
                if str(candidate) in self._configs:
                    return self._load_config(str(candidate))
            if str(current) in {".", ""}:
                return None
            current = current.parent

    def _load_config(self, path: str, depth: int = 0) -> _TsConfig | None:
        data = self._configs.get(path, {})
        directory = posixpath.dirname(path) or "."
        base_url: str | None = None
        paths: tuple[tuple[str, tuple[str, ...]], ...] = ()
        extends = data.get("extends")
        if isinstance(extends, str) and extends.startswith(".") and depth < 5:
            parent_path = posixpath.normpath(posixpath.join(directory, extends))
            if not parent_path.endswith(".json"):
                parent_path += ".json"
            parent = (
                self._load_config(parent_path, depth + 1)
                if parent_path in self._configs
                else None
            )
            if parent is not None:
                base_url, paths = parent.base_url, parent.paths
        options = data.get("compilerOptions")
        if isinstance(options, dict):
            if isinstance(options.get("baseUrl"), str):
                base_url = posixpath.normpath(
                    posixpath.join(directory, options["baseUrl"])
                )
            raw_paths = options.get("paths")
            if isinstance(raw_paths, dict):
                paths = tuple(
                    (pattern, tuple(t for t in targets if isinstance(t, str)))
                    for pattern, targets in raw_paths.items()
                    if isinstance(targets, list)
                )
                if base_url is None:
                    base_url = directory
        return _TsConfig(directory=directory, base_url=base_url, paths=paths)


def _match(pattern: str, specifier: str) -> str | None:
    """Часть спецификатора, попавшая под ``*`` шаблона, или ``None``."""
    if "*" not in pattern:
        return "" if pattern == specifier else None
    prefix, _, suffix = pattern.partition("*")
    if (
        specifier.startswith(prefix)
        and specifier.endswith(suffix)
        and len(specifier) >= len(prefix) + len(suffix)
    ):
        return specifier[len(prefix) : len(specifier) - len(suffix)]
    return None
