"""Чтение конфигурационных входов на границе файловой системы."""

import hashlib
import json
import os
import posixpath
import re
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, TypedDict

if TYPE_CHECKING:
    from collections.abc import Mapping
    from pathlib import Path

_LINE_COMMENT = re.compile(r'("(?:\\.|[^"\\])*")|//[^\n]*|/\*.*?\*/', re.DOTALL)
_TRAILING_COMMA = re.compile(r",(\s*[}\]])")
_MARKERS = frozenset({"composer.json", "package.json", "pyproject.toml", "go.mod"})
_CONFIGS = frozenset({"tsconfig.json", "jsconfig.json"})


class DirectoryInputs(TypedDict):
    """Кэш имён конфигурационных входов по mtime каталога."""

    mtime: int | None
    names: list[str]


@dataclass(frozen=True, slots=True)
class ResolutionInputs:
    """Снимок конфигов и границ кодовых баз."""

    configs: dict[str, dict[str, Any]]
    digest: str
    markers: tuple[str, ...]
    directories: dict[str, DirectoryInputs]


def read_inputs(
    root: Path,
    directories: Mapping[str, int | None],
    previous: Mapping[str, DirectoryInputs],
) -> ResolutionInputs:
    """Прочитать конфиги, их extends и маркеры в каталогах анализируемого дерева."""
    configs: dict[str, dict[str, Any]] = {}
    markers: list[str] = []
    current: dict[str, DirectoryInputs] = {}

    def read_config(path: str, depth: int = 0) -> None:
        if path in configs or depth > 5:
            return
        data = read_jsonc(root / path)
        configs[path] = data
        parent = data.get("extends")
        if isinstance(parent, str) and parent.startswith("."):
            target = posixpath.normpath(posixpath.join(posixpath.dirname(path), parent))
            if not target.endswith(".json"):
                target += ".json"
            read_config(target, depth + 1)

    for directory, mtime in sorted(directories.items()):
        cached = previous.get(directory)
        if cached is not None and cached["mtime"] == mtime:
            names = set(cached["names"])
        else:
            try:
                with os.scandir(root / directory) as entries:
                    names = {
                        entry.name
                        for entry in entries
                        if entry.name in _MARKERS | _CONFIGS
                    }
            except FileNotFoundError, NotADirectoryError:
                continue
        current[directory] = {"mtime": mtime, "names": sorted(names)}
        for name in sorted(names):
            path = posixpath.join(directory, name) if directory else name
            if not (root / path).is_file():
                continue
            if name in _MARKERS:
                markers.append(path)
            if name == "go.mod":
                configs[path] = read_go_mod(root / path)
            elif name in _CONFIGS:
                read_config(path)
    digest = hashlib.sha256(json.dumps(configs, sort_keys=True).encode()).hexdigest()
    return ResolutionInputs(configs, digest, tuple(sorted(markers)), current)


_GO_MODULE = re.compile(r'^module\s+"?([^\s"]+)"?', re.MULTILINE)
_GO_REPLACE = re.compile(
    r'^\s*(?:replace\s+)?"?([^\s"=]+)"?(?:\s+v\S+)?\s*=>\s*"?([^\s"]+)"?',
    re.MULTILINE,
)


def read_go_mod(path: Path) -> dict[str, Any]:
    """Путь модуля и локальные ``replace`` из ``go.mod``; остальное разрешателю не нужно."""
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError:
        return {}
    raw = re.sub(r"//[^\n]*", "", raw)
    data: dict[str, Any] = {}
    module = _GO_MODULE.search(raw)
    if module is not None:
        data["module"] = module.group(1)
    replaced = {
        match.group(1): match.group(2)
        for match in _GO_REPLACE.finditer(raw)
        if match.group(2).startswith((".", "/"))
    }
    if replaced:
        data["replace"] = replaced
    return data


def read_jsonc(path: Path) -> dict[str, Any]:
    """Прочитать JSON с комментариями и висячими запятыми, как пишут tsconfig."""
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError:
        return {}
    stripped = _LINE_COMMENT.sub(lambda match: match.group(1) or "", raw)
    stripped = _TRAILING_COMMA.sub(r"\1", stripped)
    try:
        data = json.loads(stripped)
    except json.JSONDecodeError:
        return {}
    return data if isinstance(data, dict) else {}
