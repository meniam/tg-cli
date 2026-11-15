"""Извлечение узлов и ссылок из PHP.

Имена классов разрешаются здесь же, по правилам PHP: полное имя, ``use``, текущий
namespace. Разрешателю остаётся найти узел по полному имени. Тип получателя вызова
определяется из типов свойств и параметров и из присваиваний ``new``.
"""

from dataclasses import replace
from pathlib import PurePosixPath
from typing import TYPE_CHECKING, Any

from atlas.model import (
    FLAG_ABSTRACT,
    FLAG_STATIC,
    Edge,
    EdgeKind,
    FileExtract,
    Language,
    Node,
    NodeKind,
    Provenance,
    Ref,
    node_id,
)
from atlas.parse.common import (
    col,
    docblock,
    end_line,
    line,
    signature,
    text,
    unquote,
    walk,
)
from atlas.parse.grammars import parser_for

if TYPE_CHECKING:
    from collections.abc import Iterable

    from tree_sitter import Node as TsNode

    from atlas.bindings import Binding

_TYPE_DECLARATIONS = {
    "class_declaration": NodeKind.CLASS,
    "interface_declaration": NodeKind.INTERFACE,
    "trait_declaration": NodeKind.TRAIT,
    "enum_declaration": NodeKind.ENUM,
}
_NAME_TYPES = frozenset({"name", "qualified_name"})
_NESTED_SCOPES = frozenset(
    {
        "class_declaration",
        "interface_declaration",
        "trait_declaration",
        "enum_declaration",
        "function_definition",
        "method_declaration",
    }
)
_PSEUDO_TYPES = frozenset(
    {
        "self",
        "static",
        "parent",
        "mixed",
        "iterable",
        "object",
        "callable",
        "void",
        "never",
        "null",
        "false",
        "true",
        "array",
        "bool",
        "int",
        "float",
        "string",
    }
)
_ATTRIBUTE_TYPES = frozenset({"attribute_list"})


def extract(path: str, source: bytes) -> FileExtract:
    """Разобрать PHP-файл в узлы, синтаксические рёбра и ссылки."""
    tree = parser_for(Language.PHP).parse(source)
    extractor = _PhpExtractor(path)
    extractor.run(tree.root_node)
    errors = ("syntax errors in file",) if tree.root_node.has_error else ()
    nodes = extractor.nodes
    if extractor.all_uses and nodes and nodes[0].kind is NodeKind.FILE:
        nodes[0] = replace(
            nodes[0], meta={**nodes[0].meta, "uses": sorted(extractor.all_uses)}
        )
    return FileExtract(
        path=path,
        language=Language.PHP,
        nodes=tuple(nodes),
        refs=tuple(extractor.refs),
        edges=tuple(extractor.edges),
        errors=errors,
    )


class _Scope:
    """Контекст функции: класс, его родитель и известные типы переменных."""

    def __init__(self, class_fqcn: str | None, parent_fqcn: str | None) -> None:
        self.class_fqcn = class_fqcn
        self.parent_fqcn = parent_fqcn
        self.variables: dict[str, str | None] = {}

    def assign(self, variable: str, fqcn: str | None) -> None:
        # WHY: анализ не учитывает поток управления; переменная с двумя разными типами
        # в одной функции честно считается неизвестной, а не последней присвоенной.
        if variable in self.variables and self.variables[variable] != fqcn:
            self.variables[variable] = None
            return
        self.variables[variable] = fqcn


