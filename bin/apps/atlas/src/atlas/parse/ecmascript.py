"""Извлечение узлов и ссылок из TypeScript и JavaScript, включая JSX.

Каждое имя в ссылке получает привязку: объявлено в файле или импортировано из модуля.
Разрешатель по привязке находит файл модуля и экспорт в нём; имена без привязки
разрешаются только по однозначному совпадению в кодовой базе.
"""

import re
from pathlib import PurePosixPath
from typing import TYPE_CHECKING, Any

from atlas.model import (
    FLAG_ABSTRACT,
    FLAG_ASYNC,
    FLAG_DEFAULT_EXPORT,
    FLAG_EXPORTED,
    FLAG_STATIC,
    Edge,
    EdgeKind,
    FileExtract,
    HttpCall,
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

_FUNCTION_VALUES = frozenset(
    {"arrow_function", "function_expression", "function", "generator_function"}
)
_CLASS_DECLARATIONS = frozenset(
    {"class_declaration", "abstract_class_declaration", "class"}
)
_NESTED = frozenset(
    {
        "function_declaration",
        "generator_function_declaration",
        "class_declaration",
        "abstract_class_declaration",
        "method_definition",
    }
)
_DIRECTIVES = frozenset(
    {"use client", "use server", "use cache", "use cache: private", "use cache: remote"}
)
_BUILTIN_TYPES = frozenset(
    {
        "Array",
        "Promise",
        "Record",
        "Partial",
        "Required",
        "Readonly",
        "Pick",
        "Omit",
        "Exclude",
        "Extract",
        "ReturnType",
        "Parameters",
        "Awaited",
        "NonNullable",
        "Map",
        "Set",
        "Date",
        "Error",
        "RegExp",
        "Function",
        "Object",
        "String",
        "Number",
        "Boolean",
        "Symbol",
    }
)
MAX_LINE_LENGTH = 1000


def extract(path: str, source: bytes, language: Language) -> FileExtract:
    """Разобрать TS/JS-файл в узлы, синтаксические рёбра и ссылки.

    Минифицированный файл (строка длиннее 1000 символов) не разбирается: связи в нём
    нечитаемы, а разбор дорог.

    Returns:
        Узлы, ссылки, рёбра и ошибки разбора файла.

    """
    if any(len(chunk) > MAX_LINE_LENGTH for chunk in source.split(b"\n")):
        file_node = _file_node(path, language, source.count(b"\n") + 1)
        return FileExtract(
            path, language, (file_node,), (), (), ("minified file skipped",)
        )
    tree = parser_for(language).parse(source)
    extractor = _EcmaExtractor(path, language)
    extractor.run(tree.root_node)
    errors = ("syntax errors in file",) if tree.root_node.has_error else ()
    return FileExtract(
        path=path,
        language=language,
        nodes=tuple(extractor.nodes),
        refs=tuple(extractor.refs),
        edges=tuple(extractor.edges),
        errors=errors,
        http_calls=tuple(extractor.http_calls),
    )


def _file_node(
    path: str, language: Language, last_line: int, meta: dict[str, Any] | None = None
) -> Node:
    return Node(
        id=node_id(NodeKind.FILE, path, path),
        kind=NodeKind.FILE,
        name=PurePosixPath(path).name,
        qualified_name=path,
        path=path,
        language=language,
        start_line=1,
        end_line=last_line,
        meta=meta or {},
    )


class _Scope:
    """Контекст функции: класс и известные типы переменных."""

    def __init__(self, class_name: str | None, parent: Binding | None) -> None:
        self.class_name = class_name
        self.parent = parent
        self.variables: dict[str, Binding | None] = {}

    def assign(self, variable: str, binding: Binding | None) -> None:
        if variable in self.variables and self.variables[variable] != binding:
            self.variables[variable] = None
            return
        self.variables[variable] = binding


class _EcmaExtractor:
    def __init__(self, path: str, language: Language) -> None:
        self.path = path
        self.language = language
        self.file_id = node_id(NodeKind.FILE, path, path)
        self.nodes: list[Node] = []
        self.refs: list[Ref] = []
        self.edges: list[Edge] = []
        self.http_calls: list[HttpCall] = []
        self.imports: dict[str, dict[str, str]] = {}
        self.exports: dict[str, dict[str, str]] = {}
        self.star_exports: list[str] = []
        self.top_level: set[str] = set()
        self._ids: set[str] = set()
        self._default_local: str | None = None
        self._pending_defaults: set[str] = set()

    # --- верхний уровень ----------------------------------------------------------------

    def run(self, root: TsNode) -> None:
        statements = list(root.named_children)
        for statement in statements:
            self._collect_bindings(statement)
        file_scope = _Scope(None, None)
        for statement in statements:
            self._statement(statement, file_scope)
        meta: dict[str, Any] = {}
        directives = _directives(statements)
        if directives:
            meta["directives"] = directives
        if self.imports:
            meta["imports"] = self.imports
        if self.exports:
            meta["exports"] = self.exports
        if self.star_exports:
            meta["star_exports"] = self.star_exports
        self.nodes.insert(0, _file_node(self.path, self.language, end_line(root), meta))
        if self._pending_defaults:
            self.nodes[:] = [
                _with_flags(node, FLAG_DEFAULT_EXPORT | FLAG_EXPORTED)
                if node.qualified_name in self._pending_defaults
                else node
                for node in self.nodes
            ]

    def _collect_bindings(self, statement: TsNode) -> None:
        """Предварительный проход: импорты и имена верхнего уровня, объявленные ниже по файлу."""
        if statement.type == "import_statement":
            self._import(statement)
            return
        target = (
            statement.child_by_field_name("declaration")
            if statement.type == "export_statement"
            else statement
        )
        if target is None:
            return
        for name in _declared_names(target):
            self.top_level.add(name)

    def _import(self, statement: TsNode) -> None:
        source = _string_value(statement.child_by_field_name("source"))
        if source is None:
            return
        clause = next(
            (c for c in statement.named_children if c.type == "import_clause"), None
        )
        if clause is None:
            return
        for child in clause.named_children:
            if child.type == "identifier":
                self.imports[text(child)] = {"from": source, "name": "default"}
            elif child.type == "namespace_import":
                identifier = next(
                    (c for c in child.named_children if c.type == "identifier"), None
                )
                if identifier is not None:
                    self.imports[text(identifier)] = {"from": source, "name": "*"}
            elif child.type == "named_imports":
                for specifier in child.named_children:
                    if specifier.type != "import_specifier":
                        continue
                    name = text(specifier.child_by_field_name("name"))
                    alias_node = specifier.child_by_field_name("alias")
                    local = text(alias_node) if alias_node is not None else name
                    if name:
                        self.imports[local] = {"from": source, "name": name}

    def _statement(self, statement: TsNode, scope: _Scope) -> None:
        match statement.type:
            case "import_statement":
                source = _string_value(statement.child_by_field_name("source"))
                if source is not None:
                    self._ref(
                        self.file_id,
                        source,
                        EdgeKind.IMPORTS,
                        statement,
                        {"module": source},
                    )
            case "export_statement":
                self._export(statement, scope)
            case _:
                self._declaration(statement, scope, exported=False)

    def _export(self, statement: TsNode, scope: _Scope) -> None:
        source = _string_value(statement.child_by_field_name("source"))
        declaration = statement.child_by_field_name("declaration")
        is_default = any(child.type == "default" for child in statement.children)
        if declaration is not None:
            names = self._declaration(
                statement, scope, exported=True, default=is_default
            )
            for name in names:
                self.exports[name] = {"local": name}
            if is_default and names:
                self.exports["default"] = {"local": names[0]}
            return
        clause = next(
            (c for c in statement.named_children if c.type == "export_clause"), None
        )
        if clause is not None:
            for specifier in clause.named_children:
                if specifier.type != "export_specifier":
                    continue
                name = text(specifier.child_by_field_name("name"))
                alias_node = specifier.child_by_field_name("alias")
                exported = text(alias_node) if alias_node is not None else name
                if source is not None:
                    self.exports[exported] = {"from": source, "name": name}
                else:
                    self.exports[exported] = {"local": name}
            if source is not None:
                self._ref(
                    self.file_id,
                    source,
                    EdgeKind.IMPORTS,
                    statement,
                    {"module": source},
                )
            return
        namespace = next(
            (c for c in statement.named_children if c.type == "namespace_export"), None
        )
        if source is not None:
            if namespace is not None:
                identifier = next(
                    (c for c in namespace.named_children if c.type == "identifier"),
                    None,
                )
                if identifier is not None:
                    self.exports[text(identifier)] = {"from": source, "name": "*"}
            else:
                self.star_exports.append(source)
            self._ref(
                self.file_id, source, EdgeKind.IMPORTS, statement, {"module": source}
            )
            return
        value = statement.child_by_field_name("value")
        if is_default and value is not None:
            if value.type == "identifier":
                self.exports["default"] = {"local": text(value)}
                self._pending_defaults.add(text(value))
            else:
                if value.type in _CLASS_DECLARATIONS or value.type in _FUNCTION_VALUES:
                    names = self._named_value(
                        "default", value, statement, scope, exported=True, default=True
                    )
                    if names:
                        self.exports["default"] = {"local": names[0]}
                        return
                self._scan(value, self.file_id, scope)

    # --- объявления ---------------------------------------------------------------------

    def _declaration(
        self, statement: TsNode, scope: _Scope, *, exported: bool, default: bool = False
    ) -> list[str]:
        target = (
            statement.child_by_field_name("declaration")
            if statement.type == "export_statement"
            else statement
        )
        if target is None:
            self._scan(statement, self.file_id, scope)
            return []
        match target.type:
            case "function_declaration" | "generator_function_declaration":
                name = text(target.child_by_field_name("name"))
                if not name:
                    return []
                self._function(
                    name,
                    name,
                    target,
                    statement,
                    None,
                    exported=exported,
                    default=default,
                )
                return [name]
            case kind if kind in _CLASS_DECLARATIONS:
                name = text(target.child_by_field_name("name"))
                if not name:
                    return []
                self._class(name, target, statement, exported=exported, default=default)
                return [name]
            case "lexical_declaration" | "variable_declaration":
                names: list[str] = []
                for declarator in target.named_children:
                    if declarator.type != "variable_declarator":
                        continue
                    name_node = declarator.child_by_field_name("name")
                    value = declarator.child_by_field_name("value")
                    if name_node is None or name_node.type != "identifier":
                        if value is not None:
                            self._scan(value, self.file_id, scope)
                        continue
                    name = text(name_node)
                    if value is None:
                        continue
                    names.extend(
                        self._named_value(
                            name, value, statement, scope, exported=exported
                        )
                    )
                return names
            case (
                "interface_declaration" | "type_alias_declaration" | "enum_declaration"
            ):
                return self._type_declaration(target, statement, exported=exported)
            case _:
                self._scan(target, self.file_id, scope)
                return []

    def _named_value(
        self,
        name: str,
        value: TsNode,
        statement: TsNode,
        scope: _Scope,
        *,
        exported: bool,
        default: bool = False,
    ) -> list[str]:
        """Константа верхнего уровня: функция, класс, обёрнутый компонент или значение."""
        if value.type in _FUNCTION_VALUES:
            self._function(
                name, name, value, statement, None, exported=exported, default=default
            )
            return [name]
        if value.type in _CLASS_DECLARATIONS:
            self._class(name, value, statement, exported=exported, default=default)
            return [name]
        wrapped = _wrapped_function(value)
        if wrapped is not None:
            self._function(
                name,
                name,
                wrapped,
                statement,
                None,
                exported=exported,
                default=default,
                outer=value,
            )
            return [name]
        if exported:
            constant_id = self._unique_id(
                node_id(NodeKind.CONSTANT, self.path, name), statement
            )
            flags = FLAG_EXPORTED | (FLAG_DEFAULT_EXPORT if default else 0)
            self._add(
                Node(
                    id=constant_id,
                    kind=NodeKind.CONSTANT,
                    name=name,
                    qualified_name=name,
                    path=self.path,
                    language=self.language,
                    start_line=line(statement),
                    end_line=end_line(statement),
                    signature=signature(statement, None),
                    flags=flags,
                    # WHY: ``export const GET = handle`` — обработчик маршрута Next.js
                    # объявлен под другим именем; без псевдонима его не найти.
                    meta={"alias": text(value)} if value.type == "identifier" else {},
                )
            )
            self._contains(self.file_id, constant_id, statement)
            self._scan(value, constant_id, scope)
        else:
            self._scan(value, self.file_id, scope)
        return [name] if exported else []

    def _function(
        self,
        name: str,
        qualified: str,
        function: TsNode,
        statement: TsNode,
        class_scope: _Scope | None,
        *,
        exported: bool,
        default: bool = False,
        outer: TsNode | None = None,
        container: str | None = None,
    ) -> str:
        kind = NodeKind.METHOD if class_scope is not None else NodeKind.FUNCTION
        function_id = self._unique_id(node_id(kind, self.path, qualified), function)
        body = function.child_by_field_name("body")
        flags = (FLAG_EXPORTED if exported else 0) | (
            FLAG_DEFAULT_EXPORT if default else 0
        )
        for child in function.children:
            if child.type == "async":
                flags |= FLAG_ASYNC
            elif child.type == "static":
                flags |= FLAG_STATIC
            elif child.type == "abstract":
                flags |= FLAG_ABSTRACT
        meta: dict[str, Any] = {}
        if body is not None and body.type == "statement_block":
            directives = _directives(body.named_children)
            if directives:
                meta["directives"] = directives
        if outer is not None:
            meta["wrapped_by"] = text(outer.child_by_field_name("function"))
        self._add(
            Node(
                id=function_id,
                kind=kind,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=self.language,
                start_line=line(statement),
                end_line=end_line(statement),
                signature=signature(statement if outer is None else function, body),
                docblock=docblock(statement),
                flags=flags,
                meta=meta,
            )
        )
        self._contains(container or self.file_id, function_id, statement)
        scope = _Scope(
            class_scope.class_name if class_scope else None,
            class_scope.parent if class_scope else None,
        )
        parameters = function.child_by_field_name(
            "parameters"
        ) or function.child_by_field_name("parameter")
        if parameters is not None:
            self._parameters(parameters, function_id, scope)
        self._type_refs(function.child_by_field_name("return_type"), function_id)
        if outer is not None:
            for argument in outer.named_children:
                if argument.type == "arguments":
                    for item in argument.named_children:
                        if item.id != function.id:
                            self._scan(item, function_id, scope)
        if body is not None:
            self._collect_assignments(body, scope)
            self._scan(body, function_id, scope)
        return function_id

    def _parameters(self, parameters: TsNode, owner: str, scope: _Scope) -> None:
        if parameters.type == "identifier":
            return
        for parameter in parameters.named_children:
            pattern = parameter.child_by_field_name("pattern")
            type_annotation = parameter.child_by_field_name("type")
            if pattern is not None and pattern.type == "identifier":
                scope.assign(text(pattern), self._annotation_binding(type_annotation))
            self._type_refs(type_annotation, owner)

    def _class(
        self,
        name: str,
        declaration: TsNode,
        statement: TsNode,
        *,
        exported: bool,
        default: bool,
    ) -> None:
        body = declaration.child_by_field_name("body")
        extends: Binding | None = None
        implements: list[Binding] = []
        heritage = next(
            (c for c in declaration.named_children if c.type == "class_heritage"), None
        )
        if heritage is not None:
            for clause in heritage.named_children:
                if clause.type == "extends_clause":
                    value = clause.child_by_field_name("value")
                    if value is not None and value.type == "identifier":
                        extends = self.binding(text(value))
                elif clause.type == "implements_clause":
                    implements.extend(
                        self.binding(_type_name(child))
                        for child in clause.named_children
                        if _type_name(child)
                    )
        if heritage is None:
            for child in declaration.named_children:
                if (
                    child.type == "identifier"
                    and child.prev_sibling is not None
                    and child.prev_sibling.type == "extends"
                ):
                    extends = self.binding(text(child))
        class_id = self._unique_id(
            node_id(NodeKind.CLASS, self.path, name), declaration
        )
        props: dict[str, Binding] = {}
        members = list(body.named_children) if body is not None else []
        for member in members:
            if member.type in {"public_field_definition", "field_definition"}:
                prop_name = text(
                    member.child_by_field_name("name")
                    or member.child_by_field_name("property")
                )
                binding = self._annotation_binding(member.child_by_field_name("type"))
                if prop_name and binding is not None:
                    props[prop_name] = binding
            elif (
                member.type == "method_definition"
                and text(member.child_by_field_name("name")) == "constructor"
            ):
                parameters = member.child_by_field_name("parameters")
                for parameter in (
                    parameters.named_children if parameters is not None else []
                ):
                    has_modifier = any(
                        c.type in {"accessibility_modifier", "readonly"}
                        for c in parameter.children
                    )
                    pattern = parameter.child_by_field_name("pattern")
                    binding = self._annotation_binding(
                        parameter.child_by_field_name("type")
                    )
                    if has_modifier and pattern is not None and binding is not None:
                        props[text(pattern)] = binding
        meta: dict[str, Any] = {}
        if extends:
            meta["extends"] = extends
        if implements:
            meta["implements"] = implements
        if props:
            meta["props"] = props
        flags = (FLAG_EXPORTED if exported else 0) | (
            FLAG_DEFAULT_EXPORT if default else 0
        )
        if declaration.type == "abstract_class_declaration":
            flags |= FLAG_ABSTRACT
        self._add(
            Node(
                id=class_id,
                kind=NodeKind.CLASS,
                name=name,
                qualified_name=name,
                path=self.path,
                language=self.language,
                start_line=line(statement),
                end_line=end_line(statement),
                signature=signature(declaration, body),
                docblock=docblock(statement),
                flags=flags,
                attributes=_decorators(declaration),
                meta=meta,
            )
        )
        self._contains(self.file_id, class_id, statement)
        if extends:
            self._ref(class_id, extends["name"], EdgeKind.EXTENDS, declaration, extends)
        for interface in implements:
            self._ref(
                class_id, interface["name"], EdgeKind.IMPLEMENTS, declaration, interface
            )

        class_scope = _Scope(name, extends)
        for member in members:
            if member.type == "method_definition":
                method_name = text(member.child_by_field_name("name"))
                if method_name:
                    self._function(
                        method_name,
                        f"{name}.{method_name}",
                        member,
                        member,
                        class_scope,
                        exported=False,
                        container=class_id,
                    )
            elif member.type in {"public_field_definition", "field_definition"}:
                value = member.child_by_field_name("value")
                self._type_refs(member.child_by_field_name("type"), class_id)
                if value is not None:
                    if value.type in _FUNCTION_VALUES:
                        field_name = text(
                            member.child_by_field_name("name")
                            or member.child_by_field_name("property")
                        )
                        self._function(
                            field_name,
                            f"{name}.{field_name}",
                            value,
                            member,
                            class_scope,
                            exported=False,
                            container=class_id,
                        )
                    else:
                        self._scan(value, class_id, class_scope)

    def _type_declaration(
        self, declaration: TsNode, statement: TsNode, *, exported: bool
    ) -> list[str]:
        name = text(declaration.child_by_field_name("name"))
        if not name:
            return []
        kind = {
            "interface_declaration": NodeKind.INTERFACE,
            "type_alias_declaration": NodeKind.TYPE,
            "enum_declaration": NodeKind.ENUM,
        }[declaration.type]
        type_id = self._unique_id(node_id(kind, self.path, name), declaration)
        meta: dict[str, Any] = {}
        if kind is NodeKind.INTERFACE:
            extends = [
                self.binding(_type_name(child))
                for clause in declaration.named_children
                if clause.type == "extends_type_clause"
                for child in clause.named_children
                if _type_name(child)
            ]
            if extends:
                meta["extends"] = extends
                for binding in extends:
                    self._ref(
                        type_id, binding["name"], EdgeKind.EXTENDS, declaration, binding
                    )
        self._add(
            Node(
                id=type_id,
                kind=kind,
                name=name,
                qualified_name=name,
                path=self.path,
                language=self.language,
                start_line=line(statement),
                end_line=end_line(statement),
                signature=signature(
                    declaration, declaration.child_by_field_name("body")
                ),
                docblock=docblock(statement),
                flags=FLAG_EXPORTED if exported else 0,
                meta=meta,
            )
        )
        self._contains(self.file_id, type_id, statement)
        body = declaration.child_by_field_name(
            "body"
        ) or declaration.child_by_field_name("value")
        if body is not None and kind is not NodeKind.ENUM:
            self._type_refs(body, type_id)
        return [name]

    # --- привязки и типы ----------------------------------------------------------------

    def binding(self, name: str) -> Binding:
        """Привязка имени: импорт с источником, объявление в файле или ничего."""
        root, _, member = name.partition(".")
        imported = self.imports.get(root)
        if imported is not None:
            result: Binding = {
                "name": name,
                "from": imported["from"],
                "imported": imported["name"],
            }
            if member:
                result["member"] = member
            return result
        if root in self.top_level:
            return {"name": name, "local": True}
        return {"name": name}

    def _annotation_binding(self, annotation: TsNode | None) -> Binding | None:
        """Привязка типа из аннотации, если тип — одно именованное имя."""
        if annotation is None:
            return None
        inner = (
            annotation.named_children[0]
            if annotation.type == "type_annotation" and annotation.named_children
            else annotation
        )
        name = _type_name(inner)
        if not name or name in _BUILTIN_TYPES:
            return None
        binding = self.binding(name)
        return binding if ("from" in binding or "local" in binding) else None

    def _type_refs(self, node: TsNode | None, source: str) -> None:
        if node is None:
            return
        seen: set[str] = set()
        for child in [node, *walk(node)]:
            if child.type != "type_identifier":
                continue
            name = text(child)
            if name in seen or name in _BUILTIN_TYPES:
                continue
            binding = self.binding(name)
            if "from" in binding or "local" in binding:
                seen.add(name)
                self._ref(source, name, EdgeKind.REFERENCES, child, binding)

    # --- тела ---------------------------------------------------------------------------

    def _collect_assignments(self, body: TsNode, scope: _Scope) -> None:
        for node in walk(body, _NESTED):
            if node.type != "variable_declarator":
                continue
            name_node = node.child_by_field_name("name")
            if name_node is None or name_node.type != "identifier":
                continue
            value = node.child_by_field_name("value")
            annotation = self._annotation_binding(node.child_by_field_name("type"))
            if annotation is not None:
                scope.assign(text(name_node), annotation)
            elif value is not None and value.type == "new_expression":
                constructor = value.child_by_field_name("constructor")
                scope.assign(
                    text(name_node),
                    self.binding(text(constructor))
                    if constructor is not None
                    else None,
                )
            else:
                scope.assign(text(name_node), None)

    def _scan(self, root: TsNode, source: str, scope: _Scope) -> None:
        for node in [root, *walk(root, _NESTED)]:
            match node.type:
                case "call_expression":
                    self._call(node, source, scope)
                case "new_expression":
                    constructor = node.child_by_field_name("constructor")
                    if constructor is not None and constructor.type in {
                        "identifier",
                        "member_expression",
                    }:
                        binding = self.binding(text(constructor))
                        self._ref(
                            source,
                            binding["name"],
                            EdgeKind.INSTANTIATES,
                            constructor,
                            binding,
                        )
                case "jsx_opening_element" | "jsx_self_closing_element":
                    name_node = node.child_by_field_name("name")
                    name = text(name_node)
                    if (
                        name_node is not None
                        and name
                        and (name[0].isupper() or "." in name)
                    ):
                        binding = self.binding(name)
                        self._ref(source, name, EdgeKind.RENDERS, name_node, binding)
                case (
                    "type_annotation"
                    | "as_expression"
                    | "satisfies_expression"
                    | "type_arguments"
                ):
                    self._type_refs(node, source)
                case kind if kind in _NESTED and node is not root:
                    self._declaration(node, scope, exported=False)

    def _call(self, node: TsNode, source: str, scope: _Scope) -> None:
        function = node.child_by_field_name("function")
        if function is None:
            return
        self._http_call(node, function, source)
        if function.type == "identifier":
            name = text(function)
            if name in {"require", "import"}:
                arguments = node.child_by_field_name("arguments")
                module = (
                    _string_value(arguments.named_children[0])
                    if arguments and arguments.named_children
                    else None
                )
                if module is not None:
                    self._ref(
                        source, module, EdgeKind.IMPORTS, node, {"module": module}
                    )
                return
            if name in scope.variables:
                return
            binding = self.binding(name)
            self._ref(source, name, EdgeKind.CALLS, function, binding)
            return
        if function.type == "import":
            arguments = node.child_by_field_name("arguments")
            module = (
                _string_value(arguments.named_children[0])
                if arguments and arguments.named_children
                else None
            )
            if module is not None:
                self._ref(source, module, EdgeKind.IMPORTS, node, {"module": module})
            return
        if function.type != "member_expression":
            return
        receiver = function.child_by_field_name("object")
        method_node = function.child_by_field_name("property")
        if receiver is None or method_node is None:
            return
        method = text(method_node)
        context: Binding = {"recv": "unknown"}
        if receiver.type == "this" and scope.class_name:
            context = {
                "recv": "type",
                "type": {"name": scope.class_name, "local": True},
                "self": True,
            }
        elif receiver.type == "super" and scope.parent is not None:
            context = {"recv": "type", "type": scope.parent}
        elif receiver.type == "identifier":
            variable = text(receiver)
            imported = self.imports.get(variable)
            if imported is not None and imported["name"] == "*":
                context = {"recv": "module", "from": imported["from"]}
            elif (variable_binding := scope.variables.get(variable)) is not None:
                context = {"recv": "type", "type": variable_binding}
            elif variable not in scope.variables:
                binding = self.binding(variable)
                if "from" in binding or "local" in binding:
                    context = {"recv": "static", "type": binding}
        elif receiver.type == "member_expression" and scope.class_name:
            inner = receiver.child_by_field_name("object")
            prop = receiver.child_by_field_name("property")
            if inner is not None and inner.type == "this" and prop is not None:
                context = {
                    "recv": "prop",
                    "type": {"name": scope.class_name, "local": True},
                    "prop": text(prop),
                }
        self._ref(source, method, EdgeKind.CALLS, method_node, context)

    def _http_call(self, node: TsNode, function: TsNode, source: str) -> None:
        """Запомнить вызов, первый аргумент которого — путь от корня сайта."""
        if function.type not in {"identifier", "member_expression"}:
            return
        arguments = node.child_by_field_name("arguments")
        values = arguments.named_children if arguments is not None else []
        path = _request_path(values[0]) if values else None
        if path is None:
            return
        method = ""
        for option in values[1:]:
            if option.type == "object":
                method = _object_string(option, "method").upper()
        self.http_calls.append(
            HttpCall(
                source=source,
                callee=text(function)[:80],
                path=path,
                method=method,
                line=line(node),
                col=col(node),
            )
        )

    # --- запись -------------------------------------------------------------------------

    def _add(self, node: Node) -> None:
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
                via="ecmascript.declaration",
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


def _declared_names(declaration: TsNode) -> Iterable[str]:
    if declaration.type in {"lexical_declaration", "variable_declaration"}:
        for declarator in declaration.named_children:
            name = (
                declarator.child_by_field_name("name")
                if declarator.type == "variable_declarator"
                else None
            )
            if name is not None and name.type == "identifier":
                yield text(name)
        return
    name = declaration.child_by_field_name("name")
    if name is not None:
        yield text(name)


def _wrapped_function(value: TsNode) -> TsNode | None:
    """Функция внутри обёртки ``memo(...)``, ``forwardRef(...)``, ``observer(...)``."""
    current = value
    for _ in range(3):
        if current.type != "call_expression":
            return None
        arguments = current.child_by_field_name("arguments")
        if arguments is None or not arguments.named_children:
            return None
        first = arguments.named_children[0]
        if first.type in _FUNCTION_VALUES:
            return first
        current = first
    return None


def _type_name(node: TsNode) -> str:
    match node.type:
        case "type_identifier" | "identifier":
            return text(node)
        case "nested_type_identifier" | "member_expression":
            return text(node)
        case "generic_type":
            name = node.child_by_field_name("name")
            return _type_name(name) if name is not None else ""
        case _:
            return ""


def _string_value(node: TsNode | None) -> str | None:
    if node is None or node.type not in {"string", "template_string"}:
        return None
    value = unquote(text(node))
    return None if "${" in value else value


def _request_path(node: TsNode) -> str | None:
    """Статическая часть пути запроса: ``'/a/b?x'`` → ``/a/b``, вставка в пути → ``{}``.

    ``new URL('/a', base)`` даёт путь своего первого аргумента. Путь, который начинается
    вставкой, не распознаётся: его начало известно только во время работы.

    Returns:
        Путь или ``None``, если первый аргумент не путь от корня.

    """
    if node.type == "new_expression":
        constructor = node.child_by_field_name("constructor")
        arguments = node.child_by_field_name("arguments")
        if text(constructor) != "URL" or arguments is None:
            return None
        values = arguments.named_children
        return _request_path(values[0]) if values else None
    if node.type == "string":
        raw = unquote(text(node))
    elif node.type == "template_string":
        parts: list[str] = []
        for child in node.named_children:
            if child.type == "template_substitution":
                parts.append("{}")
            else:
                parts.append(text(child))
        raw = "".join(parts)
    else:
        return None
    path = re.split(r"[?#]", raw, maxsplit=1)[0]
    if not path.startswith("/") or path.startswith("//") or len(path) < 2:
        return None
    return path


def _object_string(node: TsNode, key: str) -> str:
    """Строковое значение ключа в литерале объекта или пустая строка."""
    for pair in node.named_children:
        if pair.type != "pair":
            continue
        name = pair.child_by_field_name("key")
        value = pair.child_by_field_name("value")
        if text(name).strip("'\"") == key and value is not None:
            return _string_value(value) or ""
    return ""


def _directives(statements: Iterable[TsNode]) -> list[str]:
    found: list[str] = []
    for statement in statements:
        if statement.type != "expression_statement":
            break
        literal = statement.named_children[0] if statement.named_children else None
        value = _string_value(literal)
        if value is None:
            break
        if value in _DIRECTIVES:
            found.append(value)
    return found


def _decorators(declaration: TsNode) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    for child in declaration.children:
        if child.type == "decorator":
            expression = child.named_children[0] if child.named_children else None
            if expression is None:
                continue
            name_node = (
                expression.child_by_field_name("function")
                if expression.type == "call_expression"
                else expression
            )
            result.append(
                {
                    "name": text(name_node),
                    "line": line(child),
                    "expr": text(expression)[:300],
                }
            )
    return tuple(result)


def _with_flags(node: Node, flags: int) -> Node:
    return Node(
        id=node.id,
        kind=node.kind,
        name=node.name,
        qualified_name=node.qualified_name,
        path=node.path,
        language=node.language,
        start_line=node.start_line,
        end_line=node.end_line,
        signature=node.signature,
        docblock=node.docblock,
        attributes=node.attributes,
        flags=node.flags | flags,
        meta=node.meta,
    )
