"""Извлечение узлов и ссылок из Go.

Пакет Go — каталог, а не файл, поэтому имя в ссылке привязывается к одному из трёх: импорт
(путь пакета и имя в нём), объявление в том же пакете (оно может лежать в соседнем файле,
разрешатель ищет его по каталогу) или ничего. Тип получателя вызова берётся из типа
получателя метода и параметров, из ``var x T``, из составного литерала ``T{}``, ``&T{}``,
``new(T)``, из утверждения типа и из первого результата вызываемой функции.
"""

import re
from dataclasses import dataclass
from pathlib import PurePosixPath
from typing import TYPE_CHECKING, Any

from atlas.model import (
    FLAG_EXPORTED,
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
    MAX_DOCBLOCK,
    col,
    end_line,
    line,
    signature,
    text,
    unquote,
    walk,
)
from atlas.parse.grammars import parser_for

if TYPE_CHECKING:
    from collections.abc import Iterator

    from tree_sitter import Node as TsNode

    from atlas.bindings import Binding

# Предопределённые типы: у них нет узлов в проекте, но известно, что вызов метода на них
# внешний (``err.Error()``), и это не повод угадывать метод по имени.
_PREDECLARED_TYPES = frozenset(
    {
        "any",
        "bool",
        "byte",
        "comparable",
        "complex64",
        "complex128",
        "error",
        "float32",
        "float64",
        "int",
        "int8",
        "int16",
        "int32",
        "int64",
        "rune",
        "string",
        "uint",
        "uint8",
        "uint16",
        "uint32",
        "uint64",
        "uintptr",
    }
)
_BUILTIN_FUNCTIONS = frozenset(
    {
        "append",
        "cap",
        "clear",
        "close",
        "complex",
        "copy",
        "delete",
        "imag",
        "len",
        "make",
        "max",
        "min",
        "new",
        "panic",
        "print",
        "println",
        "real",
        "recover",
    }
)
_PREDECLARED_VALUES = frozenset({"false", "iota", "nil", "true"})
_BUILTIN = "builtin"
_TYPE_NODES = frozenset({"type_identifier", "qualified_type"})
_NAMED_TYPE_NODES = frozenset({"type_identifier", "qualified_type", "generic_type"})
_VERSION_SUFFIX = re.compile(r"^v\d+$")
_GOPKG_VERSION = re.compile(r"\.v\d+$")


def extract(path: str, source: bytes) -> FileExtract:
    """Разобрать Go-файл в узлы, синтаксические рёбра и ссылки."""
    tree = parser_for(Language.GO).parse(source)
    extractor = _GoExtractor(path)
    extractor.run(tree.root_node)
    errors = ("syntax errors in file",) if tree.root_node.has_error else ()
    return FileExtract(
        path=path,
        language=Language.GO,
        nodes=tuple(extractor.nodes),
        refs=tuple(extractor.refs),
        edges=tuple(extractor.edges),
        errors=errors,
    )


def default_package_name(import_path: str) -> str:
    """Имя пакета по пути импорта, если оно не задано псевдонимом.

    Версия модуля и суффиксы ``go-`` и ``.v3`` в имя не входят.

    >>> default_package_name("github.com/acme/shop/orders")
    'orders'
    >>> default_package_name("github.com/acme/shop/v2")
    'shop'
    >>> default_package_name("gopkg.in/yaml.v3")
    'yaml'

    Returns:
        Имя, под которым пакет виден в файле.
    """
    parts = import_path.split("/")
    if len(parts) > 1 and _VERSION_SUFFIX.match(parts[-1]):
        parts.pop()
    name = _GOPKG_VERSION.sub("", parts[-1])
    return name.removeprefix("go-").replace("-", "_").replace(".", "_")


class _Scope:
    """Имена функции: переменные с типом, если он известен, и параметры типа.

    Область плоская: Go ограничивает видимость блоками, но имя, объявленное где-то в
    функции, считается локальным везде в ней. Ложной связи с одноимённым объявлением
    пакета при этом не бывает, пропуск возможен.
    """

    def __init__(self, type_params: frozenset[str] = frozenset()) -> None:
        self.type_params = type_params
        self.variables: dict[str, Binding | None] = {}

    def assign(self, variable: str, binding: Binding | None) -> None:
        # WHY: поток управления не учитывается; переменная с двумя разными типами
        # честно считается неизвестной, а не последней присвоенной.
        if variable in self.variables and self.variables[variable] != binding:
            self.variables[variable] = None
            return
        self.variables[variable] = binding

    def lookup(self, name: str) -> tuple[bool, Binding | None]:
        """Объявлено ли имя в функции и его тип, если он известен."""
        if name in self.variables:
            return True, self.variables[name]
        return False, None


@dataclass(frozen=True, slots=True)
class _Receiver:
    """Получатель метода: имя переменной, тип и параметры типа."""

    variable: str | None
    type_name: str
    type_params: frozenset[str]