class _PhpExtractor:
    def __init__(self, path: str) -> None:
        self.path = path
        self.namespace = ""
        self.class_uses: dict[str, str] = {}
        self.function_uses: dict[str, str] = {}
        self.all_uses: set[str] = set()
        self.nodes: list[Node] = []
        self.refs: list[Ref] = []
        self.edges: list[Edge] = []
        self._ids: set[str] = set()
        self.file_id = node_id(NodeKind.FILE, path, path)

    # --- обход верхнего уровня ----------------------------------------------------------

    def run(self, root: TsNode) -> None:
        self._add_node(
            Node(
                id=self.file_id,
                kind=NodeKind.FILE,
                name=PurePosixPath(self.path).name,
                qualified_name=self.path,
                path=self.path,
                language=Language.PHP,
                start_line=1,
                end_line=end_line(root),
            )
        )
        self._statements(root.children)

    def _statements(self, statements: Iterable[TsNode]) -> None:
        loose: list[TsNode] = []
        for statement in statements:
            match statement.type:
                case "namespace_definition":
                    self._namespace(statement)
                case "namespace_use_declaration":
                    self._use(statement)
                case kind if kind in _TYPE_DECLARATIONS:
                    self._type(statement)
                case "function_definition":
                    self._function(
                        statement, class_fqcn=None, parent_fqcn=None, container=None
                    )
                case _:
                    loose.append(statement)
        scope = _Scope(None, None)
        for statement in loose:
            self._scan_body(statement, self.file_id, scope)

    def _namespace(self, statement: TsNode) -> None:
        self.namespace = text(statement.child_by_field_name("name")).lstrip("\\")
        self.class_uses = {}
        self.function_uses = {}
        body = statement.child_by_field_name("body")
        if body is not None:
            self._statements(body.children)

    def _use(self, statement: TsNode) -> None:
        declared = _use_type(statement)
        prefix = ""
        for child in statement.named_children:
            if child.type == "namespace_name":
                prefix = text(child).strip("\\") + "\\"
            elif child.type == "namespace_use_clause":
                self._use_clause(child, "", declared)
            elif child.type == "namespace_use_group":
                for clause in child.named_children:
                    if clause.type == "namespace_use_clause":
                        self._use_clause(clause, prefix, declared)

    def _use_clause(self, clause: TsNode, prefix: str, declared: str | None) -> None:
        kind = _use_type(clause) or declared
        if kind == "const":
            return
        target = self.function_uses if kind == "function" else self.class_uses
        names = [child for child in clause.named_children if child.type in _NAME_TYPES]
        if not names:
            return
        full = prefix + text(names[0]).strip("\\")
        alias_node = clause.child_by_field_name("alias")
        alias = text(alias_node) if alias_node is not None else full.rsplit("\\", 1)[-1]
        target[alias.lower()] = full
        self.all_uses.add(full)

    # --- имена --------------------------------------------------------------------------

    def resolve_class(self, raw: str, scope: _Scope | None = None) -> str | None:
        """Полное имя класса по правилам PHP; ``None`` для псевдотипов без класса."""
        name = raw.strip()
        lower = name.lower()
        if lower in {"self", "static"}:
            return scope.class_fqcn if scope is not None else None
        if lower == "parent":
            return scope.parent_fqcn if scope is not None else None
        if lower in _PSEUDO_TYPES or not name:
            return None
        if name.startswith("\\"):
            return name[1:]
        if lower.startswith("namespace\\"):
            return self._qualify(name[len("namespace\\") :])
        first, _, rest = name.partition("\\")
        imported = self.class_uses.get(first.lower())
        if imported is not None:
            return f"{imported}\\{rest}" if rest else imported
        return self._qualify(name)

    def _qualify(self, name: str) -> str:
        return f"{self.namespace}\\{name}" if self.namespace else name

    def function_candidates(self, raw: str) -> list[str]:
        """Полные имена функции в порядке, в каком их ищет PHP."""
        name = raw.strip()
        if name.startswith("\\"):
            return [name[1:]]
        imported = self.function_uses.get(name.lower())
        if imported is not None:
            return [imported]
        if "\\" in name:
            resolved = self.resolve_class(name)
            return [resolved] if resolved else []
        candidates = [self._qualify(name)]
        if self.namespace:
            candidates.append(name)
        return candidates

    # --- объявления ---------------------------------------------------------------------

    def _type(self, declaration: TsNode) -> None:
        kind = _TYPE_DECLARATIONS[declaration.type]
        name = text(declaration.child_by_field_name("name"))
        if not name:
            return
        fqcn = self._qualify(name)
        body = declaration.child_by_field_name("body")
        extends: list[str] = []
        implements: list[str] = []
        for child in declaration.named_children:
            if child.type == "base_clause":
                extends.extend(self._class_names(child, None))
            elif child.type == "class_interface_clause":
                implements.extend(self._class_names(child, None))
        parent = extends[0] if kind is NodeKind.CLASS and extends else None
        scope = _Scope(fqcn, parent)
        type_id = node_id(kind, self.path, fqcn)
        flags = (
            FLAG_ABSTRACT
            if any(c.type == "abstract_modifier" for c in declaration.children)
            else 0
        )

        traits: list[str] = []
        properties: dict[str, str] = {}
        members: list[TsNode] = list(body.named_children) if body is not None else []
        for member in members:
            if member.type == "use_declaration":
                traits.extend(self._class_names(member, scope))
            elif member.type == "property_declaration":
                properties.update(self._property_types(member, scope))
            elif (
                member.type == "method_declaration"
                and text(member.child_by_field_name("name")).lower() == "__construct"
            ):
                properties.update(self._promoted_types(member, scope))

        meta: dict[str, Any] = {}
        if extends:
            meta["extends"] = extends
        if implements:
            meta["implements"] = implements
        if traits:
            meta["traits"] = traits
        if properties:
            meta["props"] = properties
        self._add_node(
            Node(
                id=type_id,
                kind=kind,
                name=name,
                qualified_name=fqcn,
                path=self.path,
                language=Language.PHP,
                start_line=line(declaration),
                end_line=end_line(declaration),
                signature=signature(declaration, body),
                docblock=docblock(declaration, _ATTRIBUTE_TYPES),
                attributes=self._attributes(declaration, type_id, scope),
                flags=flags,
                meta=meta,
            )
        )
        self._contains(self.file_id, type_id, declaration)
        for target in extends:
            self._ref(type_id, target, EdgeKind.EXTENDS, declaration, {"fqcn": target})
        for target in implements:
            self._ref(
                type_id, target, EdgeKind.IMPLEMENTS, declaration, {"fqcn": target}
            )
        for target in traits:
            self._ref(
                type_id, target, EdgeKind.USES_TRAIT, declaration, {"fqcn": target}
            )

        for member in members:
            match member.type:
                case "method_declaration":
                    self._function(
                        member, class_fqcn=fqcn, parent_fqcn=parent, container=type_id
                    )
                case "property_declaration":
                    self._properties(member, fqcn, type_id, scope)
                case "const_declaration":
                    self._constants(member, fqcn, type_id)
                case "enum_case":
                    self._constant(member, fqcn, type_id)
                case "method_declaration" | "use_declaration":
                    pass
                case _:
                    self._scan_body(member, type_id, scope)

    def _function(
        self,
        declaration: TsNode,
        *,
        class_fqcn: str | None,
        parent_fqcn: str | None,
        container: str | None,
    ) -> None:
        name = text(declaration.child_by_field_name("name"))
        if not name:
            return
        is_method = class_fqcn is not None
        qualified = f"{class_fqcn}::{name}" if is_method else self._qualify(name)
        kind = NodeKind.METHOD if is_method else NodeKind.FUNCTION
        function_id = self._unique_id(node_id(kind, self.path, qualified), declaration)
        body = declaration.child_by_field_name("body")
        scope = _Scope(class_fqcn, parent_fqcn)
        flags = 0
        for child in declaration.children:
            if child.type == "static_modifier":
                flags |= FLAG_STATIC
            elif child.type == "abstract_modifier":
                flags |= FLAG_ABSTRACT

        meta: dict[str, Any] = {}
        return_type = declaration.child_by_field_name("return_type")
        if return_type is not None:
            returns = self._single_type(return_type, scope)
            if returns is not None:
                meta["returns"] = returns
        self._add_node(
            Node(
                id=function_id,
                kind=kind,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.PHP,
                start_line=line(declaration),
                end_line=end_line(declaration),
                signature=signature(declaration, body),
                docblock=docblock(declaration, _ATTRIBUTE_TYPES),
                attributes=self._attributes(declaration, function_id, scope),
                flags=flags,
                meta=meta,
            )
        )
        self._contains(container or self.file_id, function_id, declaration)

        parameters = declaration.child_by_field_name("parameters")
        if parameters is not None:
            for parameter in parameters.named_children:
                if (
                    parameter.type == "property_promotion_parameter"
                    and class_fqcn
                    and container
                ):
                    self._promoted_property(parameter, class_fqcn, container)
                variable = text(parameter.child_by_field_name("name")).lstrip("$")
                type_node = parameter.child_by_field_name("type")
                fqcn = (
                    self._single_type(type_node, scope)
                    if type_node is not None
                    else None
                )
                if variable:
                    scope.assign(variable, fqcn)
                self._attributes(parameter, function_id, scope)
                self._type_refs(type_node, function_id, scope)
        self._type_refs(return_type, function_id, scope)
        if body is not None:
            self._collect_assignments(body, scope)
            self._scan_body(body, function_id, scope)

    def _properties(
        self, declaration: TsNode, class_fqcn: str, container: str, scope: _Scope
    ) -> None:
        type_node = declaration.child_by_field_name("type")
        for element in declaration.named_children:
            if element.type != "property_element":
                continue
            name = text(element.child_by_field_name("name")).lstrip("$")
            if not name:
                continue
            qualified = f"{class_fqcn}::${name}"
            property_id = self._unique_id(
                node_id(NodeKind.PROPERTY, self.path, qualified), element
            )
            self._add_node(
                Node(
                    id=property_id,
                    kind=NodeKind.PROPERTY,
                    name=name,
                    qualified_name=qualified,
                    path=self.path,
                    language=Language.PHP,
                    start_line=line(declaration),
                    end_line=end_line(declaration),
                    signature=signature(declaration, None),
                    attributes=self._attributes(declaration, property_id, scope),
                )
            )
            self._contains(container, property_id, declaration)
            self._type_refs(type_node, property_id, scope)
            default = element.child_by_field_name("default_value")
            if default is not None:
                self._scan_body(default, property_id, scope)

    def _promoted_property(
        self, parameter: TsNode, class_fqcn: str, container: str
    ) -> None:
        name = text(parameter.child_by_field_name("name")).lstrip("$")
        if not name:
            return
        qualified = f"{class_fqcn}::${name}"
        property_id = self._unique_id(
            node_id(NodeKind.PROPERTY, self.path, qualified), parameter
        )
        self._add_node(
            Node(
                id=property_id,
                kind=NodeKind.PROPERTY,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.PHP,
                start_line=line(parameter),
                end_line=end_line(parameter),
                signature=signature(parameter, None),
                meta={"promoted": True},
            )
        )
        self._contains(container, property_id, parameter)

    def _constants(self, declaration: TsNode, class_fqcn: str, container: str) -> None:
        for element in declaration.named_children:
            if element.type == "const_element":
                self._constant(element, class_fqcn, container)

    def _constant(self, element: TsNode, class_fqcn: str, container: str) -> None:
        name_node = element.child_by_field_name("name") or next(
            (child for child in element.named_children if child.type == "name"), None
        )
        name = text(name_node)
        if not name:
            return
        qualified = f"{class_fqcn}::{name}"
        constant_id = self._unique_id(
            node_id(NodeKind.CONSTANT, self.path, qualified), element
        )
        value = next(
            (
                c
                for c in element.named_children
                if c is not name_node and c.type != "name"
            ),
            None,
        )
        meta: dict[str, Any] = {}
        # WHY: имена маршрутов в атрибутах — ``self::ROUTE``; без значения константы правило
        # фреймворка не узнает имя маршрута.
        if value is not None and value.type in {"string", "encapsed_string", "integer"}:
            meta["value"] = (
                _int(text(value)) if value.type == "integer" else unquote(text(value))
            )
        self._add_node(
            Node(
                id=constant_id,
                kind=NodeKind.CONSTANT,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.PHP,
                start_line=line(element),
                end_line=end_line(element),
                signature=signature(element, None),
                meta=meta,
            )
        )
        self._contains(container, constant_id, element)

    # --- типы ---------------------------------------------------------------------------

    def _class_names(self, clause: TsNode, scope: _Scope | None) -> list[str]:
        return [
            resolved
            for child in clause.named_children
            if child.type in _NAME_TYPES
            and (resolved := self.resolve_class(text(child), scope))
        ]

    def _single_type(self, type_node: TsNode | None, scope: _Scope) -> str | None:
        """Класс из объявления типа, если тип однозначен: ``Foo`` или ``?Foo``."""
        if type_node is None:
            return None
        if type_node.type == "optional_type":
            inner = type_node.named_children
            return self._single_type(inner[0], scope) if inner else None
        if type_node.type == "named_type":
            names = [c for c in type_node.named_children if c.type in _NAME_TYPES]
            return self.resolve_class(text(names[0]), scope) if names else None
        return None

    def _property_types(self, declaration: TsNode, scope: _Scope) -> dict[str, str]:
        fqcn = self._single_type(declaration.child_by_field_name("type"), scope)
        if fqcn is None:
            return {}
        result: dict[str, str] = {}
        for element in declaration.named_children:
            if element.type == "property_element":
                name = text(element.child_by_field_name("name")).lstrip("$")
                if name:
                    result[name] = fqcn
        return result

    def _promoted_types(self, constructor: TsNode, scope: _Scope) -> dict[str, str]:
        parameters = constructor.child_by_field_name("parameters")
        result: dict[str, str] = {}
        if parameters is None:
            return result
        for parameter in parameters.named_children:
            if parameter.type != "property_promotion_parameter":
                continue
            name = text(parameter.child_by_field_name("name")).lstrip("$")
            fqcn = self._single_type(parameter.child_by_field_name("type"), scope)
            if name and fqcn:
                result[name] = fqcn
        return result

    def _type_refs(self, type_node: TsNode | None, source: str, scope: _Scope) -> None:
        if type_node is None:
            return
        nodes = [type_node, *walk(type_node)]
        for node in nodes:
            if node.type != "named_type":
                continue
            for child in node.named_children:
                if child.type in _NAME_TYPES and (
                    fqcn := self.resolve_class(text(child), scope)
                ):
                    self._ref(source, fqcn, EdgeKind.REFERENCES, child, {"fqcn": fqcn})

    # --- атрибуты -----------------------------------------------------------------------

    def _attributes(
        self, declaration: TsNode, owner: str, scope: _Scope
    ) -> tuple[dict[str, Any], ...]:
        result: list[dict[str, Any]] = []
        for attribute_list in declaration.children:
            if attribute_list.type != "attribute_list":
                continue
            for attribute in walk(attribute_list):
                if attribute.type != "attribute":
                    continue
                name_node = next(
                    (c for c in attribute.named_children if c.type in _NAME_TYPES), None
                )
                fqcn = (
                    self.resolve_class(text(name_node), scope)
                    if name_node is not None
                    else None
                )
                if fqcn is None or name_node is None:
                    continue
                self._ref(
                    owner,
                    fqcn,
                    EdgeKind.REFERENCES,
                    name_node,
                    {"fqcn": fqcn, "attribute": True},
                )
                arguments = attribute.child_by_field_name("parameters")
                entry: dict[str, Any] = {"name": fqcn, "line": line(attribute)}
                if arguments is not None:
                    positional, named = self._arguments(arguments, owner, scope)
                    if positional:
                        entry["args"] = positional
                    if named:
                        entry["named"] = named
                result.append(entry)
        return tuple(result)

    def _arguments(
        self, arguments: TsNode, owner: str, scope: _Scope
    ) -> tuple[list[Any], dict[str, Any]]:
        positional: list[Any] = []
        named: dict[str, Any] = {}
        for argument in arguments.named_children:
            if argument.type != "argument":
                continue
            name_node = argument.child_by_field_name("name")
            values = [
                c
                for c in argument.named_children
                if c is not name_node and c.id != getattr(name_node, "id", None)
            ]
            if not values:
                continue
            value = self._value(values[-1], owner, scope)
            if name_node is not None:
                named[text(name_node)] = value
            else:
                positional.append(value)
        return positional, named

    def _value(self, node: TsNode, owner: str, scope: _Scope) -> object:
        """Значение аргумента атрибута в виде JSON: литералы, массивы, ``X::class``, ``new``."""
        match node.type:
            case "string" | "encapsed_string":
                return unquote(text(node))
            case "integer":
                return _int(text(node))
            case "float":
                return _float(text(node))
            case "boolean":
                return text(node).lower() == "true"
            case "null":
                return None
            case "array_creation_expression":
                return self._array(node, owner, scope)
            case "class_constant_access_expression":
                parts = [
                    c
                    for c in node.named_children
                    if c.type in _NAME_TYPES or c.type == "relative_scope"
                ]
                if len(parts) == 2:
                    fqcn = self.resolve_class(text(parts[0]), scope)
                    member = text(parts[1])
                    if fqcn is not None:
                        self._ref(
                            owner, fqcn, EdgeKind.REFERENCES, parts[0], {"fqcn": fqcn}
                        )
                        return (
                            {"class": fqcn}
                            if member == "class"
                            else {"const": f"{fqcn}::{member}"}
                        )
                return {"expr": text(node)}
            case "object_creation_expression":
                name_node = next(
                    (c for c in node.named_children if c.type in _NAME_TYPES), None
                )
                fqcn = (
                    self.resolve_class(text(name_node), scope)
                    if name_node is not None
                    else None
                )
                result: dict[str, Any] = {"new": fqcn or text(name_node)}
                arguments = next(
                    (c for c in node.named_children if c.type == "arguments"), None
                )
                if arguments is not None:
                    positional, named = self._arguments(arguments, owner, scope)
                    if positional:
                        result["args"] = positional
                    if named:
                        result["named"] = named
                return result
            case _:
                return {"expr": text(node)[:200]}

    def _array(self, node: TsNode, owner: str, scope: _Scope) -> object:
        items: list[Any] = []
        mapping: dict[str, Any] = {}
        for element in node.named_children:
            if element.type != "array_element_initializer":
                continue
            parts = element.named_children
            has_key = any(child.type == "=>" for child in element.children)
            if has_key and len(parts) >= 2:
                key = self._value(parts[0], owner, scope)
                mapping[key if isinstance(key, str) else str(key)] = self._value(
                    parts[-1], owner, scope
                )
            elif parts:
                items.append(self._value(parts[-1], owner, scope))
        if mapping and not items:
            return mapping
        if mapping:
            return {"items": items, "keys": mapping}
        return items

    # --- тела функций -------------------------------------------------------------------

    def _collect_assignments(self, body: TsNode, scope: _Scope) -> None:
        for node in walk(body, _NESTED_SCOPES):
            if node.type != "assignment_expression":
                continue
            left = node.child_by_field_name("left")
            right = node.child_by_field_name("right")
            if left is None or right is None or left.type != "variable_name":
                continue
            variable = text(left).lstrip("$")
            if right.type == "object_creation_expression":
                name_node = next(
                    (c for c in right.named_children if c.type in _NAME_TYPES), None
                )
                fqcn = (
                    self.resolve_class(text(name_node), scope)
                    if name_node is not None
                    else None
                )
                scope.assign(variable, fqcn)
            else:
                scope.assign(variable, None)

    def _scan_body(self, body: TsNode, source: str, scope: _Scope) -> None:
        nodes = [body, *walk(body, _NESTED_SCOPES)]
        for node in nodes:
            match node.type:
                case "member_call_expression" | "nullsafe_member_call_expression":
                    self._member_call(node, source, scope)
                case "scoped_call_expression":
                    self._static_call(node, source, scope)
                case "function_call_expression":
                    self._function_call(node, source)
                case "object_creation_expression":
                    name_node = next(
                        (c for c in node.named_children if c.type in _NAME_TYPES), None
                    )
                    if name_node is not None and (
                        fqcn := self.resolve_class(text(name_node), scope)
                    ):
                        self._ref(
                            source,
                            fqcn,
                            EdgeKind.INSTANTIATES,
                            name_node,
                            {"fqcn": fqcn},
                        )
                case "class_constant_access_expression":
                    first = node.named_children[0] if node.named_children else None
                    if first is not None and first.type in _NAME_TYPES:
                        fqcn = self.resolve_class(text(first), scope)
                        if fqcn is not None:
                            self._ref(
                                source, fqcn, EdgeKind.REFERENCES, first, {"fqcn": fqcn}
                            )
                case "binary_expression":
                    right = node.child_by_field_name("right")
                    if (
                        any(child.type == "instanceof" for child in node.children)
                        and right is not None
                        and right.type in _NAME_TYPES
                        and (fqcn := self.resolve_class(text(right), scope))
                    ):
                        self._ref(
                            source, fqcn, EdgeKind.REFERENCES, right, {"fqcn": fqcn}
                        )
                case "catch_clause":
                    self._type_refs(node.child_by_field_name("type"), source, scope)
                case "anonymous_function" | "arrow_function":
                    parameters = node.child_by_field_name("parameters")
                    if parameters is not None:
                        for parameter in parameters.named_children:
                            self._type_refs(
                                parameter.child_by_field_name("type"), source, scope
                            )
                case _ if node.type in _TYPE_DECLARATIONS or node.type in {
                    "function_definition",
                    "method_declaration",
                }:
                    if node is not body:
                        self._statements([node])

    def _member_call(self, node: TsNode, source: str, scope: _Scope) -> None:
        name_node = node.child_by_field_name("name")
        receiver = node.child_by_field_name("object")
        if name_node is None or name_node.type != "name" or receiver is None:
            return
        method = text(name_node)
        context: Binding = {"recv": "unknown"}
        if receiver.type == "variable_name":
            variable = text(receiver).lstrip("$")
            if variable == "this" and scope.class_fqcn:
                context = {"recv": "type", "type": scope.class_fqcn, "self": True}
            elif (fqcn := scope.variables.get(variable)) is not None:
                context = {"recv": "type", "type": fqcn}
        elif receiver.type in {
            "member_access_expression",
            "nullsafe_member_access_expression",
        }:
            inner = receiver.child_by_field_name("object")
            prop = receiver.child_by_field_name("name")
            if (
                inner is not None
                and prop is not None
                and text(inner) == "$this"
                and scope.class_fqcn
                and prop.type == "name"
            ):
                context = {"recv": "prop", "type": scope.class_fqcn, "prop": text(prop)}
        self._ref(source, method, EdgeKind.CALLS, name_node, context)

    def _static_call(self, node: TsNode, source: str, scope: _Scope) -> None:
        name_node = node.child_by_field_name("name")
        scope_node = node.child_by_field_name("scope")
        if name_node is None or name_node.type != "name" or scope_node is None:
            return
        if scope_node.type not in _NAME_TYPES and scope_node.type != "relative_scope":
            return
        fqcn = self.resolve_class(text(scope_node), scope)
        if fqcn is None:
            return
        context: Binding = {"recv": "type", "type": fqcn, "static": True}
        self._ref(source, text(name_node), EdgeKind.CALLS, name_node, context)

    def _function_call(self, node: TsNode, source: str) -> None:
        function = node.child_by_field_name("function")
        if function is None or function.type not in _NAME_TYPES:
            return
        candidates = self.function_candidates(text(function))
        if candidates:
            self._ref(
                source,
                candidates[0],
                EdgeKind.CALLS,
                function,
                {"functions": candidates},
            )

    # --- запись -------------------------------------------------------------------------

    def _add_node(self, node: Node) -> None:
        self._ids.add(node.id)
        self.nodes.append(node)

    def _unique_id(self, candidate: str, declaration: TsNode) -> str:
        return (
            candidate
            if candidate not in self._ids
            else f"{candidate}:{line(declaration)}"
        )

    def _contains(self, source: str, target: str, declaration: TsNode) -> None:
        self.edges.append(
            Edge(
                source=source,
                target=target,
                kind=EdgeKind.CONTAINS,
                line=line(declaration),
                col=col(declaration),
                provenance=Provenance.SYNTAX,
                confidence=1.0,
                via="php.declaration",
                origin_path=self.path,
            )
        )

    def _ref(
        self, source: str, name: str, kind: EdgeKind, at: TsNode, context: Binding
    ) -> None:
        self.refs.append(
            Ref(
                source=source,
                name=name,
                kind=kind,
                line=line(at),
                col=col(at),
                context=context,
            )
        )


def _use_type(node: TsNode) -> str | None:
    """Вид импорта ``use function`` или ``use const``; ``None`` для классов."""
    for child in node.children:
        if child.type in {"function", "const"}:
            return child.type
    return None


def _int(value: str) -> int | str:
    try:
        return int(value.replace("_", ""), 0)
    except ValueError:
        return value


def _float(value: str) -> float | str:
    try:
        return float(value.replace("_", ""))
    except ValueError:
        return value
