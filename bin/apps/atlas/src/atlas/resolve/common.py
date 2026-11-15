"""Общие данные и правила разрешения, без файловой системы и SQL."""

from dataclasses import dataclass
from typing import TYPE_CHECKING

from atlas.model import EdgeKind, Language, NodeKind, Provenance, RefStatus

if TYPE_CHECKING:
    from atlas.records import NodeRow, RefRow
    from atlas.resolve.index import NodeIndex

MAX_CANDIDATES = 20
_TYPE_KINDS = frozenset({"class", "interface", "trait", "enum"})
_TS_TOP_KINDS = frozenset(
    {"function", "class", "interface", "type", "enum", "constant"}
)
_TS_LANGUAGES = frozenset({Language.TYPESCRIPT, Language.TSX, Language.JAVASCRIPT})
_PY_TOP_KINDS = frozenset(
    {"function", "class", "interface", "enum", "type", "constant"}
)
_MAX_HIERARCHY = 64


@dataclass(frozen=True, slots=True)
class Resolution:
    """Итог разрешения одной ссылки."""

    status: RefStatus
    target: NodeRow | None = None
    provenance: Provenance = Provenance.RESOLVED
    confidence: float = 0.0
    via: str = ""
    candidates: tuple[NodeRow, ...] = ()
    # Вид ребра, если он отличается от вида ссылки: ``T(x)`` синтаксически вызов, а по смыслу ссылка на тип.
    kind: EdgeKind | None = None


_EXTERNAL = Resolution(RefStatus.EXTERNAL)
_UNRESOLVED = Resolution(RefStatus.UNRESOLVED)


class BaseResolver:
    """Общие операции над снимком символов."""

    def __init__(self, index: NodeIndex) -> None:
        self._index = index

    def _by_unique_name(
        self, ref: RefRow, kind: NodeKind | None, language: Language | None
    ) -> Resolution:
        """Однозначное имя в кодовой базе ссылки; несколько — ``ambiguous``, ноль — внешнее."""
        codebase = self._codebase_of(ref.path)
        own = language or self._language_of(ref.path)
        family = (
            {Language(own)}
            if own in {Language.PHP, Language.PYTHON, Language.GO}
            else set(_TS_LANGUAGES)
        )
        candidates = [
            n
            for n in self._named(ref.name, codebase)
            if n.language in family
            and (
                n.kind == kind
                if kind is not None
                else n.kind in _TS_TOP_KINDS | {"function"}
            )
        ]
        if len(candidates) == 1:
            return Resolution(
                RefStatus.RESOLVED,
                candidates[0],
                Provenance.HEURISTIC,
                0.5,
                "names.unique-in-codebase",
            )
        if candidates:
            return self._ambiguous(candidates)
        return _EXTERNAL

    def _ambiguous(self, nodes: list[NodeRow]) -> Resolution:
        return Resolution(RefStatus.AMBIGUOUS, candidates=tuple(nodes[:MAX_CANDIDATES]))

    def _lower_qn(self, qualified_name: str) -> list[NodeRow]:
        return self._index.qualified(qualified_name)

    def _named(self, name: str, codebase: str) -> list[NodeRow]:
        return self._index.named(name, codebase)

    def _file_nodes(self, path: str) -> list[NodeRow]:
        return self._index.in_file(path)

    def _file_node(self, path: str) -> NodeRow | None:
        nodes = self._file_nodes(path)
        return next((n for n in nodes if n.kind == "file"), None)

    def _codebase_of(self, path: str) -> str:
        node = self._file_node(path)
        return node.codebase if node is not None else "."

    def _language_of(self, path: str) -> str | None:
        node = self._file_node(path)
        return node.language if node is not None else None

    def _member(self, owner: NodeRow, member: str) -> NodeRow | None:
        qualified = f"{owner.qualified_name}.{member}"
        found = [
            n for n in self._file_nodes(owner.path) if n.qualified_name == qualified
        ]
        return found[0] if found else None