class _GoExtractor:
    def __init__(self, path: str) -> None:
        self.path = path
        self.file_id = node_id(NodeKind.FILE, path, path)
        self.nodes: list[Node] = []
        self.refs: list[Ref] = []
        self.edges: list[Edge] = []
        self.imports: dict[str, str] = {}
        self.import_paths: list[str] = []
        self.dot_imports: list[str] = []
        self.type_kinds: dict[str, NodeKind] = {}
        self._ids: set[str] = set()

    # --- верхний уровень ----------------------------------------------------------------

    def run(self, root: TsNode) -> None:
        package = ""
        for child in root.named_children:
            if child.type == "package_clause":
                package = text(child.named_children[0]) if child.named_children else ""
            elif child.type == "import_declaration":
                self._register_imports(child)
            elif child.type == "type_declaration":
                for spec in _type_specs(child):
                    kind = _type_kind(spec)
                    name = text(spec.child_by_field_name("name"))
                    if name:
                        self.type_kinds[name] = kind
        meta: dict[str, Any] = {"package": package}
        if self.import_paths:
            meta["uses"] = sorted(set(self.import_paths))
        if self.dot_imports:
            meta["dot_imports"] = self.dot_imports
        package_clause = next(
            (c for c in root.named_children if c.type == "package_clause"), None
        )
        self._add(
            Node(
                id=self.file_id,
                kind=NodeKind.FILE,
                name=PurePosixPath(self.path).name,
                qualified_name=self.path,
                path=self.path,
                language=Language.GO,
                start_line=1,
                end_line=end_line(root),
                docblock=_doc(package_clause) if package_clause is not None else None,
                meta=meta,
            )
        )
        for child in root.named_children:
            match child.type:
                case "import_declaration":
                    self._import_refs(child)
                case "type_declaration":
                    self._type_declaration(child)
                case "const_declaration" | "var_declaration":
                    self._value_declaration(child)
                case "function_declaration":
                    self._function(child)
                case "method_declaration":
                    self._method(child)
                case _:
                    pass

    def _register_imports(self, declaration: TsNode) -> None:
        for spec in _import_specs(declaration):
            path = _import_path(spec)
            if not path:
                continue
            self.import_paths.append(path)
            name = spec.child_by_field_name("name")
            if name is None:
                self.imports.setdefault(default_package_name(path), path)
            elif name.type == "package_identifier":
                self.imports.setdefault(text(name), path)
            elif name.type == "dot":
                self.dot_imports.append(path)

    def _import_refs(self, declaration: TsNode) -> None:
        for spec in _import_specs(declaration):
            path = _import_path(spec)
            if path:
                self._ref(self.file_id, path, EdgeKind.IMPORTS, spec, {"module": path})

    # --- типы ---------------------------------------------------------------------------

    def _type_declaration(self, declaration: TsNode) -> None:
        specs = _type_specs(declaration)
        grouped = len(specs) > 1
        for spec in specs:
            outer = spec if grouped else declaration
            self._type_spec(spec, outer)

    def _type_spec(self, spec: TsNode, outer: TsNode) -> None:
        name_node = spec.child_by_field_name("name")
        name = text(name_node)
        if not name or name == "_":
            return
        type_node = spec.child_by_field_name("type")
        kind = _type_kind(spec)
        type_params = _type_param_names(spec.child_by_field_name("type_parameters"))
        scope = _Scope(type_params)
        type_id = self._unique_id(node_id(kind, self.path, name), outer)
        header = _header(spec, type_node, kind)
        meta: dict[str, Any] = {}
        members: list[TsNode] = []
        extends: list[Binding] = []
        props: dict[str, Binding] = {}
        if kind in {NodeKind.CLASS, NodeKind.INTERFACE} and type_node is not None:
            members = _members(type_node)
        if kind is NodeKind.CLASS:
            for field in members:
                self._collect_field(field, scope, extends, props)
        elif kind is NodeKind.INTERFACE:
            for member in members:
                embedded = self._embedded_interface(member, scope)
                if embedded is not None:
                    extends.append(embedded)
        elif spec.type == "type_alias" and type_node is not None:
            alias = self._named_type(type_node, scope)
            if alias is not None and _external_type(alias):
                meta["alias"] = alias
        if extends:
            meta["extends"] = extends
        if props:
            meta["props"] = props
        self._add(
            Node(
                id=type_id,
                kind=kind,
                name=name,
                qualified_name=name,
                path=self.path,
                language=Language.GO,
                start_line=line(outer),
                end_line=end_line(outer),
                signature=header,
                docblock=_doc(outer),
                flags=_exported(name),
                meta=meta,
            )
        )
        self._contains(self.file_id, type_id, outer)
        for parent in extends:
            self._ref(type_id, _binding_name(parent), EdgeKind.EXTENDS, spec, parent)
        type_params_node = spec.child_by_field_name("type_parameters")
        self._type_refs(type_params_node, type_id, scope)
        if kind is NodeKind.CLASS:
            for field in members:
                self._field_nodes(field, name, type_id, scope)
        elif kind is NodeKind.INTERFACE:
            for member in members:
                self._interface_method(member, name, type_id, scope)
        elif type_node is not None:
            self._type_refs(type_node, type_id, scope)

    def _collect_field(
        self,
        field: TsNode,
        scope: _Scope,
        extends: list[Binding],
        props: dict[str, Binding],
    ) -> None:
        """Типы полей структуры и встроенные типы для поиска методов и свойств."""
        type_node = field.child_by_field_name("type")
        binding = self._named_type(type_node, scope) if type_node is not None else None
        if binding is None:
            return
        names = field.children_by_field_name("name")
        if names:
            for name in names:
                props[text(name)] = binding
            return
        if _is_builtin(binding):
            return
        extends.append(binding)
        props[_binding_name(binding).rsplit(".", 1)[-1]] = binding

    def _field_nodes(
        self, field: TsNode, owner: str, owner_id: str, scope: _Scope
    ) -> None:
        type_node = field.child_by_field_name("type")
        names = field.children_by_field_name("name")
        if not names:
            # Встроенное поле уже стало ребром ``extends``; здесь только вложенные типы.
            return
        for name_node in names:
            name = text(name_node)
            qualified = f"{owner}.{name}"
            field_id = self._unique_id(
                node_id(NodeKind.PROPERTY, self.path, qualified), field
            )
            self._add(
                Node(
                    id=field_id,
                    kind=NodeKind.PROPERTY,
                    name=name,
                    qualified_name=qualified,
                    path=self.path,
                    language=Language.GO,
                    start_line=line(field),
                    end_line=end_line(field),
                    signature=signature(field, None),
                    docblock=_doc(field),
                    flags=_exported(name),
                )
            )
            self._contains(owner_id, field_id, field)
            self._type_refs(type_node, field_id, scope)

    def _embedded_interface(self, member: TsNode, scope: _Scope) -> Binding | None:
        """Встроенный интерфейс; объединения и ограничения ``~int | string`` не в счёт."""
        if member.type != "type_elem" or len(member.named_children) != 1:
            return None
        inner = member.named_children[0]
        if inner.type not in _NAMED_TYPE_NODES:
            return None
        binding = self._named_type(inner, scope)
        if binding is None or _is_builtin(binding):
            return None
        return binding

    def _interface_method(
        self, member: TsNode, owner: str, owner_id: str, scope: _Scope
    ) -> None:
        if member.type == "type_elem":
            if self._embedded_interface(member, scope) is None:
                self._type_refs(member, owner_id, scope)
            return
        if member.type != "method_elem":
            return
        name = text(member.child_by_field_name("name"))
        if not name:
            return
        qualified = f"{owner}.{name}"
        method_id = self._unique_id(
            node_id(NodeKind.METHOD, self.path, qualified), member
        )
        self._add(
            Node(
                id=method_id,
                kind=NodeKind.METHOD,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.GO,
                start_line=line(member),
                end_line=end_line(member),
                signature=signature(member, None),
                docblock=_doc(member),
                flags=_exported(name),
                meta={"sig": _shape(member)},
            )
        )
        self._contains(owner_id, method_id, member)
        self._signature_refs(member, method_id, scope)

    # --- значения -----------------------------------------------------------------------

    def _value_declaration(self, declaration: TsNode) -> None:
        keyword = "const" if declaration.type == "const_declaration" else "var"
        specs = [
            spec
            for spec in walk(declaration, frozenset({"func_literal"}))
            if spec.type in {"const_spec", "var_spec"}
        ]
        grouped = len(specs) > 1 or any(
            child.type in {"const_spec_list", "var_spec_list"}
            for child in declaration.named_children
        )
        for spec in specs:
            outer = spec if grouped else declaration
            type_node = spec.child_by_field_name("type")
            values = spec.child_by_field_name("value")
            scope = _Scope()
            self._collect_assignments(spec, scope)
            for name_node in spec.children_by_field_name("name"):
                name = text(name_node)
                if not name or name == "_":
                    continue
                value_id = self._unique_id(
                    node_id(NodeKind.CONSTANT, self.path, name), outer
                )
                self._add(
                    Node(
                        id=value_id,
                        kind=NodeKind.CONSTANT,
                        name=name,
                        qualified_name=name,
                        path=self.path,
                        language=Language.GO,
                        start_line=line(outer),
                        end_line=end_line(outer),
                        signature=f"{keyword} {signature(spec, None)}",
                        docblock=_doc(outer),
                        flags=_exported(name),
                    )
                )
                self._contains(self.file_id, value_id, outer)
                self._type_refs(type_node, value_id, scope)
                if values is not None:
                    self._scan(values, value_id, scope)

    # --- функции ------------------------------------------------------------------------

    def _function(self, declaration: TsNode) -> None:
        name = text(declaration.child_by_field_name("name"))
        if not name or name == "_":
            return
        type_params = _type_param_names(
            declaration.child_by_field_name("type_parameters")
        )
        scope = _Scope(type_params)
        function_id = self._callable(
            declaration, NodeKind.FUNCTION, name, name, scope, None
        )
        self._contains(self.file_id, function_id, declaration)

    def _method(self, declaration: TsNode) -> None:
        name = text(declaration.child_by_field_name("name"))
        receiver = _receiver(declaration.child_by_field_name("receiver"))
        if not name or receiver is None:
            return
        scope = _Scope(receiver.type_params)
        qualified = f"{receiver.type_name}.{name}"
        method_id = self._callable(
            declaration, NodeKind.METHOD, name, qualified, scope, receiver
        )
        # WHY: метод лежит в файле типа или в соседнем; ребро ``contains`` от типа возможно
        # только в первом случае, во втором метод принадлежит файлу.
        owner_kind = self.type_kinds.get(receiver.type_name)
        owner = (
            node_id(owner_kind, self.path, receiver.type_name)
            if owner_kind is not None
            else self.file_id
        )
        self._contains(owner, method_id, declaration)

    def _callable(
        self,
        declaration: TsNode,
        kind: NodeKind,
        name: str,
        qualified: str,
        scope: _Scope,
        receiver: _Receiver | None,
    ) -> str:
        function_id = self._unique_id(node_id(kind, self.path, qualified), declaration)
        body = declaration.child_by_field_name("body")
        meta: dict[str, Any] = {}
        result = declaration.child_by_field_name("result")
        first = _first_result_type(result)
        if first is not None:
            returns = self._named_type(first, scope)
            if returns is not None and _external_type(returns):
                meta["returns"] = returns
        if kind is NodeKind.METHOD:
            meta["sig"] = _shape(declaration)
        self._add(
            Node(
                id=function_id,
                kind=kind,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.GO,
                start_line=line(declaration),
                end_line=end_line(declaration),
                signature=signature(declaration, body),
                docblock=_doc(declaration),
                flags=_exported(name),
                meta=meta,
            )
        )
        if receiver is not None and receiver.variable:
            scope.assign(receiver.variable, {"name": receiver.type_name, "local": True})
        self._declare_parameters(declaration.child_by_field_name("parameters"), scope)
        self._declare_parameters(result, scope)
        self._signature_refs(declaration, function_id, scope)
        if body is not None:
            self._collect_assignments(body, scope)
            self._scan(body, function_id, scope)
        return function_id

    def _declare_parameters(self, parameters: TsNode | None, scope: _Scope) -> None:
        if parameters is None or parameters.type != "parameter_list":
            return
        for parameter in parameters.named_children:
            self._declare_parameter(parameter, scope)

    def _declare_parameter(self, parameter: TsNode, scope: _Scope) -> None:
        if parameter.type not in {
            "parameter_declaration",
            "variadic_parameter_declaration",
        }:
            return
        type_node = parameter.child_by_field_name("type")
        binding = (
            self._named_type(type_node, scope)
            if type_node is not None and parameter.type == "parameter_declaration"
            else None
        )
        for name in parameter.children_by_field_name("name"):
            scope.assign(text(name), binding)

    def _signature_refs(self, declaration: TsNode, source: str, scope: _Scope) -> None:
        """Типы параметров, результатов и параметров типа в заголовке."""
        for field in ("type_parameters", "parameters", "result"):
            self._type_refs(declaration.child_by_field_name(field), source, scope)

    # --- привязки и типы ----------------------------------------------------------------

    def _named_type(self, node: TsNode, scope: _Scope) -> Binding | None:
        """Привязка именованного типа: ``T``, ``*T``, ``pkg.T``, ``T[X]``; иначе ``None``."""
        match node.type:
            case "type_identifier":
                return self._local_type(text(node), scope)
            case "qualified_type":
                package = text(node.child_by_field_name("package"))
                name = text(node.child_by_field_name("name"))
                path = self.imports.get(package)
                if path is None or not name:
                    return None
                return {"name": f"{package}.{name}", "from": path, "imported": name}
            case "generic_type":
                head = node.child_by_field_name("type")
                return self._named_type(head, scope) if head is not None else None
            case "pointer_type" | "parenthesized_type":
                inner = node.named_children[0] if node.named_children else None
                return self._named_type(inner, scope) if inner is not None else None
            case _:
                return None

    def _local_type(self, name: str, scope: _Scope) -> Binding | None:
        if name in scope.type_params or name in scope.variables or name == "nil":
            return None
        if name in _PREDECLARED_TYPES:
            return {"name": name, "from": _BUILTIN, "imported": name}
        return {"name": name, "local": True}

    def _type_refs(self, node: TsNode | None, source: str, scope: _Scope) -> None:
        """Ссылки ``references`` на именованные типы внутри выражения типа."""
        if node is None:
            return
        seen: set[str] = set()
        for name_node in _type_names(node):
            binding = self._named_type(name_node, scope)
            if binding is None or _is_builtin(binding):
                continue
            name = _binding_name(binding)
            if name not in seen:
                seen.add(name)
                self._ref(source, name, EdgeKind.REFERENCES, name_node, binding)

    def _value_binding(self, value: TsNode | None, scope: _Scope) -> Binding | None:
        """Тип значения: литерал, ``new(T)``, утверждение типа, результат функции."""
        if value is None:
            return None
        match value.type:
            case "identifier":
                return scope.lookup(text(value))[1]
            case "parenthesized_expression":
                inner = value.named_children[0] if value.named_children else None
                return self._value_binding(inner, scope)
            case "unary_expression":
                if text(value.child_by_field_name("operator")) != "&":
                    return None
                return self._value_binding(value.child_by_field_name("operand"), scope)
            case "composite_literal":
                type_node = value.child_by_field_name("type")
                if type_node is None or type_node.type not in _NAMED_TYPE_NODES:
                    return None
                return self._named_type(type_node, scope)
            case "type_assertion_expression":
                type_node = value.child_by_field_name("type")
                return self._named_type(type_node, scope) if type_node else None
            case "call_expression":
                return self._call_binding(value, scope)
            case _:
                return None

    def _call_binding(self, call: TsNode, scope: _Scope) -> Binding | None:
        function = call.child_by_field_name("function")
        if function is None:
            return None
        function = _generic_head(function)
        if function.type == "identifier":
            name = text(function)
            if name == "new" and not scope.lookup(name)[0]:
                arguments = call.child_by_field_name("arguments")
                first = (
                    arguments.named_children[0]
                    if arguments and arguments.named_children
                    else None
                )
                return self._named_type(first, scope) if first is not None else None
            if name in _BUILTIN_FUNCTIONS or scope.lookup(name)[0]:
                return None
            if name in _PREDECLARED_TYPES:
                return {"name": name, "from": _BUILTIN, "imported": name}
            return {"name": name, "local": True, "ret": True}
        if function.type == "selector_expression":
            binding = self._package_member(function, scope)
            if binding is not None:
                return {**binding, "ret": True}
        return None

    def _package_member(self, selector: TsNode, scope: _Scope) -> Binding | None:
        """``pkg.Name`` с ``pkg`` из импорта и без затеняющей переменной."""
        operand = selector.child_by_field_name("operand")
        field = selector.child_by_field_name("field")
        if operand is None or field is None or operand.type != "identifier":
            return None
        package = text(operand)
        path = self.imports.get(package)
        if path is None or scope.lookup(package)[0]:
            return None
        member = text(field)
        return {"name": f"{package}.{member}", "from": path, "imported": member}

    # --- тела функций -------------------------------------------------------------------

    def _collect_assignments(self, root: TsNode, scope: _Scope) -> None:
        for node in [root, *walk(root)]:
            match node.type:
                case "short_var_declaration" | "assignment_statement":
                    if (
                        node.type == "assignment_statement"
                        and text(node.child_by_field_name("operator")) != "="
                    ):
                        continue
                    self._assign_pairs(
                        node.child_by_field_name("left"),
                        node.child_by_field_name("right"),
                        scope,
                    )
                case "var_spec":
                    self._declare_var(node, scope)
                case "const_spec":
                    for name in node.children_by_field_name("name"):
                        scope.assign(text(name), None)
                case "range_clause" | "receive_statement":
                    for target in _identifiers(node.child_by_field_name("left")):
                        scope.assign(target, None)
                case "type_switch_statement":
                    for target in _identifiers(node.child_by_field_name("alias")):
                        scope.assign(target, None)
                case "type_spec" | "type_alias":
                    declared = text(node.child_by_field_name("name"))
                    if declared:
                        scope.assign(declared, None)
                case "parameter_declaration" | "variadic_parameter_declaration":
                    self._declare_parameter(node, scope)
                case _:
                    pass

    def _declare_var(self, spec: TsNode, scope: _Scope) -> None:
        names = [text(n) for n in spec.children_by_field_name("name")]
        type_node = spec.child_by_field_name("type")
        if type_node is not None:
            binding = self._named_type(type_node, scope)
            for name in names:
                scope.assign(name, binding)
            return
        self._assign_pairs(None, spec.child_by_field_name("value"), scope, names)

    def _assign_pairs(
        self,
        left: TsNode | None,
        right: TsNode | None,
        scope: _Scope,
        names: list[str] | None = None,
    ) -> None:
        targets = names if names is not None else _plain_names(left)
        values = list(right.named_children) if right is not None else []
        for index, target in enumerate(targets):
            if target == "_":
                continue
            binding: Binding | None = None
            if len(values) == len(targets):
                binding = self._value_binding(values[index], scope)
            elif len(values) == 1 and index == 0:
                binding = self._value_binding(values[0], scope)
            scope.assign(target, binding)

    def _scan(self, root: TsNode, source: str, scope: _Scope) -> None:
        for node in [root, *walk(root)]:
            match node.type:
                case "call_expression":
                    self._call(node, source, scope)
                case "composite_literal":
                    self._literal(node, source, scope)
                case "type_conversion_expression":
                    self._conversion(node, source, scope)
                case "type_assertion_expression":
                    self._type_refs(node.child_by_field_name("type"), source, scope)
                case "type_case":
                    for type_node in node.children_by_field_name("type"):
                        self._type_refs(type_node, source, scope)
                case "argument_list":
                    for argument in node.named_children:
                        self._value_ref(argument, source, scope)
                case "literal_element":
                    parent = node.parent
                    is_key = (
                        parent is not None
                        and parent.type == "keyed_element"
                        and parent.child_by_field_name("key") == node
                    )
                    if not is_key and node.named_children:
                        self._value_ref(node.named_children[0], source, scope)
                case "var_spec" | "const_spec":
                    self._type_refs(node.child_by_field_name("type"), source, scope)
                case "func_literal":
                    self._type_refs(
                        node.child_by_field_name("parameters"), source, scope
                    )
                    self._type_refs(node.child_by_field_name("result"), source, scope)
                case _:
                    pass

    def _value_ref(self, node: TsNode, source: str, scope: _Scope) -> None:
        """Функция, переменная или константа, переданная как значение: ``Handle(path, h)``."""
        if node.type == "identifier":
            name = text(node)
            if (
                scope.lookup(name)[0]
                or name == "_"
                or name in _PREDECLARED_VALUES
                or name in _BUILTIN_FUNCTIONS
                or name in _PREDECLARED_TYPES
                or name in self.imports
            ):
                return
            self._ref(
                source, name, EdgeKind.REFERENCES, node, {"name": name, "local": True}
            )
        elif node.type == "selector_expression":
            binding = self._package_member(node, scope)
            if binding is not None:
                member = text(node.child_by_field_name("field"))
                self._ref(source, member, EdgeKind.REFERENCES, node, binding)

    def _literal(self, node: TsNode, source: str, scope: _Scope) -> None:
        type_node = node.child_by_field_name("type")
        if type_node is None:
            return
        if type_node.type in _NAMED_TYPE_NODES:
            binding = self._named_type(type_node, scope)
            if binding is not None and _not_builtin(binding):
                self._ref(
                    source,
                    _binding_name(binding),
                    EdgeKind.INSTANTIATES,
                    type_node,
                    binding,
                )
            return
        self._type_refs(type_node, source, scope)

    def _conversion(self, node: TsNode, source: str, scope: _Scope) -> None:
        """``T(x)`` и ``F[int](x)``: вызов обобщённой функции разбирается как грамматика."""
        type_node = node.child_by_field_name("type")
        if type_node is None:
            return
        if type_node.type == "generic_type":
            head = type_node.child_by_field_name("type")
            if head is not None and head.type in {"type_identifier", "qualified_type"}:
                self._callee_name(_as_expression_name(head), source, scope, head)
            self._type_refs(
                type_node.child_by_field_name("type_arguments"), source, scope
            )
            return
        self._type_refs(type_node, source, scope)

    def _call(self, node: TsNode, source: str, scope: _Scope) -> None:
        function = node.child_by_field_name("function")
        if function is None:
            return
        function = _generic_head(function)
        arguments = node.child_by_field_name("arguments")
        first = (
            arguments.named_children[0]
            if arguments and arguments.named_children
            else None
        )
        if function.type == "identifier" and not scope.lookup(text(function))[0]:
            name = text(function)
            if name == "new" and first is not None:
                self._literal_type(first, source, scope)
                return
            if name == "make" and first is not None:
                self._type_refs(first, source, scope)
                return
        if function.type == "identifier":
            self._callee_name(text(function), source, scope, function)
        elif function.type == "selector_expression":
            self._selector_call(function, source, scope)

    def _literal_type(self, node: TsNode, source: str, scope: _Scope) -> None:
        """Аргумент ``new``: именованный тип создаётся, остальное только упомянуто."""
        if node.type in _NAMED_TYPE_NODES:
            binding = self._named_type(node, scope)
            if binding is not None and _not_builtin(binding):
                self._ref(
                    source, _binding_name(binding), EdgeKind.INSTANTIATES, node, binding
                )
            return
        self._type_refs(node, source, scope)

    def _callee_name(self, name: str, source: str, scope: _Scope, at: TsNode) -> None:
        """Вызов ``f(...)`` или преобразование ``T(x)`` имени пакета или импорта."""
        if "." in name:
            package, _, member = name.partition(".")
            path = self.imports.get(package)
            if path is not None and not scope.lookup(package)[0]:
                binding: Binding = {"name": name, "from": path, "imported": member}
                self._ref(source, member, EdgeKind.CALLS, at, binding)
            return
        if scope.lookup(name)[0] or name in _BUILTIN_FUNCTIONS:
            return
        if name in _PREDECLARED_TYPES:
            return
        self._ref(source, name, EdgeKind.CALLS, at, {"name": name, "local": True})

    def _selector_call(self, function: TsNode, source: str, scope: _Scope) -> None:
        operand = function.child_by_field_name("operand")
        field = function.child_by_field_name("field")
        if operand is None or field is None:
            return
        method = text(field)
        imported = self._package_member(function, scope)
        if imported is not None:
            self._ref(source, method, EdgeKind.CALLS, field, imported)
            return
        context: Binding = {"recv": "unknown"}
        if operand.type == "identifier":
            _, variable = scope.lookup(text(operand))
            if variable is not None:
                context = {"recv": "type", "type": variable}
        elif operand.type == "selector_expression":
            inner = operand.child_by_field_name("operand")
            prop = operand.child_by_field_name("field")
            if inner is not None and prop is not None and inner.type == "identifier":
                _, variable = scope.lookup(text(inner))
                if variable is not None:
                    context = {"recv": "prop", "type": variable, "prop": text(prop)}
        self._ref(source, method, EdgeKind.CALLS, field, context)

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
                via="go.declaration",
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


