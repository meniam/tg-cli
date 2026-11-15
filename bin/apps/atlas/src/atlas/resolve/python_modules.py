"""Разрешение имён модулей Python в файлы проекта.

Корень импорта — каталог над самым верхним предком файла с ``__init__.py``: так
``src/atlas/parse/common.py`` при ``src/atlas/__init__.py`` — это ``atlas.parse.common``, даже
если в ``parse/`` своего ``__init__.py`` нет. Файл вне пакетов импортируется от своего каталога:
``tests/test_sync.py`` — это ``test_sync``. Запасные имена — путь от ближайшего ``src/`` и от
корня проекта: они находят пакеты без единого ``__init__.py``. Относительный импорт считается
от каталога файла. Имя с незнакомым проекту первым сегментом — внешний пакет или стандартная
библиотека.
"""

import os.path
import posixpath
from functools import cached_property
from typing import TYPE_CHECKING

from atlas.resolve.modules import EXTERNAL

if TYPE_CHECKING:
    from collections.abc import Collection

_SUFFIXES = (".py", ".pyi")
_INITS = ("__init__.py", "__init__.pyi")


class PythonModules:
    """Находит файл модуля по имени импорта из файла ``from_path``."""

    def __init__(self, files: Collection[str]) -> None:
        self._files = files

    def resolve(self, module: str, from_path: str) -> str | None:
        """Путь файла, ``EXTERNAL`` для внешнего модуля или ``None``, если файл не найден."""
        if module.startswith("."):
            return self._relative(module, from_path)
        candidates = self._index.get(module)
        if candidates:
            return _nearest(candidates, from_path)
        return None if module.split(".", 1)[0] in self._roots else EXTERNAL

    def submodule(self, package_path: str, name: str) -> str | None:
        """Файл подмодуля ``name``, если ``package_path`` — ``__init__.py`` пакета."""
        directory, filename = posixpath.split(package_path)
        if filename not in _INITS:
            return None
        return self._probe(posixpath.join(directory, name) if directory else name)

    @cached_property
    def _index(self) -> dict[str, list[str]]:
        python = sorted(path for path in self._files if path.endswith(_SUFFIXES))
        packages = {
            posixpath.dirname(path)
            for path in python
            if posixpath.basename(path) in _INITS
        }
        primary: dict[str, list[str]] = {}
        fallback: dict[str, list[str]] = {}
        for path in python:
            parts = path.rsplit(".", 1)[0].split("/")
            if parts[-1] == "__init__":
                parts.pop()
            directories = len(path.split("/")) - 1
            top = next(
                (
                    i
                    for i in range(directories)
                    if "/".join(path.split("/")[: i + 1]) in packages
                ),
                None,
            )
            start = top if top is not None else directories
            if parts[start:]:
                primary.setdefault(".".join(parts[start:]), []).append(path)
            src = max(
                (i + 1 for i, part in enumerate(parts[:directories]) if part == "src"),
                default=0,
            )
            for offset in {src, 0} - {start}:
                if parts[offset:]:
                    fallback.setdefault(".".join(parts[offset:]), []).append(path)
        # WHY: запасное имя не спорит с основным: если имя уже дал файлу его пакет, путь
        # от ``src/`` или от корня другого файла его не перехватывает.
        for name, paths in fallback.items():
            primary.setdefault(name, paths)
        return primary

    @cached_property
    def _roots(self) -> frozenset[str]:
        return frozenset(name.split(".", 1)[0] for name in self._index)

    def _relative(self, module: str, from_path: str) -> str | None:
        level = len(module) - len(module.lstrip("."))
        base = posixpath.dirname(from_path)
        for _ in range(level - 1):
            if not base:
                return None
            base = posixpath.dirname(base)
        rest = module[level:]
        target = posixpath.join(base, *rest.split(".")) if rest else base
        return self._probe(target)

    def _probe(self, target: str) -> str | None:
        for suffix in _SUFFIXES:
            if target and target + suffix in self._files:
                return target + suffix
        for init in _INITS:
            candidate = posixpath.join(target, init) if target else init
            if candidate in self._files:
                return candidate
        return None


def _nearest(candidates: list[str], from_path: str) -> str:
    """Из одноимённых модулей — ближайший к импортирующему файлу по общему каталогу."""
    return max(
        candidates,
        key=lambda path: (len(os.path.commonpath([path, from_path])), -len(path)),
    )
