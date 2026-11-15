"""Разрешение путей импорта Go в каталоги пакетов проекта.

Пакет Go — каталог с файлами ``.go``. Путь импорта состоит из пути модуля из ближайшего
``go.mod`` и каталога внутри модуля: ``example.com/shop/orders`` при модуле
``example.com/shop`` в корне — это каталог ``orders``. Локальные ``replace`` из ``go.mod``
работают как ещё один модуль. Путь с незнакомым проекту префиксом — стандартная библиотека
или внешний модуль.
"""

import posixpath
from functools import cached_property
from typing import TYPE_CHECKING, Any

from atlas.resolve.modules import EXTERNAL

if TYPE_CHECKING:
    from collections.abc import Collection, Mapping

_TEST_SUFFIX = "_test.go"


class GoModules:
    """Находит каталог пакета по пути импорта и файлы пакета по каталогу."""

    def __init__(
        self, files: Collection[str], configs: Mapping[str, dict[str, Any]]
    ) -> None:
        self._files = files
        self._configs = configs

    def resolve(self, import_path: str) -> str | None:
        """Каталог пакета, ``EXTERNAL`` для внешнего пути или ``None``, если каталога нет."""
        for module, base in self._roots:
            if import_path == module:
                rest = ""
            elif import_path.startswith(module + "/"):
                rest = import_path[len(module) + 1 :]
            else:
                continue
            directory = "/".join(part for part in (base, rest) if part)
            if directory.startswith(".."):
                return EXTERNAL
            return directory if directory in self._dirs else None
        return EXTERNAL

    def files_in(self, directory: str) -> list[str]:
        """Файлы ``.go`` каталога по возрастанию пути."""
        return self._dirs.get(directory, [])

    def entry(self, directory: str) -> str | None:
        """Файл, на который ведёт импорт пакета, или ``None``, если в каталоге нет файлов.

        Импорт целится в пакет, а у пакета нет одного файла, поэтому берётся самый
        говорящий: файл с именем каталога, затем ``doc.go``, затем первый по имени.
        Тестовые файлы выбираются, только если других нет.

        Returns:
            Путь файла или ``None``.
        """
        files = self.files_in(directory)
        source = [path for path in files if not path.endswith(_TEST_SUFFIX)] or files
        if not source:
            return None
        name = posixpath.basename(directory) or "main"
        for wanted in (f"{name}.go", "doc.go"):
            for path in source:
                if posixpath.basename(path) == wanted:
                    return path
        return source[0]

    @cached_property
    def _dirs(self) -> dict[str, list[str]]:
        result: dict[str, list[str]] = {}
        for path in sorted(self._files):
            if path.endswith(".go"):
                result.setdefault(posixpath.dirname(path), []).append(path)
        return result

    @cached_property
    def _roots(self) -> tuple[tuple[str, str], ...]:
        """Пары ``(путь модуля, каталог)``, самые длинные пути модулей первыми."""
        roots: list[tuple[str, str]] = []
        for path, data in self._configs.items():
            if posixpath.basename(path) != "go.mod":
                continue
            base = posixpath.dirname(path)
            module = data.get("module")
            if isinstance(module, str) and module:
                roots.append((module, base))
            replaced = data.get("replace")
            if not isinstance(replaced, dict):
                continue
            for name, target in replaced.items():
                if isinstance(target, str):
                    local = posixpath.normpath(posixpath.join(base, target))
                    roots.append((str(name), "" if local == "." else local))
        return tuple(sorted(roots, key=lambda root: (-len(root[0]), root[0], root[1])))