# --- функции над деревом --------------------------------------------------------------------


def _import_specs(declaration: TsNode) -> list[TsNode]:
    return [node for node in walk(declaration) if node.type == "import_spec"]


def _import_path(spec: TsNode) -> str:
    return unquote(text(spec.child_by_field_name("path")))


def _type_specs(declaration: TsNode) -> list[TsNode]:
    return [
        node for node in walk(declaration) if node.type in {"type_spec", "type_alias"}
    ]


def _type_kind(spec: TsNode) -> NodeKind:
    type_node = spec.child_by_field_name("type")
    if spec.type == "type_spec" and type_node is not None:
        if type_node.type == "struct_type":
            return NodeKind.CLASS
        if type_node.type == "interface_type":
            return NodeKind.INTERFACE
    return NodeKind.TYPE


def _header(spec: TsNode, type_node: TsNode | None, kind: NodeKind) -> str:
    """Заголовок типа в одну строку: ``type Service struct``, без тела."""
    if kind in {NodeKind.CLASS, NodeKind.INTERFACE} and type_node is not None:
        keyword = "struct" if kind is NodeKind.CLASS else "interface"
        return f"type {signature(spec, type_node)} {keyword}"
    return f"type {signature(spec, None)}"


def _members(type_node: TsNode) -> list[TsNode]:
    """Поля структуры или элементы интерфейса."""
    for child in type_node.named_children:
        if child.type == "field_declaration_list":
            return [c for c in child.named_children if c.type == "field_declaration"]
    return [
        c for c in type_node.named_children if c.type in {"type_elem", "method_elem"}
    ]


