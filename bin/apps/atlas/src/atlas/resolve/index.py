"""Снимок символов в памяти и зависимости разрешения исходных файлов."""

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from collections.abc import Iterable

    from atlas.records import NodeRow


def node_keys(node: NodeRow) -> set[str]:
    """Ключи поиска, на которые влияет изменение узла."""
    keys = {
        f"file:{node.path}",
        f"qualified:{node.qualified_name.lower()}",
        f"name:{node.codebase}:{node.name}",
    }
    if (
        node.language == "php"
        and node.kind in {"class", "interface", "trait", "enum"}
        and "\\" in node.qualified_name
    ):
        keys.add(f"php-namespace:{node.qualified_name.split('\\', 1)[0].lower()}")
    return keys


class NodeIndex:
    """Индексы снимка; чтение не обращается к SQLite или диску.

    Зависимости объединяются по исходному файлу: при его инвалидации разрешаются
    все его ссылки, поэтому не нужны миллионы повторяющихся строк на каждую ссылку.
    """

    def __init__(self, nodes: Iterable[NodeRow]) -> None:
        self._qualified: dict[str, list[NodeRow]] = {}
        self._named: dict[tuple[str, str], list[NodeRow]] = {}
        self._files: dict[str, list[NodeRow]] = {}
        self._namespaces: set[str] = set()
        self.dependencies: dict[int, set[str]] = {}
        self._source = 0
        for node in nodes:
            self._qualified.setdefault(node.qualified_name.lower(), []).append(node)
            self._named.setdefault((node.codebase, node.name), []).append(node)
            self._files.setdefault(node.path, []).append(node)
            if (
                node.language == "php"
                and node.kind in {"class", "interface", "trait", "enum"}
                and "\\" in node.qualified_name
            ):
                self._namespaces.add(node.qualified_name.split("\\", 1)[0].lower())

    def start(self, file_id: int) -> None:
        """Начать сбор зависимостей следующей ссылки файла."""
        self._source = file_id
        self.dependencies.setdefault(file_id, set())

    def track(self, key: str) -> None:
        """Учесть прочитанный ключ, в том числе отсутствующий символ."""
        self.dependencies[self._source].add(key)

    def qualified(self, name: str) -> list[NodeRow]:
        """Найти полное имя без учёта регистра."""
        self.track(f"qualified:{name.lower()}")
        return self._qualified.get(name.lower(), [])

    def named(self, name: str, codebase: str) -> list[NodeRow]:
        """Найти короткое имя внутри кодовой базы."""
        self.track(f"name:{codebase}:{name}")
        return self._named.get((codebase, name), [])

    def in_file(self, path: str) -> list[NodeRow]:
        """Узлы файла; фиксирует также промежуточные реэкспорты и типы."""
        self.track(f"file:{path}")
        return self._files.get(path, [])

    def has_php_namespace(self, namespace: str) -> bool:
        """Есть ли пространство в проекте; зависит только от этого пространства."""
        self.track(f"php-namespace:{namespace}")
        return namespace in self._namespaces
