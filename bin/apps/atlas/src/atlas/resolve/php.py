"""Правила разрешения php; работают на снимке графа."""

from typing import TYPE_CHECKING

from atlas.model import Language, NodeKind, Provenance, RefStatus
from atlas.resolve.common import (
    _EXTERNAL,
    _MAX_HIERARCHY,
    _TYPE_KINDS,
    _UNRESOLVED,
    BaseResolver,
    Resolution,
)

if TYPE_CHECKING:
    from collections.abc import Iterable

    from atlas.records import NodeRow, RefRow


class PhpResolver(BaseResolver):
    """Разрешение ссылок языка php."""

    def resolve(self, ref: RefRow) -> Resolution:
        """Разрешить ссылку PHP."""
        context = ref.context
        if "fqcn" in context:
            return self._php_type(str(context["fqcn"]), ref)
        if "functions" in context:
            for candidate in context["functions"]:
                found = [
                    n for n in self._lower_qn(str(candidate)) if n.kind == "function"
                ]
                if len(found) == 1:
                    return Resolution(
                        RefStatus.RESOLVED,
                        found[0],
                        Provenance.RESOLVED,
                        0.95,
                        "php.function",
                    )
                if found:
                    return self._ambiguous(found)
            candidates = [str(candidate) for candidate in context["functions"]]
            # WHY: неполное имя функции PHP ищет сначала в namespace, потом глобально;
            # не найдена ни там, ни там — это встроенная функция или функция пакета.
            if any("\\" not in candidate for candidate in candidates):
                return _EXTERNAL
            return self._php_missing(candidates[0])
        receiver = context.get("recv")
        if receiver == "type":
            return self._php_method_on(
                str(context["type"]), ref.name, "php.receiver.type"
            )
        if receiver == "prop":
            prop_type = self._php_property_type(
                str(context["type"]), str(context["prop"])
            )
            if prop_type is not None:
                return self._php_method_on(prop_type, ref.name, "php.receiver.property")
        return self._by_unique_name(ref, NodeKind.METHOD, Language.PHP)

    def _php_type(self, fqcn: str, ref: RefRow) -> Resolution:
        found = [n for n in self._lower_qn(fqcn) if n.kind in _TYPE_KINDS]
        if len(found) == 1:
            return Resolution(
                RefStatus.RESOLVED, found[0], Provenance.RESOLVED, 0.95, "php.fqcn"
            )
        if found:
            source_codebase = self._codebase_of(ref.path)
            same = [
                n
                for n in found
                if n.codebase == source_codebase and "/tests/" not in f"/{n.path}"
            ]
            if len(same) == 1:
                return Resolution(
                    RefStatus.RESOLVED,
                    same[0],
                    Provenance.RESOLVED,
                    0.9,
                    "php.fqcn.codebase",
                )
            return self._ambiguous(found)
        return self._php_missing(fqcn)

    def _php_missing(self, fqcn: str) -> Resolution:
        """Имени нет в индексе: внешний код, если namespace не проектный."""
        namespace = fqcn.split("\\", 1)[0].lower()
        return (
            _UNRESOLVED
            if "\\" in fqcn and self._index.has_php_namespace(namespace)
            else _EXTERNAL
        )

    def _php_class(self, fqcn: str) -> NodeRow | None:
        found = [n for n in self._lower_qn(fqcn) if n.kind in _TYPE_KINDS]
        return found[0] if len(found) == 1 else None

    def _php_hierarchy(self, fqcn: str) -> Iterable[tuple[str, NodeRow | None]]:
        """Тип и его предки: родители, трейты, интерфейсы; без повторов."""
        queue = [fqcn]
        seen: set[str] = set()
        while queue and len(seen) < _MAX_HIERARCHY:
            current = queue.pop(0)
            if current.lower() in seen:
                continue
            seen.add(current.lower())
            node = self._php_class(current)
            yield current, node
            if node is None:
                continue
            for key in ("traits", "extends", "implements"):
                queue.extend(str(item) for item in node.meta.get(key, []))

    def _php_method_on(self, fqcn: str, method: str, via: str) -> Resolution:
        external_ancestor = False
        for current, node in self._php_hierarchy(fqcn):
            if node is None:
                external_ancestor = True
                continue
            members = [
                n
                for n in self._file_nodes(node.path)
                if n.kind == "method"
                and n.qualified_name.lower() == f"{current}::{method}".lower()
            ]
            if members:
                confidence = 0.95 if current.lower() == fqcn.lower() else 0.9
                return Resolution(
                    RefStatus.RESOLVED, members[0], Provenance.RESOLVED, confidence, via
                )
        return _EXTERNAL if external_ancestor else _UNRESOLVED

    def _php_property_type(self, fqcn: str, prop: str) -> str | None:
        for _, node in self._php_hierarchy(fqcn):
            if node is None:
                continue
            props = node.meta.get("props", {})
            if prop in props:
                return str(props[prop])
        return None