def _type_param_names(parameters: TsNode | None) -> frozenset[str]:
    if parameters is None:
        return frozenset()
    return frozenset(
        text(name)
        for declaration in parameters.named_children
        if declaration.type == "type_parameter_declaration"
        for name in declaration.children_by_field_name("name")
    )


def _receiver(parameters: TsNode | None) -> _Receiver | None:
    """Получатель метода: ``(s *Service)`` или ``(p Pair[K, V])``."""
    if parameters is None:
        return None
    declaration = next(
        (c for c in parameters.named_children if c.type == "parameter_declaration"),
        None,
    )
    if declaration is None:
        return None
    type_node = declaration.child_by_field_name("type")
    params: frozenset[str] = frozenset()
    while type_node is not None and type_node.type in {
        "pointer_type",
        "parenthesized_type",
    }:
        type_node = type_node.named_children[0] if type_node.named_children else None
    if type_node is not None and type_node.type == "generic_type":
        arguments = type_node.child_by_field_name("type_arguments")
        params = (
            frozenset(
                text(child)
                for child in walk(arguments)
                if child.type == "type_identifier"
            )
            if arguments is not None
            else frozenset()
        )
        type_node = type_node.child_by_field_name("type")
    if type_node is None or type_node.type != "type_identifier":
        return None
    name = declaration.child_by_field_name("name")
    return _Receiver(text(name) if name is not None else None, text(type_node), params)


