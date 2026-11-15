"""Типизированные записи сохранённого графа, независимые от хранилища."""

from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from atlas.bindings import Binding


@dataclass(frozen=True, slots=True)
class FileRow:
    """Состояние файла на момент последней синхронизации."""

    id: int
    path: str
    language: str
    codebase: str
    scope: str
    size: int
    mtime_ns: int
    sha256: str


@dataclass(frozen=True, slots=True)
class RefRow:
    """Ссылка вместе с текущим результатом разрешения. ``path`` — файл ссылки."""

    id: int
    source: int
    file_id: int
    path: str
    name: str
    kind: str
    line: int
    col: int
    context: Binding
    status: str
    target: int | None


@dataclass(frozen=True, slots=True)
class NodeRow:
    """Узел из базы в виде, удобном разрешателю и запросам."""

    id: int
    key: str
    file_id: int
    kind: str
    name: str
    qualified_name: str
    path: str
    codebase: str
    language: str
    start_line: int
    end_line: int
    signature: str | None
    flags: int
    meta: dict[str, Any]
