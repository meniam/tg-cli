"""Правила разрешения ecmascript; работают на снимке графа."""

from typing import TYPE_CHECKING

from atlas.bindings import read_binding
from atlas.model import EdgeKind, NodeKind, Provenance, RefStatus
from atlas.resolve.common import (
    _EXTERNAL,
    _MAX_HIERARCHY,
    _TS_TOP_KINDS,
    _UNRESOLVED,
    BaseResolver,
    Resolution,
)
from atlas.resolve.modules import EXTERNAL, ModuleResolver

if TYPE_CHECKING:
    from atlas.bindings import Binding
    from atlas.records import NodeRow, RefRow
    from atlas.resolve.index import NodeIndex


class EcmaResolver(BaseResolver):
    """Разрешение ссылок языка ecmascript."""

    def __init__(self, index: NodeIndex, modules: ModuleResolver) -> None:
        super().__init__(index)
        self._modules = modules

    def resolve(self, ref: RefRow) -> Resolution:
        """Разрешить ссылку по правилам языка."""
        context = ref.context
        if ref.kind == EdgeKind.IMPORTS:
            target = self._modules.resolve(str(context["module"]), ref.path)
            if target is None:
                return _UNRESOLVED
            if target == EXTERNAL:
                return _EXTERNAL
            file_node = self._file_node(target)
            if file_node is None:
                return _UNRESOLVED
            return Resolution(
                RefStatus.RESOLVED,
                file_node,
                Provenance.RESOLVED,
                1.0,
                "ecmascript.module",
            )
        receiver = context.get("recv")
        if receiver is None:
            return self._ecma_binding(context, ref.path, ref)
        if receiver == "module":
            target = self._modules.resolve(str(context["from"]), ref.path)
            if target is None:
                return _UNRESOLVED
            if target == EXTERNAL:
                return _EXTERNAL
            node = self._export(target, ref.name, set())
            if node is None:
                return _UNRESOLVED
            return Resolution(
                RefStatus.RESOLVED,
                node,
                Provenance.RESOLVED,
                0.95,
                "ecmascript.namespace-import",
            )
        type_binding = context.get("type")
        if isinstance(type_binding, dict):
            if receiver == "prop":
                owner = self._binding_node(type_binding, ref.path)
                if owner is None:
                    return self._by_unique_name(ref, NodeKind.METHOD, None)
                prop_binding = read_binding(
                    owner.meta.get("props", {}).get(str(context["prop"]))
                )
                if prop_binding is None:
                    return self._by_unique_name(ref, NodeKind.METHOD, None)
                return self._ecma_method(
                    prop_binding, owner.path, ref.name, "ecmascript.receiver.property"
                )
            return self._ecma_method(
                type_binding, ref.path, ref.name, f"ecmascript.receiver.{receiver}"
            )
        return self._by_unique_name(ref, NodeKind.METHOD, None)

    def _ecma_binding(self, binding: Binding, path: str, ref: RefRow) -> Resolution:
        if "from" in binding:
            target = self._modules.resolve(str(binding["from"]), path)
            if target is None:
                return _UNRESOLVED
            if target == EXTERNAL:
                return _EXTERNAL
            node = self._binding_node(binding, path)
            if node is None:
                return _UNRESOLVED
            return Resolution(
                RefStatus.RESOLVED, node, Provenance.RESOLVED, 0.95, "ecmascript.import"
            )
        if binding.get("local"):
            node = self._binding_node(binding, path)
            if node is not None:
                return Resolution(
                    RefStatus.RESOLVED, node, Provenance.SYNTAX, 1.0, "ecmascript.local"
                )
            return _UNRESOLVED
        return self._by_unique_name(ref, None, None)

    def _binding_node(self, binding: Binding, path: str) -> NodeRow | None:
        """Узел, на который указывает привязка имени в файле ``path``."""
        name = str(binding.get("name", ""))
        if "from" in binding:
            target = self._modules.resolve(str(binding["from"]), path)
            if target is None or target == EXTERNAL:
                return None
            imported = str(binding.get("imported", name))
            member = binding.get("member")
            if imported == "*":
                return (
                    self._export(target, str(member), set())
                    if member
                    else self._file_node(target)
                )
            node = self._export(target, imported, set())
            if node is not None and member:
                return self._member(node, str(member))
            return node
        if binding.get("local"):
            root, _, member = name.partition(".")
            node = self._top_level(path, root)
            if node is not None and member:
                return self._member(node, member)
            return node
        return None

    def _export(
        self, path: str, name: str, seen: set[tuple[str, str]]
    ) -> NodeRow | None:
        """Узел, экспортированный из файла под именем ``name``, с проходом реэкспортов."""
        if (path, name) in seen or len(seen) > 20:
            return None
        seen.add((path, name))
        file_node = self._file_node(path)
        if file_node is None:
            return None
        exports = file_node.meta.get("exports", {})
        entry = exports.get(name)
        if isinstance(entry, dict):
            if "local" in entry:
                return self._top_level(path, str(entry["local"]))
            if "from" in entry:
                target = self._modules.resolve(str(entry["from"]), path)
                if target is None or target == EXTERNAL:
                    return None
                if entry.get("name") == "*":
                    return self._file_node(target)
                return self._export(target, str(entry["name"]), seen)
        if name == "default":
            return None
        for star in file_node.meta.get("star_exports", []):
            target = self._modules.resolve(str(star), path)
            if target is None or target == EXTERNAL:
                continue
            node = self._export(target, name, seen)
            if node is not None:
                return node
        return None

    def _ecma_method(
        self, type_binding: Binding, path: str, method: str, via: str
    ) -> Resolution:
        owner = self._binding_node(type_binding, path)
        if owner is None:
            return _EXTERNAL if "from" in type_binding else _UNRESOLVED
        for _ in range(_MAX_HIERARCHY):
            member = self._member(owner, method)
            if member is not None:
                return Resolution(
                    RefStatus.RESOLVED, member, Provenance.RESOLVED, 0.85, via
                )
            parent = owner.meta.get("extends")
            parents = (
                parent
                if isinstance(parent, list)
                else [parent]
                if isinstance(parent, dict)
                else []
            )
            next_owner = None
            for candidate in parents:
                checked = read_binding(candidate)
                next_owner = (
                    self._binding_node(checked, owner.path)
                    if checked is not None
                    else None
                )
                if next_owner is not None:
                    break
            if next_owner is None:
                return _EXTERNAL if parents else _UNRESOLVED
            owner = next_owner
        return _UNRESOLVED

    def _top_level(self, path: str, name: str) -> NodeRow | None:
        found = [
            n
            for n in self._file_nodes(path)
            if n.qualified_name == name and n.kind in _TS_TOP_KINDS
        ]
        return found[0] if found else None