def _shape(declaration: TsNode) -> str:
    """Форма сигнатуры метода для сравнения с интерфейсом: типы без имён и пакетов.

    Имена параметров и результатов и пакетные префиксы не меняют тип, поэтому
    ``(ctx context.Context) (n int, err error)`` и ``(Context) (int, error)`` совпадают.

    Returns:
        Строка вида ``(int,string)(error)``: параметры и результаты.
    """
    return _shape_key(
        declaration.child_by_field_name("parameters"),
        declaration.child_by_field_name("result"),
    )


def _shape_key(parameters: TsNode | None, result: TsNode | None) -> str:
    results = (
        _parameter_types(result)
        if result is not None and result.type == "parameter_list"
        else [_type_key(result)]
        if result is not None
        else []
    )
    shape = f"({','.join(_parameter_types(parameters))})"
    return f"{shape}({','.join(results)})" if results else shape


def _parameter_types(parameters: TsNode | None) -> list[str]:
    """Тип каждого параметра, по одному на имя: ``a, b int`` даёт два ``int``."""
    found: list[str] = []
    if parameters is None:
        return found
    for child in parameters.named_children:
        if child.type not in {
            "parameter_declaration",
            "variadic_parameter_declaration",
        }:
            continue
        key = _type_key(child.child_by_field_name("type"))
        if child.type == "variadic_parameter_declaration":
            key = f"...{key}"
        found.extend([key] * max(1, len(child.children_by_field_name("name"))))
    return found


