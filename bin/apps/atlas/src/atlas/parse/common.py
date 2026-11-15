"""Общие помощники извлекателей поверх дерева tree-sitter."""

import re
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from collections.abc import Iterator

    from tree_sitter import Node as TsNode

MAX_SIGNATURE = 400
MAX_DOCBLOCK = 2000
_SPACES = re.compile(r"\s+")


def text(node: TsNode | None) -> str:
    """Исходный текст узла дерева."""
    if node is None or node.text is None:
        return ""
    return node.text.decode("utf-8", errors="replace")


def line(node: TsNode) -> int:
    """Номер строки начала узла, с единицы."""
    return node.start_point.row + 1


def end_line(node: TsNode) -> int:
    """Номер строки конца узла, с единицы."""
    return node.end_point.row + 1


def col(node: TsNode) -> int:
    """Колонка начала узла, с нуля."""
    return node.start_point.column


def signature(declaration: TsNode, body: TsNode | None) -> str:
    """Заголовок объявления до тела, в одну строку."""
    source = declaration.text or b""
    if body is not None:
        source = source[: body.start_byte - declaration.start_byte]
    collapsed = _SPACES.sub(" ", source.decode("utf-8", errors="replace")).strip()
    return collapsed[:MAX_SIGNATURE]


def docblock(
    declaration: TsNode, attribute_types: frozenset[str] = frozenset()
) -> str | None:
    """Комментарий ``/** ... */`` непосредственно перед объявлением.

    Атрибуты и декораторы между комментарием и объявлением пропускаются.

    Returns:
        Текст документации или None, если комментария нет.

    """
    previous = declaration.prev_named_sibling
    while previous is not None and previous.type in attribute_types:
        previous = previous.prev_named_sibling
    if previous is None or previous.type != "comment":
        return None
    comment = text(previous)
    if not comment.startswith("/**"):
        return None
    return comment[:MAX_DOCBLOCK]


def walk(node: TsNode, stop: frozenset[str] = frozenset()) -> Iterator[TsNode]:
    """Потомки узла в порядке обхода; в поддеревья типов ``stop`` не заходит."""
    stack = list(reversed(node.children))
    while stack:
        current = stack.pop()
        yield current
        if current.type in stop:
            continue
        stack.extend(reversed(current.children))


def unquote(value: str) -> str:
    """Снять одинарные, двойные или обратные кавычки строкового литерала."""
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"`":
        return value[1:-1]
    return value
