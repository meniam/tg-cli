"""Правила разрешения python; работают на снимке графа."""

from typing import TYPE_CHECKING

from atlas.bindings import read_binding
from atlas.model import EdgeKind, Language, NodeKind, Provenance, RefStatus
from atlas.resolve.common import (
    _EXTERNAL,
    _MAX_HIERARCHY,
    _PY_TOP_KINDS,
    _UNRESOLVED,
    BaseResolver,
    Resolution,
)
from atlas.resolve.modules import EXTERNAL

if TYPE_CHECKING:
    from atlas.bindings import Binding
    from atlas.records import NodeRow, RefRow
    from atlas.resolve.index import NodeIndex
    from atlas.resolve.python_modules import PythonModules


class PythonResolver(BaseResolver):
    """Разрешение ссылок языка python."""

    def __init__(self, index: NodeIndex, modules: PythonModules) -> None:
        super().__init__(index)
        self._python_modules = modules

    def resolve(self, ref: RefRow) -> Resolution:
        """Разрешить ссылку по правилам языка."""
        context = ref.context
        if ref.kind == EdgeKind.IMPORTS:
            return self._py_import(context, ref.path)
        receiver = context.get("recv")
        if receiver is None:
            return self._py_binding(context, ref)
        type_binding = context.get("type")
        if isinstance(type_binding, dict):
            if receiver == "prop":
                owner = self._py_node(type_binding, ref.path, set())
                props = owner.meta.get("props", {}) if owner is not None else {}
                prop_binding = read_binding(props.get(str(context["prop"])))
                if owner is None or prop_binding is None:
                    return self._by_unique_name(ref, NodeKind.METHOD, Language.PYTHON)
                return self._py_method(
                    prop_binding, owner.path, ref.name, "python.receiver.property"
                )
            return self._py_method(
                type_binding, ref.path, ref.name, f"python.receiver.{receiver}"
            )
        return self._by_unique_name(ref, NodeKind.METHOD, Language.PYTHON)

    def _py_import(self, context: Binding, path: str) -> Resolution:
        module = str(context["module"])
        target = self._python_modules.resolve(module, path)
        # WHY: у каталога без ``__init__.py`` нет файла; ``from pkg import mod`` тогда ведёт
        # на первый импортированный подмодуль, а ``from . import x`` — на сам пакет.
        for name in context.get("names", []) if target is None else []:
            target = self._python_modules.resolve(
                f"{module}{'' if module.endswith('.') else '.'}{name}", path
            )
            if target is not None:
                break
        if target is None and "package" in context:
            target = self._python_modules.resolve(str(context["package"]), path)
        if target is None:
            return _UNRESOLVED
        if target == EXTERNAL:
            return _EXTERNAL
        file_node = self._file_node(target)
        if file_node is None:
            return _UNRESOLVED
        return Resolution(
            RefStatus.RESOLVED, file_node, Provenance.RESOLVED, 1.0, "python.module"
        )

    def _py_binding(self, binding: Binding, ref: RefRow) -> Resolution:
        if "from" in binding:
            node = self._py_node(binding, ref.path, set())
            if node is not None:
                return Resolution(
                    RefStatus.RESOLVED, node, Provenance.RESOLVED, 0.95, "python.import"
                )
            return _EXTERNAL if self._py_external(binding, ref.path) else _UNRESOLVED
        if binding.get("local"):
            node = self._py_node(binding, ref.path, set())
            if node is not None:
                return Resolution(
                    RefStatus.RESOLVED, node, Provenance.SYNTAX, 1.0, "python.local"
                )
            return _UNRESOLVED
        # WHY: имя без привязки в Python — встроенное или пришедшее через ``import *``;
        # угадывание по имени здесь дало бы ложные рёбра на одноимённые функции проекта.
        file_node = self._file_node(ref.path)
        parts = str(binding.get("name", ref.name)).split(".")
        for module in (
            file_node.meta.get("star_imports", []) if file_node is not None else []
        ):
            target = self._python_modules.resolve(str(module), ref.path)
            if target is None or target == EXTERNAL:
                continue
            node = self._py_members(
                self._py_export(target, parts[0], set()), parts[1:], set()
            )
            if node is not None:
                return Resolution(
                    RefStatus.RESOLVED,
                    node,
                    Provenance.RESOLVED,
                    0.9,
                    "python.star-import",
                )
        return _EXTERNAL

    def _py_external(self, binding: Binding, path: str) -> bool:
        return self._python_modules.resolve(str(binding["from"]), path) == EXTERNAL

    def _py_node(
        self, binding: Binding, path: str, seen: set[tuple[str, str]]
    ) -> NodeRow | None:
        """Узел, на который указывает привязка имени ``a.b.c`` в файле ``path``."""
        parts = str(binding.get("name", "")).split(".")
        if "from" in binding:
            imported = str(binding.get("imported", "*"))
            members = parts[1:] if imported == "*" else [imported, *parts[1:]]
            return self._py_path(str(binding["from"]), members, path, seen)
        if binding.get("local"):
            return self._py_members(self._py_top_level(path, parts[0]), parts[1:], seen)
        return None

    def _py_path(
        self, module: str, members: list[str], path: str, seen: set[tuple[str, str]]
    ) -> NodeRow | None:
        """Самый длинный префикс ``module.members``, который оказался модулем, и члены дальше."""
        for split in range(len(members), -1, -1):
            head = members[:split]
            name = (
                module
                if not head
                else f"{module}{'' if module.endswith('.') else '.'}{'.'.join(head)}"
            )
            target = self._python_modules.resolve(name, path)
            if target is None or target == EXTERNAL:
                continue
            return self._py_members(self._file_node(target), members[split:], seen)
        return None

    def _py_members(
        self, node: NodeRow | None, members: list[str], seen: set[tuple[str, str]]
    ) -> NodeRow | None:
        for member in members:
            if node is None:
                return None
            node = (
                self._py_export(node.path, member, seen)
                if node.kind == "file"
                else self._member(node, member)
            )
        return node

    def _py_export(
        self, path: str, name: str, seen: set[tuple[str, str]]
    ) -> NodeRow | None:
        """Имя модуля: объявление, реэкспорт импортом, подмодуль пакета или ``import *``."""
        if (path, name) in seen or len(seen) > 20:
            return None
        seen.add((path, name))
        node = self._py_top_level(path, name)
        if node is not None:
            return node
        file_node = self._file_node(path)
        if file_node is None:
            return None
        entry = file_node.meta.get("imports", {}).get(name)
        if isinstance(entry, dict):
            imported = str(entry.get("imported", "*"))
            return self._py_path(
                str(entry["from"]), [] if imported == "*" else [imported], path, seen
            )
        submodule = self._python_modules.submodule(path, name)
        if submodule is not None:
            return self._file_node(submodule)
        for module in file_node.meta.get("star_imports", []):
            target = self._python_modules.resolve(str(module), path)
            if target is None or target == EXTERNAL:
                continue
            node = self._py_export(target, name, seen)
            if node is not None:
                return node
        return None

    def _py_top_level(self, path: str, name: str) -> NodeRow | None:
        found = [
            n
            for n in self._file_nodes(path)
            if n.qualified_name == name and n.kind in _PY_TOP_KINDS
        ]
        return found[0] if found else None

    def _py_method(
        self, type_binding: Binding, path: str, method: str, via: str
    ) -> Resolution:
        """Найти метод в порядке C3 MRO; неполная иерархия остаётся нерешённой."""
        owner = self._py_node(type_binding, path, set())
        if owner is None:
            if "from" in type_binding:
                return (
                    _EXTERNAL if self._py_external(type_binding, path) else _UNRESOLVED
                )
            return _UNRESOLVED
        direct = self._member(owner, method)
        if direct is not None and direct.kind == "method":
            return Resolution(
                RefStatus.RESOLVED, direct, Provenance.RESOLVED, 0.85, via
            )
        order = self._mro(owner, set())
        if order is None:
            return _UNRESOLVED
        for parent in order[1:]:
            member = self._member(parent, method)
            if member is not None and member.kind == "method":
                return Resolution(
                    RefStatus.RESOLVED, member, Provenance.RESOLVED, 0.85, via
                )
        return _UNRESOLVED

    def _mro(self, owner: NodeRow, active: set[int]) -> list[NodeRow] | None:
        """Линеаризовать известную иерархию; цикл или неизвестный предок дают None."""
        if owner.id in active or len(active) >= _MAX_HIERARCHY:
            return None
        active = active | {owner.id}
        parents: list[NodeRow] = []
        sequences: list[list[NodeRow]] = []
        for binding in owner.meta.get("extends", []):
            checked = read_binding(binding)
            if checked is None:
                return None
            parent = self._py_node(checked, owner.path, set())
            if parent is None:
                return None
            parents.append(parent)
            order = self._mro(parent, active)
            if order is None:
                return None
            sequences.append(order)
        sequences.append(parents.copy())
        result = [owner]
        # WHY: обход в ширину не сохраняет порядок Python. C3 выбирает голову,
        # которая не встречается в хвосте ни одной оставшейся последовательности.
        while any(sequences):
            sequences = [sequence for sequence in sequences if sequence]
            tail_ids = {node.id for sequence in sequences for node in sequence[1:]}
            candidate = next(
                (
                    sequence[0]
                    for sequence in sequences
                    if sequence[0].id not in tail_ids
                ),
                None,
            )
            if candidate is None:
                return None
            result.append(candidate)
            for sequence in sequences:
                if sequence[0].id == candidate.id:
                    sequence.pop(0)
        return result