def _type_key(node: TsNode | None) -> str:
    """Тип без пакетных префиксов и имён параметров вложенных функций."""
    if node is None:
        return ""
    if node.type == "qualified_type":
        return text(node.child_by_field_name("name"))
    if node.type == "function_type":
        return "func" + _shape_key(
            node.child_by_field_name("parameters"), node.child_by_field_name("result")
        )
    if node.child_count == 0:
        return text(node)
    key = ""
    for child in node.children:
        if child.type == "comment":
            continue
        part = _type_key(child)
        if (
            key
            and (key[-1].isalnum() or key[-1] == "_")
            and (part[:1].isalnum() or part[:1] == "_")
        ):
            key += " "
        key += part
    return key


def _first_result_type(result: TsNode | None) -> TsNode | None:
    """Тип первого результата: ``*Service`` из ``(*Service, error)`` и из ``*Service``."""
    if result is None:
        return None
    if result.type != "parameter_list":
        return result
    first = next(
        (c for c in result.named_children if c.type == "parameter_declaration"), None
    )
    return first.child_by_field_name("type") if first is not None else None


def _type_names(node: TsNode) -> Iterator[TsNode]:
    """Именованные типы выражения типа; имя пакета и имя в нём — один тип."""
    if node.type in _TYPE_NODES:
        yield node
        return
    for child in node.named_children:
        yield from _type_names(child)


