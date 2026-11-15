"""Данные графа, которые извлекатели передают хранилищу и разрешателю.

Записи имеют frozen-поля и сериализуются между процессами. Вложенные метаданные —
обычные словари; после передачи результата извлекатель их больше не изменяет.
"""

from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any

from atlas.bindings import Binding


class Language(StrEnum):
    """Язык файла; определяет грамматику и извлекатель."""

    PHP = "php"
    TYPESCRIPT = "typescript"
    TSX = "tsx"
    JAVASCRIPT = "javascript"
    PYTHON = "python"
    GO = "go"
    MARKDOWN = "markdown"


class NodeKind(StrEnum):
    """Вид узла графа."""

    FILE = "file"
    CLASS = "class"
    INTERFACE = "interface"
    TRAIT = "trait"
    ENUM = "enum"
    FUNCTION = "function"
    METHOD = "method"
    PROPERTY = "property"
    CONSTANT = "constant"
    TYPE = "type"
    ROUTE = "route"


TYPE_KINDS = frozenset(
    {NodeKind.CLASS, NodeKind.INTERFACE, NodeKind.TRAIT, NodeKind.ENUM}
)
CALLABLE_KINDS = frozenset({NodeKind.FUNCTION, NodeKind.METHOD})


class EdgeKind(StrEnum):
    """Вид ребра графа."""

    CONTAINS = "contains"
    IMPORTS = "imports"
    CALLS = "calls"
    INSTANTIATES = "instantiates"
    EXTENDS = "extends"
    IMPLEMENTS = "implements"
    USES_TRAIT = "uses_trait"
    REFERENCES = "references"
    RENDERS = "renders"
    ROUTES_TO = "routes_to"
    REQUESTS = "requests"


class Provenance(StrEnum):
    """Происхождение ребра: чем доказана связь."""

    SYNTAX = "syntax"
    RESOLVED = "resolved"
    FRAMEWORK = "framework"
    COMPILED = "compiled"
    HEURISTIC = "heuristic"


class RefStatus(StrEnum):
    """Состояние ссылки после разрешения."""

    PENDING = "pending"
    RESOLVED = "resolved"
    AMBIGUOUS = "ambiguous"
    UNRESOLVED = "unresolved"
    EXTERNAL = "external"


# Флаги узла. Битовая маска, чтобы фильтровать в SQL без разбора JSON.
FLAG_EXPORTED = 1
FLAG_STATIC = 2
FLAG_ABSTRACT = 4
FLAG_ASYNC = 8
FLAG_DEFAULT_EXPORT = 16


@dataclass(frozen=True, slots=True)
class Node:
    """Символ или файл.

    ``id`` стабилен между сборками: ``kind:path:qualified_name``, для перегрузок с
    суффиксом строки. Сдвиг строк внутри файла идентификатор не меняет.
    """

    id: str
    kind: NodeKind
    name: str
    qualified_name: str
    path: str
    language: Language
    start_line: int
    end_line: int
    signature: str | None = None
    docblock: str | None = None
    attributes: tuple[dict[str, Any], ...] = ()
    flags: int = 0
    meta: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class Ref:
    """Ссылка из узла на имя, которое ещё предстоит разрешить в узел.

    ``kind`` совпадает с видом будущего ребра. ``context`` несёт всё, что знал
    извлекатель: кандидаты полных имён, тип получателя, класс вызова, привязку импорта.
    """

    source: str
    name: str
    kind: EdgeKind
    line: int
    col: int
    context: Binding = field(default_factory=Binding)

    @property
    def name_tail(self) -> str:
        r"""Последний сегмент имени: ``App\\Foo::bar`` → ``bar``, ``a.b`` → ``b``."""
        tail = self.name
        for separator in ("::", "\\", "."):
            tail = tail.rsplit(separator, 1)[-1]
        return tail


@dataclass(frozen=True, slots=True)
class Edge:
    """Связь между двумя узлами с доказательством её происхождения."""

    source: str
    target: str
    kind: EdgeKind
    line: int | None
    col: int | None
    provenance: Provenance
    confidence: float
    via: str
    origin_path: str


@dataclass(frozen=True, slots=True)
class Chunk:
    """Запись полнотекстового поиска: участок файла от якоря до следующего, до 120 строк.

    ``search_text`` — нормализованные заголовок, путь, символы, связи и текст; по нему
    ищет FTS5. ``content`` — исходный текст для отрывка. ``search_content`` и
    ``search_relations`` — нормализованные текст и связи для ранжирования: поиск берёт их
    готовыми, а не нормализует заново. Связи общие для файла и хранятся в ``files``.
    """

    kind: str
    start_line: int
    end_line: int
    title: str
    symbols: tuple[str, ...]
    content: str
    search_text: str
    search_content: str
    search_relations: str
    node_key: str | None = None


@dataclass(frozen=True, slots=True)
class HttpCall:
    """Вызов с путём-литералом первым аргументом: ``fetch('/api/x')``, ``getJson(`/a/${id}`)``.

    Извлекатель не знает конфигурации, поэтому пишет все такие вызовы; HTTP-мост сам решает
    по ``callee``, запрос ли это. ``path`` — статическая часть без query и hash; вставки
    ``${...}`` внутри пути заменены на ``{}``. ``method`` известен из литерала ``method:`` в
    опциях вызова, иначе пуст.
    """

    source: str
    callee: str
    path: str
    method: str
    line: int
    col: int


@dataclass(frozen=True, slots=True)
class FileExtract:
    """Всё, что извлекатель нашёл в одном файле."""

    path: str
    language: Language
    nodes: tuple[Node, ...]
    refs: tuple[Ref, ...]
    edges: tuple[Edge, ...]
    errors: tuple[str, ...] = ()
    chunks: tuple[Chunk, ...] = ()
    generated: bool = False
    http_calls: tuple[HttpCall, ...] = ()


def node_id(kind: NodeKind, path: str, qualified_name: str) -> str:
    """Собрать стабильный идентификатор узла."""
    return f"{kind}:{path}:{qualified_name}"
