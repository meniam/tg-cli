"""Правила разрешения Go; работают на снимке графа.

Объявление может лежать в любом файле пакета, поэтому имя ищется по каталогу, а не по файлу.
Метод ищется на типе получателя и на встроенных в него типах по глубине: как в языке, где
побеждает ближайшее объявление.
"""

import posixpath
from typing import TYPE_CHECKING

from atlas.bindings import read_binding
from atlas.model import EdgeKind, Language, NodeKind, Provenance, RefStatus
from atlas.resolve.common import (
    _EXTERNAL,
    _MAX_HIERARCHY,
    _UNRESOLVED,
    BaseResolver,
    Resolution,
)
from atlas.resolve.modules import EXTERNAL

if TYPE_CHECKING:
    from atlas.bindings import Binding
    from atlas.records import NodeRow, RefRow
    from atlas.resolve.go_modules import GoModules
    from atlas.resolve.index import NodeIndex

_TOP_KINDS = frozenset({"function", "class", "interface", "type", "constant"})
_TYPE_KINDS = frozenset({"class", "interface", "type"})
_MAX_ALIAS_DEPTH = 8


class GoResolver(BaseResolver):
    """Разрешение ссылок языка Go."""

    def __init__(self, index: NodeIndex, modules: GoModules) -> None:
        super().__init__(index)
        self._modules = modules

    def resolve(self, ref: RefRow) -> Resolution:
        """Разрешить ссылку по правилам языка."""
        context = ref.context
        if ref.kind == EdgeKind.IMPORTS:
            return self._import(str(context["module"]))
        receiver = context.get("recv")
        if receiver is None:
            return self._binding(context, ref)
        type_binding = context.get("type")
        if not isinstance(type_binding, dict):
            return self._by_unique_name(ref, NodeKind.METHOD, Language.GO)
        if receiver == "prop":
            owner = self._node(type_binding, ref.path, 0)
            found = self._property(owner, str(context["prop"])) if owner else None
            if found is None:
                return self._by_unique_name(ref, NodeKind.METHOD, Language.GO)
            prop_binding, declared_in = found
            return self._method(
                prop_binding, declared_in, ref.name, "go.receiver.property"
            )
        return self._method(type_binding, ref.path, ref.name, f"go.receiver.{receiver}")

    def _import(self, module: str) -> Resolution:
        directory = self._modules.resolve(module)
        if directory == EXTERNAL:
            return _EXTERNAL
        if directory is None:
            return _UNRESOLVED
        entry = self._modules.entry(directory)
        file_node = self._file_node(entry) if entry is not None else None
        if file_node is None:
            return _UNRESOLVED
        return Resolution(
            RefStatus.RESOLVED, file_node, Provenance.RESOLVED, 1.0, "go.package"
        )

    def _binding(self, binding: Binding, ref: RefRow) -> Resolution:
        if "from" in binding:
            node = self._node(binding, ref.path, 0)
            if node is not None:
                return self._found(ref, node, Provenance.RESOLVED, 0.95, "go.import")
            return _EXTERNAL if self._external(binding) else _UNRESOLVED
        if binding.get("local"):
            node = self._node(binding, ref.path, 0)
            if node is not None:
                return self._found(
                    ref, node, Provenance.SYNTAX, 1.0, "go.package-scope"
                )
            # WHY: имя, которого нет в пакете, могло прийти из ``import . "pkg"`` внешнего пакета.
            if self._has_external_dot_import(ref.path):
                return _EXTERNAL
        return _UNRESOLVED

    def _found(
        self,
        ref: RefRow,
        node: NodeRow,
        provenance: Provenance,
        confidence: float,
        via: str,
    ) -> Resolution:
        """Найденное объявление: при вариантах сборки ``ambiguous``, вызов типа это ссылка."""
        variants = self._variants(node)
        if variants:
            return self._ambiguous([node, *variants])
        # ``ID(s)`` выглядит как вызов, а по смыслу это преобразование к типу.
        kind = (
            EdgeKind.REFERENCES
            if ref.kind == EdgeKind.CALLS and node.kind in _TYPE_KINDS
            else None
        )
        return Resolution(
            RefStatus.RESOLVED, node, provenance, confidence, via, kind=kind
        )

    def _external(self, binding: Binding) -> bool:
        return self._modules.resolve(str(binding["from"])) == EXTERNAL

    def _is_external(self, binding: Binding, path: str, depth: int) -> bool:
        """Тип привязки лежит вне проекта, в том числе как тип результата функции проекта."""
        if depth > _MAX_ALIAS_DEPTH:
            return False
        if "from" in binding and self._external(binding):
            return True
        if not binding.get("ret"):
            return False
        function = self._declaration(binding, path)
        returns = (
            read_binding(function.meta.get("returns"))
            if function is not None and function.kind == "function"
            else None
        )
        return (
            function is not None
            and returns is not None
            and self._is_external(returns, function.path, depth + 1)
        )

    # --- имена пакета -------------------------------------------------------------------

    def _node(self, binding: Binding, path: str, depth: int) -> NodeRow | None:
        """Узел, на который указывает привязка; псевдоним типа и результат вызова раскрыты."""
        if depth > _MAX_ALIAS_DEPTH:
            return None
        node = self._declaration(binding, path)
        if node is None:
            return None
        if binding.get("ret") and node.kind == "function":
            returns = read_binding(node.meta.get("returns"))
            return self._node(returns, node.path, depth + 1) if returns else None
        if binding.get("ret") and node.kind not in _TYPE_KINDS:
            # Вызов переменной функционального типа: тип результата неизвестен.
            return None
        if node.kind == "type":
            alias = read_binding(node.meta.get("alias"))
            if alias is not None:
                return self._node(alias, node.path, depth + 1)
        return node

    def _declaration(self, binding: Binding, path: str) -> NodeRow | None:
        """Объявление, названное привязкой: функция, тип или значение пакета."""
        if "from" in binding:
            directory = self._modules.resolve(str(binding["from"]))
            if directory is None or directory == EXTERNAL:
                return None
            name = str(binding.get("imported", ""))
            return (
                self._top_level(directory, name, None) if name[:1].isupper() else None
            )
        if binding.get("local"):
            name = str(binding.get("name", ""))
            return self._top_level(
                posixpath.dirname(path), name, self._package_of(path)
            ) or self._dot_imported(name, path)
        return None

    def _top_level(
        self, directory: str, name: str, package: str | None
    ) -> NodeRow | None:
        """Объявление пакета: из тестового файла свой пакет, из чужого каталога не тесты."""
        for path in self._modules.files_in(directory):
            nodes = self._file_nodes(path)
            if package is None:
                if path.endswith("_test.go"):
                    continue
            elif _package_name(nodes) != package:
                continue
            for node in nodes:
                if node.qualified_name == name and node.kind in _TOP_KINDS:
                    return node
        return None

    def _dot_imported(self, name: str, path: str) -> NodeRow | None:
        """Имя из ``import . "pkg"``: виден без квалификатора."""
        file_node = self._file_node(path)
        if file_node is None or not name[:1].isupper():
            return None
        for module in file_node.meta.get("dot_imports", []):
            directory = self._modules.resolve(str(module))
            if directory is None or directory == EXTERNAL:
                continue
            node = self._top_level(directory, name, None)
            if node is not None:
                return node
        return None

    def _has_external_dot_import(self, path: str) -> bool:
        file_node = self._file_node(path)
        if file_node is None:
            return False
        return any(
            self._modules.resolve(str(module)) == EXTERNAL
            for module in file_node.meta.get("dot_imports", [])
        )

    def _package_of(self, path: str) -> str | None:
        return _package_name(self._file_nodes(path))

    def _variants(self, node: NodeRow) -> list[NodeRow]:
        """Те же объявления в других файлах пакета.

        Компилятор не допускает двух объявлений одного имени в пакете, поэтому такие
        файлы различаются тегами сборки или суффиксом платформы (``open_linux.go`` и
        ``open_windows.go``). Какой из них попадёт в сборку, граф не знает.

        Returns:
            Остальные объявления с тем же именем; пусто, если оно одно.
        """
        if node.kind not in _TOP_KINDS | {"method", "property"}:
            return []
        package = self._package_of(node.path)
        found: list[NodeRow] = []
        for path in self._modules.files_in(posixpath.dirname(node.path)):
            if path == node.path:
                continue
            nodes = self._file_nodes(path)
            if _package_name(nodes) != package:
                continue
            found.extend(
                n
                for n in nodes
                if n.qualified_name == node.qualified_name and n.kind == node.kind
            )
        return found

    # --- методы и поля ------------------------------------------------------------------

    def _method(
        self, type_binding: Binding, path: str, method: str, via: str
    ) -> Resolution:
        """Найти метод на типе и встроенных типах; неполная иерархия остаётся открытой."""
        owner = self._node(type_binding, path, 0)
        if owner is None:
            return (
                _EXTERNAL if self._is_external(type_binding, path, 0) else _UNRESOLVED
            )
        chain, open_ends = self._hierarchy(owner)
        for node in chain:
            # Поле функционального типа вызывается так же, как метод: ``s.clock()``.
            member = self._member(node, method)
            if member is not None:
                variants = self._variants(member)
                if variants:
                    return self._ambiguous([member, *variants])
                return Resolution(
                    RefStatus.RESOLVED, member, Provenance.RESOLVED, 0.85, via
                )
        if open_ends == "external":
            return _EXTERNAL
        return _UNRESOLVED

    def _property(self, owner: NodeRow, name: str) -> tuple[Binding, str] | None:
        """Тип поля ``name`` и файл, где он записан, с учётом встроенных типов."""
        for node in self._hierarchy(owner)[0]:
            binding = read_binding(node.meta.get("props", {}).get(name))
            if binding is not None:
                return binding, node.path
        return None

    def _hierarchy(self, owner: NodeRow) -> tuple[list[NodeRow], str]:
        """Тип и встроенные в него типы по глубине вложения.

        Returns:
            Типы по глубине и пометка полноты иерархии: ``external``, если часть встроенных
            типов вне проекта и метод мог прийти оттуда; ``unknown``, если встроенный тип не
            найден; пусто, если иерархия полна.
        """
        chain: list[NodeRow] = []
        seen: set[int] = set()
        level = [owner]
        open_ends = ""
        while level and len(chain) < _MAX_HIERARCHY:
            following: list[NodeRow] = []
            for node in level:
                if node.id in seen:
                    continue
                seen.add(node.id)
                chain.append(node)
                for raw in node.meta.get("extends", []):
                    binding = read_binding(raw)
                    parent = (
                        self._node(binding, node.path, 0)
                        if binding is not None
                        else None
                    )
                    if parent is not None:
                        following.append(parent)
                    elif binding is not None and "from" in binding:
                        kind = "external" if self._external(binding) else "unknown"
                        open_ends = open_ends if open_ends == "external" else kind
                    else:
                        open_ends = open_ends or "unknown"
            level = following
        return chain, open_ends

    def _member(self, owner: NodeRow, member: str) -> NodeRow | None:
        """Метод или поле типа: объявлено в любом файле пакета типа."""
        qualified = f"{owner.qualified_name}.{member}"
        package = self._package_of(owner.path)
        for path in self._modules.files_in(posixpath.dirname(owner.path)):
            nodes = self._file_nodes(path)
            if _package_name(nodes) != package:
                continue
            for node in nodes:
                if node.qualified_name == qualified and node.kind in {
                    "method",
                    "property",
                }:
                    return node
        return None


def _package_name(nodes: list[NodeRow]) -> str | None:
    file_node = next((n for n in nodes if n.kind == "file"), None)
    name = file_node.meta.get("package") if file_node is not None else None
    return str(name) if name else None