def _generic_head(function: TsNode) -> TsNode:
    """``F`` из ``F[int]`` в позиции вызываемого."""
    if function.type == "index_expression":
        operand = function.child_by_field_name("operand")
        if operand is not None:
            return operand
    return function


def _as_expression_name(type_name: TsNode) -> str:
    """Имя типа в позиции вызова: ``pkg.F`` для ``qualified_type``."""
    if type_name.type == "qualified_type":
        return f"{text(type_name.child_by_field_name('package'))}.{text(type_name.child_by_field_name('name'))}"
    return text(type_name)


def _identifiers(node: TsNode | None) -> list[str]:
    if node is None:
        return []
    return [text(child) for child in [node, *walk(node)] if child.type == "identifier"]


def _plain_names(node: TsNode | None) -> list[str]:
    """Имена слева от ``:=`` или ``=``; поле, элемент и разыменование дают пустое имя."""
    if node is None:
        return []
    return [
        text(child) if child.type == "identifier" else ""
        for child in node.named_children
    ]


def _exported(name: str) -> int:
    return FLAG_EXPORTED if name[:1].isupper() else 0


def _is_builtin(binding: Binding) -> bool:
    """Предопределённый тип: ``error``, ``string``, ``int``."""
    return binding.get("from") == _BUILTIN


