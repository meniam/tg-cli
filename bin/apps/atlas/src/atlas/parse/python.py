"""Извлечение узлов и ссылок из Python.

Каждое имя в ссылке получает привязку, как в TS: импорт из модуля, объявление в файле
или ничего. Модуль по имени находит разрешатель. Тип получателя вызова берётся из
аннотаций параметров, переменных и атрибутов класса, из присваивания вызова класса и
из ``self.x = параметр`` в методах.
"""

import inspect
import re
from dataclasses import dataclass
from pathlib import PurePosixPath
from typing import TYPE_CHECKING, Any

from atlas.model import (
    FLAG_ABSTRACT,
    FLAG_ASYNC,
    FLAG_EXPORTED,
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
from atlas.parse.common import MAX_DOCBLOCK, col, end_line, line, signature, text, walk
from atlas.parse.grammars import parser_for

if TYPE_CHECKING:
    from collections.abc import Iterable, Iterator

    from tree_sitter import Node as TsNode

    from atlas.bindings import Binding

_DEFINITIONS = frozenset(
    {"function_definition", "class_definition", "decorated_definition"}
)
_COMPOUND = frozenset(
    {
        "if_statement",
        "try_statement",
        "with_statement",
        "for_statement",
        "while_statement",
        "match_statement",
    }
)
_IMPORTS = frozenset({"import_statement", "import_from_statement"})
_EXCEPT = frozenset({"except_clause", "except_group_clause"})
_ENUM_BASES = frozenset({"Enum", "IntEnum", "StrEnum", "Flag", "IntFlag"})
_SKIPPED_BASES = frozenset({"object", "Generic", "Protocol", "ABC"})
_STATIC_DECORATORS = frozenset({"staticmethod", "classmethod"})
_TYPE_WRAPPERS = frozenset(
    {
        "Optional",
        "Annotated",
        "Final",
        "ClassVar",
        "Required",
        "NotRequired",
        "ReadOnly",
    }
)
_TYPE_ALIAS = frozenset({"TypeAlias", "typing.TypeAlias"})
_TYPE_CHECKS = frozenset({"isinstance", "issubclass"})
_DOTTED = re.compile(r"[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*")
_STRING_PREFIX = re.compile(r"^[rRbBuUfFtT]*")


def extract(path: str, source: bytes) -> FileExtract:
    """Разобрать Python-файл в узлы, синтаксические рёбра и ссылки."""
    tree = parser_for(Language.PYTHON).parse(source)
    extractor = _PythonExtractor(path)
    extractor.run(tree.root_node)
    errors = ("syntax errors in file",) if tree.root_node.has_error else ()
    return FileExtract(
        path=path,
        language=Language.PYTHON,
        nodes=tuple(extractor.nodes),
        refs=tuple(extractor.refs),
        edges=tuple(extractor.edges),
        errors=errors,
    )


@dataclass(frozen=True, slots=True)
class _ClassContext:
    """Класс, чьи методы разбираются: его полное имя в файле и первый предок."""

    qualified: str
    parent: Binding | None


class _Scope:
    """Область видимости функции: ``self``, типы переменных, вложенные функции.

    Внешняя область — объемлющая функция или модуль: замыкание видит их имена.
    """

    def __init__(
        self,
        prefix: str,
        outer: _Scope | None = None,
        owner: _ClassContext | None = None,
        receiver: str | None = None,
    ) -> None:
        self.prefix = prefix
        self.outer = outer
        self.owner = owner
        self.receiver = receiver
        self.variables: dict[str, Binding | None] = {}
        self.functions: dict[str, str] = {}

    def assign(self, variable: str, binding: Binding | None) -> None:
        # WHY: поток управления не учитывается; переменная с двумя разными типами
        # честно считается неизвестной, а не последней присвоенной.
        if variable in self.variables and self.variables[variable] != binding:
            self.variables[variable] = None
            return
        self.variables[variable] = binding

    def chain(self) -> Iterator[_Scope]:
        """Эта область и все объемлющие, изнутри наружу."""
        scope: _Scope | None = self
        while scope is not None:
            yield scope
            scope = scope.outer

    def lookup(self, name: str) -> tuple[bool, Binding | None]:
        """Известна ли переменная и её тип, если он известен."""
        for scope in self.chain():
            if name in scope.variables:
                return True, scope.variables[name]
        return False, None

    def function(self, name: str) -> str | None:
        """Полное имя вложенной функции или класса, видимых из области."""
        for scope in self.chain():
            if name in scope.functions:
                return scope.functions[name]
        return None

    def receiver_owner(self, name: str) -> _ClassContext | None:
        """Класс, если ``name`` — первый параметр метода (``self``, ``cls``)."""
        for scope in self.chain():
            if scope.receiver is not None and scope.receiver == name:
                return scope.owner
            if name in scope.variables:
                return None
        return None

    def parent(self) -> Binding | None:
        """Привязка предка класса для ``super()``."""
        for scope in self.chain():
            if scope.owner is not None and scope.receiver is not None:
                return scope.owner.parent
        return None


class _PythonExtractor:
    def __init__(self, path: str) -> None:
        self.path = path
        self.file_id = node_id(NodeKind.FILE, path, path)
        self.nodes: list[Node] = []
        self.refs: list[Ref] = []
        self.edges: list[Edge] = []
        self.imports: dict[str, dict[str, str]] = {}
        self.module_imports: dict[str, dict[str, str]] = {}
        self.star_imports: list[str] = []
        self.top_level: set[str] = set()
        self.public: set[str] | None = None
        self.file_scope = _Scope("")
        self._ids: set[str] = set()

    # --- верхний уровень ----------------------------------------------------------------

    def run(self, root: TsNode) -> None:
        statements = list(root.named_children)
        self._collect(statements)
        for node in walk(root):
            if node.type in _IMPORTS:
                self._register_import(node, self.imports)
        meta: dict[str, Any] = {}
        if self.module_imports:
            meta["imports"] = self.module_imports
        if self.star_imports:
            meta["star_imports"] = self.star_imports
        self._add(
            Node(
                id=self.file_id,
                kind=NodeKind.FILE,
                name=PurePosixPath(self.path).name,
                qualified_name=self.path,
                path=self.path,
                language=Language.PYTHON,
                start_line=1,
                end_line=end_line(root),
                docblock=_docstring(root),
                meta=meta,
            )
        )
        self._module_block(statements)

    def _collect(self, statements: Iterable[TsNode]) -> None:
        """Предварительный проход: импорты и имена модуля, объявленные ниже по файлу."""
        for statement in statements:
            match statement.type:
                case "import_statement" | "import_from_statement":
                    self._register_import(statement, self.imports, self.module_imports)
                case kind if kind in _DEFINITIONS:
                    name = text(_definition(statement).child_by_field_name("name"))
                    if name:
                        self.top_level.add(name)
                case "expression_statement":
                    self._collect_assignment(statement)
                case "type_alias_statement":
                    name = _first_identifier(statement.child_by_field_name("left"))
                    if name:
                        self.top_level.add(name)
                case kind if kind in _COMPOUND:
                    for block in _blocks(statement):
                        self._collect(block.named_children)
                case _:
                    pass

    def _collect_assignment(self, statement: TsNode) -> None:
        assignment = _assignment(statement)
        if assignment is None:
            return
        left = assignment.child_by_field_name("left")
        if left is None or left.type != "identifier":
            return
        name = text(left)
        if name == "__all__":
            right = assignment.child_by_field_name("right")
            if right is not None and right.type in {"list", "tuple"}:
                self.public = {
                    value
                    for item in right.named_children
                    if (value := _string_value(item)) is not None
                }
            return
        self.top_level.add(name)

    def _register_import(
        self, statement: TsNode, *targets: dict[str, dict[str, str]]
    ) -> None:
        """Записать привязки импорта; первая привязка имени побеждает."""
        bindings: dict[str, dict[str, str]] = {}
        if statement.type == "import_statement":
            for name_node in statement.children_by_field_name("name"):
                if name_node.type == "aliased_import":
                    module = text(name_node.child_by_field_name("name"))
                    alias = text(name_node.child_by_field_name("alias"))
                    if module and alias:
                        bindings[alias] = {"from": module, "imported": "*"}
                elif name_node.type == "dotted_name":
                    # WHY: ``import a.b.c`` привязывает только ``a``; путь до ``c`` разрешатель
                    # восстановит из полного имени ``a.b.c.f`` в ссылке.
                    root = text(name_node).split(".", 1)[0]
                    bindings[root] = {"from": root, "imported": "*"}
        else:
            module = _module_name(statement.child_by_field_name("module_name"))
            if not module:
                return
            if any(
                child.type == "wildcard_import" for child in statement.named_children
            ):
                if module not in self.star_imports:
                    self.star_imports.append(module)
                return
            for name_node in statement.children_by_field_name("name"):
                if name_node.type == "aliased_import":
                    imported = text(name_node.child_by_field_name("name"))
                    local = text(name_node.child_by_field_name("alias"))
                else:
                    imported = local = text(name_node)
                if imported and local:
                    bindings[local] = {"from": module, "imported": imported}
        for target in targets:
            for local, binding in bindings.items():
                target.setdefault(local, binding)

    def _module_block(self, statements: Iterable[TsNode]) -> None:
        for statement in statements:
            match statement.type:
                case "import_statement" | "import_from_statement":
                    self._import_refs(statement, self.file_id)
                case kind if kind in _DEFINITIONS:
                    self._definition(statement, self.file_id, self.file_scope, None)
                case "expression_statement":
                    self._module_assignment(statement)
                case "type_alias_statement":
                    self._type_alias(statement)
                case kind if kind in _COMPOUND:
                    self._compound(statement)
                case "future_import_statement" | "comment":
                    pass
                case _:
                    self._scan(statement, self.file_id, self.file_scope)

    def _compound(self, statement: TsNode) -> None:
        """``if``, ``try``, ``with`` на уровне модуля: объявления внутри — тоже модульные."""
        for child in statement.named_children:
            if child.type == "block":
                self._module_block(child.named_children)
            elif child.type.endswith("_clause"):
                if child.type in _EXCEPT:
                    self._except_refs(child, self.file_id)
                self._compound(child)
            else:
                self._scan(child, self.file_id, self.file_scope)

    def _import_refs(self, statement: TsNode, source: str) -> None:
        if statement.type == "import_statement":
            for name_node in statement.children_by_field_name("name"):
                target = _import_target(name_node)
                module = text(target)
                if module:
                    self._ref(
                        source, module, EdgeKind.IMPORTS, name_node, {"module": module}
                    )
            return
        module = _module_name(statement.child_by_field_name("module_name"))
        if not module:
            return
        names = statement.children_by_field_name("name")
        if module.strip(".") or not names:
            imported = [text(_import_target(name_node)) for name_node in names]
            context: Binding = {"module": module}
            if imported:
                context["names"] = imported
            self._ref(source, module, EdgeKind.IMPORTS, statement, context)
            return
        # WHY: в ``from . import a, b`` имена чаще всего подмодули; каждый даёт своё ребро
        # на файл, а если такого файла нет, разрешатель поведёт ребро на сам пакет.
        for name_node in names:
            target = _import_target(name_node)
            submodule = module + text(target)
            self._ref(
                source,
                submodule,
                EdgeKind.IMPORTS,
                name_node,
                {"module": submodule, "package": module},
            )

    # --- объявления ---------------------------------------------------------------------

    def _definition(
        self,
        outer: TsNode,
        container: str,
        enclosing: _Scope,
        owner: _ClassContext | None,
    ) -> None:
        definition = _definition(outer)
        decorators = [
            child for child in outer.named_children if child.type == "decorator"
        ]
        if definition.type == "class_definition":
            self._class(definition, outer, decorators, container, enclosing)
        elif definition.type == "function_definition":
            self._function(definition, outer, decorators, container, enclosing, owner)

    def _function(
        self,
        definition: TsNode,
        outer: TsNode,
        decorators: list[TsNode],
        container: str,
        enclosing: _Scope,
        owner: _ClassContext | None,
    ) -> None:
        name = text(definition.child_by_field_name("name"))
        if not name:
            return
        prefix = owner.qualified if owner is not None else enclosing.prefix
        qualified = f"{prefix}.{name}" if prefix else name
        kind = NodeKind.METHOD if owner is not None else NodeKind.FUNCTION
        function_id = self._unique_id(node_id(kind, self.path, qualified), outer)
        decorator_names = {_dotted_name(d).rsplit(".", 1)[-1] for d in decorators}
        flags = 0
        if any(child.type == "async" for child in definition.children):
            flags |= FLAG_ASYNC
        if decorator_names & _STATIC_DECORATORS:
            flags |= FLAG_STATIC
        if "abstractmethod" in decorator_names:
            flags |= FLAG_ABSTRACT
        if container == self.file_id and self._is_public(name):
            flags |= FLAG_EXPORTED
        body = definition.child_by_field_name("body")
        self._add(
            Node(
                id=function_id,
                kind=kind,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.PYTHON,
                start_line=line(outer),
                end_line=end_line(outer),
                signature=signature(definition, body),
                docblock=_docstring(body),
                attributes=_decorators(decorators),
                flags=flags,
            )
        )
        self._contains(container, function_id, outer)
        self._decorator_refs(decorators, function_id, enclosing)
        scope = self._function_scope(
            definition, qualified, enclosing, owner, decorator_names
        )
        parameters = definition.child_by_field_name("parameters")
        for parameter in parameters.named_children if parameters is not None else []:
            self._type_refs(parameter.child_by_field_name("type"), function_id)
            default = parameter.child_by_field_name("value")
            if default is not None:
                self._scan(default, function_id, enclosing)
        self._type_refs(definition.child_by_field_name("return_type"), function_id)
        if body is not None:
            self._scan(body, function_id, scope)

    def _function_scope(
        self,
        definition: TsNode,
        qualified: str,
        enclosing: _Scope,
        owner: _ClassContext | None,
        decorator_names: set[str],
    ) -> _Scope:
        scope = _Scope(qualified, enclosing, owner)
        parameters = definition.child_by_field_name("parameters")
        expects_receiver = owner is not None and "staticmethod" not in decorator_names
        for parameter in parameters.named_children if parameters is not None else []:
            name, type_node = _parameter(parameter)
            if name is None:
                continue
            if expects_receiver:
                scope.receiver = name
                expects_receiver = False
                continue
            scope.assign(name, self._annotation_binding(type_node))
        body = definition.child_by_field_name("body")
        if body is not None:
            self._collect_assignments(body, scope)
        return scope

    def _class(
        self,
        definition: TsNode,
        outer: TsNode,
        decorators: list[TsNode],
        container: str,
        enclosing: _Scope,
    ) -> None:
        name = text(definition.child_by_field_name("name"))
        if not name:
            return
        qualified = f"{enclosing.prefix}.{name}" if enclosing.prefix else name
        kind = NodeKind.CLASS
        flags = (
            FLAG_EXPORTED if container == self.file_id and self._is_public(name) else 0
        )
        bases: list[Binding] = []
        superclasses = definition.child_by_field_name("superclasses")
        for argument in superclasses.named_children if superclasses is not None else []:
            if argument.type == "keyword_argument":
                if text(argument.child_by_field_name("value")).endswith("ABCMeta"):
                    flags |= FLAG_ABSTRACT
                continue
            base = (
                argument.child_by_field_name("value")
                if argument.type == "subscript"
                else argument
            )
            dotted = _dotted(base) if base is not None else None
            if dotted is None:
                continue
            last = dotted.rsplit(".", 1)[-1]
            if last == "Protocol":
                kind = NodeKind.INTERFACE
            elif last in _ENUM_BASES:
                kind = NodeKind.ENUM
            elif last == "ABC":
                flags |= FLAG_ABSTRACT
            if last not in _SKIPPED_BASES:
                bases.append(self.binding(dotted))
        class_id = self._unique_id(node_id(kind, self.path, qualified), outer)
        context = _ClassContext(qualified, bases[0] if bases else None)
        body = definition.child_by_field_name("body")
        members = list(body.named_children) if body is not None else []
        meta: dict[str, Any] = {}
        if bases:
            meta["extends"] = bases
        props = self._class_props(members, context, enclosing)
        if props:
            meta["props"] = props
        self._add(
            Node(
                id=class_id,
                kind=kind,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.PYTHON,
                start_line=line(outer),
                end_line=end_line(outer),
                signature=signature(definition, body),
                docblock=_docstring(body),
                attributes=_decorators(decorators),
                flags=flags,
                meta=meta,
            )
        )
        self._contains(container, class_id, outer)
        for parent in bases:
            self._ref(class_id, parent["name"], EdgeKind.EXTENDS, definition, parent)
        self._decorator_refs(decorators, class_id, enclosing)

        class_scope = _Scope(qualified, enclosing)
        for member in members:
            if member.type in _DEFINITIONS:
                inner = _definition(member)
                method_owner = context if inner.type == "function_definition" else None
                self._definition(
                    member,
                    class_id,
                    class_scope if method_owner is None else enclosing,
                    method_owner,
                )
            elif member.type == "expression_statement":
                self._class_attribute(member, context, class_id, kind, class_scope)
            elif member.type != "comment":
                self._scan(member, class_id, class_scope)

    def _class_props(
        self, members: list[TsNode], context: _ClassContext, enclosing: _Scope
    ) -> dict[str, Binding]:
        """Типы атрибутов: аннотации в теле класса и присваивания ``self.x`` в методах."""
        props: dict[str, Binding] = {}
        for member in members:
            if member.type == "expression_statement":
                assignment = _assignment(member)
                left = (
                    assignment.child_by_field_name("left")
                    if assignment is not None
                    else None
                )
                if (
                    assignment is not None
                    and left is not None
                    and left.type == "identifier"
                ):
                    binding = self._annotation_binding(
                        assignment.child_by_field_name("type")
                    )
                    if binding is not None:
                        props[text(left)] = binding
                continue
            if member.type not in _DEFINITIONS:
                continue
            definition = _definition(member)
            name = text(definition.child_by_field_name("name"))
            body = definition.child_by_field_name("body")
            if definition.type != "function_definition" or body is None:
                continue
            decorator_names = {
                _dotted_name(d).rsplit(".", 1)[-1]
                for d in member.named_children
                if d.type == "decorator"
            }
            scope = self._function_scope(
                definition,
                f"{context.qualified}.{name}",
                enclosing,
                context,
                decorator_names,
            )
            if scope.receiver is None:
                continue
            for node in walk(body, _DEFINITIONS):
                if node.type != "assignment":
                    continue
                left = node.child_by_field_name("left")
                if left is None or left.type != "attribute":
                    continue
                target = left.child_by_field_name("object")
                attribute = left.child_by_field_name("attribute")
                if (
                    target is None
                    or attribute is None
                    or text(target) != scope.receiver
                ):
                    continue
                binding = self._annotation_binding(
                    node.child_by_field_name("type")
                ) or self._value_binding(node.child_by_field_name("right"), scope)
                if binding is not None:
                    props.setdefault(text(attribute), binding)
        return props

    def _class_attribute(
        self,
        statement: TsNode,
        context: _ClassContext,
        class_id: str,
        class_kind: NodeKind,
        scope: _Scope,
    ) -> None:
        assignment = _assignment(statement)
        left = (
            assignment.child_by_field_name("left") if assignment is not None else None
        )
        if assignment is None or left is None or left.type != "identifier":
            if not (
                statement.named_children
                and statement.named_children[0].type == "string"
            ):
                self._scan(statement, class_id, scope)
            return
        name = text(left)
        is_constant = class_kind is NodeKind.ENUM or name.isupper()
        kind = NodeKind.CONSTANT if is_constant else NodeKind.PROPERTY
        qualified = f"{context.qualified}.{name}"
        attribute_id = self._unique_id(node_id(kind, self.path, qualified), statement)
        self._add(
            Node(
                id=attribute_id,
                kind=kind,
                name=name,
                qualified_name=qualified,
                path=self.path,
                language=Language.PYTHON,
                start_line=line(statement),
                end_line=end_line(statement),
                signature=signature(statement, None),
            )
        )
        self._contains(class_id, attribute_id, statement)
        self._type_refs(assignment.child_by_field_name("type"), attribute_id)
        right = assignment.child_by_field_name("right")
        if right is not None:
            self._scan(right, attribute_id, scope)

    def _module_assignment(self, statement: TsNode) -> None:
        assignment = _assignment(statement)
        left = (
            assignment.child_by_field_name("left") if assignment is not None else None
        )
        if assignment is None or left is None or left.type != "identifier":
            if not (
                statement.named_children
                and statement.named_children[0].type == "string"
            ):
                self._scan(statement, self.file_id, self.file_scope)
            return
        name = text(left)
        type_node = assignment.child_by_field_name("type")
        right = assignment.child_by_field_name("right")
        self.file_scope.assign(
            name,
            self._annotation_binding(type_node)
            or self._value_binding(right, self.file_scope),
        )
        if name == "__all__":
            return
        is_alias = type_node is not None and text(type_node) in _TYPE_ALIAS
        kind = NodeKind.TYPE if is_alias else NodeKind.CONSTANT
        value_id = self._unique_id(node_id(kind, self.path, name), statement)
        self._add(
            Node(
                id=value_id,
                kind=kind,
                name=name,
                qualified_name=name,
                path=self.path,
                language=Language.PYTHON,
                start_line=line(statement),
                end_line=end_line(statement),
                signature=signature(statement, None),
                flags=FLAG_EXPORTED if self._is_public(name) else 0,
            )
        )
        self._contains(self.file_id, value_id, statement)
        self._type_refs(type_node, value_id)
        if right is not None:
            if is_alias:
                self._type_refs(right, value_id)
            else:
                self._scan(right, value_id, self.file_scope)

    def _type_alias(self, statement: TsNode) -> None:
        name = _first_identifier(statement.child_by_field_name("left"))
        if not name:
            return
        alias_id = self._unique_id(node_id(NodeKind.TYPE, self.path, name), statement)
        self._add(
            Node(
                id=alias_id,
                kind=NodeKind.TYPE,
                name=name,
                qualified_name=name,
                path=self.path,
                language=Language.PYTHON,
                start_line=line(statement),
                end_line=end_line(statement),
                signature=signature(statement, None),
                flags=FLAG_EXPORTED if self._is_public(name) else 0,
            )
        )
        self._contains(self.file_id, alias_id, statement)
        self._type_refs(statement.child_by_field_name("right"), alias_id)

    # --- привязки и типы ----------------------------------------------------------------

    def binding(self, name: str) -> Binding:
        """Привязка имени: импорт с модулем, объявление в модуле или ничего."""
        root = name.split(".", 1)[0]
        imported = self.imports.get(root)
        if imported is not None:
            return {
                "name": name,
                "from": imported["from"],
                "imported": imported["imported"],
            }
        if root in self.top_level:
            return {"name": name, "local": True}
        return {"name": name}

    def _known(self, name: str) -> Binding | None:
        """Привязка имени, если оно импортировано или объявлено в модуле."""
        binding = self.binding(name)
        return binding if ("from" in binding or "local" in binding) else None

    def _annotation_binding(self, annotation: TsNode | None) -> Binding | None:
        """Привязка класса из аннотации, если тип однозначен: ``Foo``, ``Foo | None``."""
        name = _single_type(annotation) if annotation is not None else None
        return self._known(name) if name else None

    def _value_binding(self, value: TsNode | None, scope: _Scope) -> Binding | None:
        """Тип значения: вызов класса ``Foo(...)`` или переменная с известным типом."""
        if value is None:
            return None
        if value.type == "identifier":
            return scope.lookup(text(value))[1]
        if value.type != "call":
            return None
        function = value.child_by_field_name("function")
        dotted = _dotted(function) if function is not None else None
        if dotted is None or not _looks_like_class(dotted):
            return None
        nested = scope.function(dotted)
        if nested is not None:
            return {"name": nested, "local": True}
        return self._known(dotted)

    def _type_refs(self, node: TsNode | None, source: str) -> None:
        if node is None:
            return
        seen: set[str] = set()
        for name_node in _names_in(node, strings=True):
            names = (
                _DOTTED.findall(_string_value(name_node) or "")
                if name_node.type == "string"
                else [text(name_node)]
            )
            for name in names:
                binding = self._known(name)
                if binding is not None and name not in seen:
                    seen.add(name)
                    self._ref(source, name, EdgeKind.REFERENCES, name_node, binding)

    def _name_refs(self, node: TsNode | None, source: str) -> None:
        """Ссылки на классы в выражении: ``except``, второй аргумент ``isinstance``."""
        if node is None:
            return
        for name_node in _names_in(node, strings=False):
            name = text(name_node)
            binding = self._known(name)
            if binding is not None:
                self._ref(source, name, EdgeKind.REFERENCES, name_node, binding)

    def _except_refs(self, clause: TsNode, source: str) -> None:
        for child in clause.named_children:
            if child.type == "block":
                break
            target = (
                child.named_children[0]
                if child.type == "as_pattern" and child.named_children
                else child
            )
            self._name_refs(target, source)

    def _decorator_refs(
        self, decorators: list[TsNode], source: str, scope: _Scope
    ) -> None:
        for decorator in decorators:
            expression = (
                decorator.named_children[0] if decorator.named_children else None
            )
            if expression is None:
                continue
            if expression.type == "call":
                self._scan(expression, source, scope)
            else:
                self._callee(expression, source, scope)

    def _is_public(self, name: str) -> bool:
        if self.public is not None:
            return name in self.public
        return not name.startswith("_")

    # --- тела функций -------------------------------------------------------------------

    def _collect_assignments(self, body: TsNode, scope: _Scope) -> None:
        for node in walk(body, _DEFINITIONS | {"lambda"}):
            match node.type:
                case (
                    "function_definition" | "class_definition" | "decorated_definition"
                ):
                    name = text(_definition(node).child_by_field_name("name"))
                    if name:
                        scope.functions[name] = f"{scope.prefix}.{name}"
                case "assignment":
                    left = node.child_by_field_name("left")
                    if left is None:
                        continue
                    if left.type == "identifier":
                        binding = self._annotation_binding(
                            node.child_by_field_name("type")
                        )
                        if binding is None:
                            binding = self._value_binding(
                                node.child_by_field_name("right"), scope
                            )
                        scope.assign(text(left), binding)
                    elif left.type in {"pattern_list", "tuple_pattern", "list_pattern"}:
                        for target in _identifiers(left):
                            scope.assign(target, None)
                case "for_statement" | "for_in_clause":
                    for target in _identifiers(node.child_by_field_name("left")):
                        scope.assign(target, None)
                case "as_pattern_target":
                    for target in _identifiers(node):
                        scope.assign(target, None)
                case "named_expression":
                    name_node = node.child_by_field_name("name")
                    if name_node is not None:
                        scope.assign(text(name_node), None)
                case _:
                    pass

    def _scan(self, root: TsNode, source: str, scope: _Scope) -> None:
        for node in [root, *walk(root, _DEFINITIONS)]:
            match node.type:
                case "call":
                    self._call(node, source, scope)
                case "import_statement" | "import_from_statement":
                    self._import_refs(node, source)
                case kind if kind in _EXCEPT:
                    self._except_refs(node, source)
                case "assignment":
                    self._type_refs(node.child_by_field_name("type"), source)
                case kind if kind in _DEFINITIONS and node is not root:
                    self._definition(node, source, scope, None)
                case _:
                    pass

    def _call(self, node: TsNode, source: str, scope: _Scope) -> None:
        function = node.child_by_field_name("function")
        if function is None:
            return
        if function.type == "identifier" and text(function) in _TYPE_CHECKS:
            arguments = node.child_by_field_name("arguments")
            values = (
                [a for a in arguments.named_children if a.type != "comment"]
                if arguments
                else []
            )
            if len(values) >= 2:
                self._name_refs(values[1], source)
        self._callee(function, source, scope)

    def _callee(self, function: TsNode, source: str, scope: _Scope) -> None:
        """Ссылка на вызываемое: функцию, класс или метод с известным типом получателя."""
        if function.type == "identifier":
            name = text(function)
            nested = scope.function(name)
            if nested is not None:
                binding: Binding = {"name": nested, "local": True}
            elif scope.lookup(name)[0]:
                return
            else:
                binding = self.binding(name)
            kind = EdgeKind.INSTANTIATES if _looks_like_class(name) else EdgeKind.CALLS
            self._ref(source, name, kind, function, binding)
            return
        if function.type != "attribute":
            return
        receiver = function.child_by_field_name("object")
        attribute = function.child_by_field_name("attribute")
        if receiver is None or attribute is None:
            return
        method = text(attribute)
        context: Binding = {"recv": "unknown"}
        dotted = _dotted(function)
        if _is_super_call(receiver):
            parent = scope.parent()
            if parent is not None:
                context = {"recv": "type", "type": parent}
        elif dotted is not None:
            parts = dotted.split(".")
            owner = scope.receiver_owner(parts[0])
            known, variable = scope.lookup(parts[0])
            if owner is not None:
                own_type: Binding = {"name": owner.qualified, "local": True}
                if len(parts) == 2:
                    context = {"recv": "type", "type": own_type, "self": True}
                elif len(parts) == 3:
                    context = {"recv": "prop", "type": own_type, "prop": parts[1]}
            elif known:
                if variable is not None and len(parts) == 2:
                    context = {"recv": "type", "type": variable}
            elif (imported := self._known(dotted)) is not None:
                context = imported
        kind = (
            EdgeKind.INSTANTIATES
            if "recv" not in context and _looks_like_class(method)
            else EdgeKind.CALLS
        )
        self._ref(source, method, kind, attribute, context)

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
                via="python.declaration",
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


def _definition(node: TsNode) -> TsNode:
    """Объявление без декораторов."""
    if node.type == "decorated_definition":
        inner = node.child_by_field_name("definition")
        if inner is not None:
            return inner
    return node


def _import_target(name_node: TsNode) -> TsNode:
    """Импортируемое имя без псевдонима: ``Node`` из ``Node as N``."""
    if name_node.type == "aliased_import":
        inner = name_node.child_by_field_name("name")
        if inner is not None:
            return inner
    return name_node


def _blocks(statement: TsNode) -> Iterator[TsNode]:
    """Блоки составного оператора, включая ветки ``elif``, ``except``, ``finally``."""
    for child in statement.named_children:
        if child.type == "block":
            yield child
        elif child.type.endswith("_clause"):
            yield from _blocks(child)


def _assignment(statement: TsNode) -> TsNode | None:
    first = statement.named_children[0] if statement.named_children else None
    return first if first is not None and first.type == "assignment" else None


def _module_name(node: TsNode | None) -> str:
    """Имя модуля импорта; относительное начинается с точек: ``..model``."""
    return "".join(text(node).split()) if node is not None else ""


def _first_identifier(node: TsNode | None) -> str:
    if node is None:
        return ""
    if node.type == "identifier":
        return text(node)
    return next((text(child) for child in walk(node) if child.type == "identifier"), "")


def _parameter(parameter: TsNode) -> tuple[str | None, TsNode | None]:
    """Имя параметра и его аннотация; у ``*args`` и ``**kwargs`` аннотация не тип переменной."""
    match parameter.type:
        case "identifier":
            return text(parameter), None
        case "typed_parameter":
            inner = parameter.named_children[0] if parameter.named_children else None
            if inner is None:
                return None, None
            if inner.type == "identifier":
                return text(inner), parameter.child_by_field_name("type")
            return _first_identifier(inner) or None, None
        case "default_parameter" | "typed_default_parameter":
            name = parameter.child_by_field_name("name")
            return (text(name) or None), parameter.child_by_field_name("type")
        case "list_splat_pattern" | "dictionary_splat_pattern":
            return _first_identifier(parameter) or None, None
        case _:
            return None, None


def _identifiers(node: TsNode | None) -> list[str]:
    if node is None:
        return []
    return [text(child) for child in [node, *walk(node)] if child.type == "identifier"]


def _dotted(node: TsNode) -> str | None:
    """``a.b.c`` для цепочки имён; ``None``, если в цепочке вызов или индекс."""
    if node.type == "identifier":
        return text(node)
    if node.type == "attribute":
        receiver = node.child_by_field_name("object")
        attribute = node.child_by_field_name("attribute")
        prefix = _dotted(receiver) if receiver is not None else None
        if prefix is not None and attribute is not None:
            return f"{prefix}.{text(attribute)}"
    return None


def _dotted_name(decorator: TsNode) -> str:
    """Имя декоратора: ``dataclass`` из ``@dataclass(frozen=True)``."""
    expression = decorator.named_children[0] if decorator.named_children else None
    if expression is not None and expression.type == "call":
        expression = expression.child_by_field_name("function")
    return (_dotted(expression) if expression is not None else None) or ""


def _decorators(decorators: list[TsNode]) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    for decorator in decorators:
        expression = decorator.named_children[0] if decorator.named_children else None
        if expression is None:
            continue
        result.append(
            {
                "name": _dotted_name(decorator) or text(expression),
                "line": line(decorator),
                "expr": text(expression)[:300],
            }
        )
    return tuple(result)


def _single_type(node: TsNode) -> str | None:
    """Имя класса из аннотации, если тип однозначен: ``Foo``, ``Foo | None``, ``Optional[Foo]``."""
    match node.type:
        case "type":
            return _single_type(node.named_children[0]) if node.named_children else None
        case "identifier" | "attribute":
            return _dotted(node)
        case "string":
            value = _string_value(node)
            if value is None:
                return None
            parts = [
                part.strip() for part in value.split("|") if part.strip() != "None"
            ]
            return parts[0] if len(parts) == 1 and _DOTTED.fullmatch(parts[0]) else None
        case "binary_operator":
            sides = [
                node.child_by_field_name("left"),
                node.child_by_field_name("right"),
            ]
            meaningful = [
                side for side in sides if side is not None and side.type != "none"
            ]
            return _single_type(meaningful[0]) if len(meaningful) == 1 else None
        case "generic_type" | "subscript":
            head = node.named_children[0] if node.named_children else None
            name = _dotted(head) if head is not None else None
            if name is None:
                return None
            if name.rsplit(".", 1)[-1] in _TYPE_WRAPPERS:
                arguments = [
                    child
                    for child in node.named_children[1:]
                    if child.type != "comment"
                ]
                inner = arguments[0] if arguments else None
                if inner is not None and inner.type == "type_parameter":
                    inner = inner.named_children[0] if inner.named_children else None
                return _single_type(inner) if inner is not None else None
            return name
        case _:
            return None


def _names_in(node: TsNode, *, strings: bool) -> Iterator[TsNode]:
    """Имена и цепочки ``a.b`` в выражении или аннотации; строки — отложенные аннотации."""
    match node.type:
        case "identifier":
            yield node
        case "attribute" if _dotted(node) is not None:
            yield node
        case "string":
            if strings:
                yield node
        case "call" | "keyword_argument" | "lambda":
            return
        case _:
            for child in node.named_children:
                yield from _names_in(child, strings=strings)


def _looks_like_class(name: str) -> bool:
    # WHY: вызов класса и функции в Python синтаксически неразличимы; по PEP 8 классы
    # пишутся с заглавной, и этого признака хватает, чтобы отличить создание объекта.
    return name.rsplit(".", 1)[-1].lstrip("_")[:1].isupper()


def _is_super_call(node: TsNode) -> bool:
    if node.type != "call":
        return False
    function = node.child_by_field_name("function")
    return (
        function is not None
        and function.type == "identifier"
        and text(function) == "super"
    )


def _string_value(node: TsNode) -> str | None:
    """Содержимое строкового литерала без интерполяции."""
    if node.type != "string" or any(
        child.type == "interpolation" for child in node.named_children
    ):
        return None
    return "".join(
        text(child) for child in node.named_children if child.type == "string_content"
    )


def _docstring(body: TsNode | None) -> str | None:
    """Строка документации: первый оператор тела, если это строковый литерал."""
    if body is None:
        return None
    first = next(
        (child for child in body.named_children if child.type != "comment"), None
    )
    if (
        first is None
        or first.type != "expression_statement"
        or not first.named_children
    ):
        return None
    literal = first.named_children[0]
    if literal.type != "string":
        return None
    raw = _STRING_PREFIX.sub("", text(literal), count=1)
    for quote in ('"""', "'''", '"', "'"):
        if raw.startswith(quote) and raw.endswith(quote) and len(raw) >= 2 * len(quote):
            raw = raw[len(quote) : -len(quote)]
            break
    cleaned = inspect.cleandoc(raw)
    return cleaned[:MAX_DOCBLOCK] or None
