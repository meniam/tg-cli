"""Нарезка файла на записи полнотекстового поиска.

Якоря — начало файла, классы и функции с методами (для Markdown — заголовки). Запись
покрывает строки от своего якоря до следующего; длинный участок режется кусками по
``CHUNK_LINES`` строк, чтобы поиск попадал в нужное место длинного метода.
"""

import re
from dataclasses import dataclass, replace
from pathlib import PurePosixPath
from typing import TYPE_CHECKING

from atlas.model import Chunk, FileExtract, Language, Node, NodeKind
from atlas.text import normalize

if TYPE_CHECKING:
    from collections.abc import Iterable, Sequence

CHUNK_LINES = 120
# Вид записи раздела Markdown; документ это или правило агента, говорит область файла.
SECTION = "section"

_ANCHOR_KINDS = frozenset(
    {
        NodeKind.CLASS,
        NodeKind.INTERFACE,
        NodeKind.TRAIT,
        NodeKind.ENUM,
        NodeKind.FUNCTION,
        NodeKind.METHOD,
        NodeKind.TYPE,
    }
)
_HEADING = re.compile(r"^(#{1,6})\s+(.+?)\s*#*\s*$")
_FENCE = re.compile(r"^\s*(```|~~~)")
_LINK = re.compile(r"\[[^\]]+\]\(([^)#\s]+)(?:#[^)]*)?\)")


@dataclass(frozen=True, slots=True)
class _Anchor:
    line: int
    kind: str
    title: str
    symbols: tuple[str, ...]
    node_key: str | None


def code_chunks(extract: FileExtract, source: str) -> tuple[Chunk, ...]:
    """Записи поиска для файла кода по узлам, которые нашёл извлекатель.

    Вид записи — вид узла графа, как в ``node``: язык виден по расширению, тест — по
    области файла.

    Returns:
        Записи поиска файла.
    """
    file_node = next((n for n in extract.nodes if n.kind is NodeKind.FILE), None)
    relations = _relations(file_node)
    anchors = [
        _Anchor(1, NodeKind.FILE, extract.path, (), file_node.id if file_node else None)
    ]
    anchors.extend(
        _Anchor(
            node.start_line,
            node.kind,
            node.qualified_name,
            tuple(dict.fromkeys((node.name, node.qualified_name))),
            node.id,
        )
        for node in sorted(
            (n for n in extract.nodes if n.kind in _ANCHOR_KINDS),
            key=lambda n: (n.start_line, n.id),
        )
    )
    return _chunks(extract.path, source.splitlines(), anchors, relations)


def markdown_extract(path: str, source: bytes) -> FileExtract:
    """Файловый узел и записи поиска по заголовкам Markdown."""
    text = source.decode("utf-8", errors="replace")
    lines = text.splitlines()
    anchors = [_Anchor(1, SECTION, path, (), None)]
    in_fence = False
    for number, line in enumerate(lines, start=1):
        if _FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        match = _HEADING.match(line)
        if match is not None and number > 1:
            heading = match.group(2).strip()
            anchors.append(_Anchor(number, SECTION, heading, (heading,), None))
        elif match is not None:
            heading = match.group(2).strip()
            anchors[0] = _Anchor(1, SECTION, heading, (heading,), None)
    relations = tuple(sorted(set(_LINK.findall(text))))
    node = Node(
        id=f"{NodeKind.FILE}:{path}:{path}",
        kind=NodeKind.FILE,
        name=PurePosixPath(path).name,
        qualified_name=path,
        path=path,
        language=Language.MARKDOWN,
        start_line=1,
        end_line=max(len(lines), 1),
    )
    chunks = tuple(
        replace(chunk, node_key=None)
        for chunk in _chunks(path, lines, anchors, relations)
    )
    return FileExtract(path, Language.MARKDOWN, (node,), (), (), (), chunks)


def _relations(file_node: Node | None) -> tuple[str, ...]:
    if file_node is None:
        return ()
    uses = file_node.meta.get("uses", [])
    imports = {
        entry["from"]
        for entry in file_node.meta.get("imports", {}).values()
        if "from" in entry
    }
    return tuple(sorted({*uses, *imports}))


def _chunks(
    path: str,
    lines: Sequence[str],
    anchors: Iterable[_Anchor],
    relations: tuple[str, ...],
) -> tuple[Chunk, ...]:
    ordered = sorted(anchors, key=lambda anchor: anchor.line)
    total = max(len(lines), 1)
    # Поля нормализуются по отдельности: склейка готовых полей равна нормализации склейки,
    # а готовые поля нужны ранжированию. Связи общие для файла и считаются один раз.
    search_path = normalize(path)
    search_relations = normalize(" ".join(relations))
    result: list[Chunk] = []
    for index, anchor in enumerate(ordered):
        start = max(anchor.line, 1)
        end = min(
            total, (ordered[index + 1].line - 1) if index + 1 < len(ordered) else total
        )
        if end < start:
            continue
        for chunk_start in range(start, end + 1, CHUNK_LINES):
            chunk_end = min(end, chunk_start + CHUNK_LINES - 1)
            content = "\n".join(lines[chunk_start - 1 : chunk_end])
            search_content = normalize(content)
            search_text = " ".join(
                field
                for field in (
                    normalize(anchor.title),
                    search_path,
                    normalize(" ".join(anchor.symbols)),
                    search_relations,
                    search_content,
                )
                if field
            )
            result.append(
                Chunk(
                    kind=anchor.kind,
                    start_line=chunk_start,
                    end_line=chunk_end,
                    title=anchor.title,
                    symbols=anchor.symbols,
                    content=content,
                    search_text=search_text,
                    search_content=search_content,
                    search_relations=search_relations,
                    node_key=anchor.node_key,
                )
            )
    return tuple(result)