def _not_builtin(binding: Binding) -> bool:
    return not _is_builtin(binding)


def _external_type(binding: Binding) -> bool:
    """Привязка, которую стоит хранить в метаданных узла: не предопределённый тип."""
    return not _is_builtin(binding)


def _binding_name(binding: Binding) -> str:
    return binding.get("name", "")


def _doc(declaration: TsNode | None) -> str | None:
    """Комментарий, вплотную предшествующий объявлению: подряд идущие ``//`` или ``/* */``."""
    if declaration is None:
        return None
    lines: list[str] = []
    current = declaration
    previous = declaration.prev_sibling
    while (
        previous is not None
        and previous.type == "comment"
        and previous.end_point.row + 1 == current.start_point.row
        and not _trailing(previous)
    ):
        lines.append(_comment_text(text(previous)))
        current = previous
        previous = previous.prev_sibling
    cleaned = "\n".join(reversed(lines)).strip()
    return cleaned[:MAX_DOCBLOCK] or None


def _trailing(comment: TsNode) -> bool:
    """Комментарий в конце строки с кодом относится к этому коду, а не к следующему."""
    before = comment.prev_sibling
    return before is not None and before.end_point.row == comment.start_point.row


def _comment_text(raw: str) -> str:
    if raw.startswith("//"):
        return raw[2:].removeprefix(" ")
    return raw.removeprefix("/*").removesuffix("*/").strip()
