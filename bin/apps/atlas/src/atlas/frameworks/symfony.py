"""Маршруты Symfony 7 и операции API Platform.

Контур — окружение Symfony (``APP_ENV``) со своим набором файлов маршрутов. Файлы читаются
в порядке ядра: ``config/routes/<env>/*.yaml``, ``config/routes/*.yaml`` с блоками
``when@<env>``, ``config/routes.yaml``. Из них берутся подключения каталогов контроллеров
с префиксами, явные маршруты с ``controller`` и подключение API Platform с префиксом.

Сами маршруты строятся из атрибутов, которые PHP-извлекатель уже сохранил в узлах:
``#[Route]`` на классе и методах, ``#[ApiResource]`` с операциями. Одинаковый маршрут из
нескольких контуров — один узел со списком контуров.
"""

import json
import os
import posixpath
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING, Any

import yaml

from atlas.frameworks.routes import RouteSpec, Target
from atlas.model import EdgeKind

if TYPE_CHECKING:
    from collections.abc import Iterable, Iterator, Mapping

_ROUTE_ATTRIBUTES = ("\\routing\\attribute\\route", "\\routing\\annotation\\route")
_API_RESOURCE = "apiplatform\\metadata\\apiresource"
_API_OPERATIONS: dict[str, tuple[str, str]] = {
    "get": ("GET", "get"),
    "getcollection": ("GET", "get_collection"),
    "post": ("POST", "post"),
    "put": ("PUT", "put"),
    "patch": ("PATCH", "patch"),
    "delete": ("DELETE", "delete"),
    "httpoperation": ("", ""),
}
_ITEM_OPERATIONS = frozenset({"get", "put", "patch", "delete"})
_DEFAULT_OPERATIONS = ("get", "getcollection", "post", "put", "patch", "delete")
_FORMAT_SUFFIX = re.compile(r"\.\{_format\}$")
_MAX_INCLUDE_DEPTH = 8


@dataclass(frozen=True, slots=True)
class AttributeImport:
    """Подключение каталога или файла контроллеров с атрибутами ``#[Route]``."""

    resource: str
    prefix: str = ""
    name_prefix: str = ""
    host: str = ""
    condition: str = ""
    trailing_slash_on_root: bool = True
    origin: str = ""


@dataclass(frozen=True, slots=True)
class YamlRoute:
    """Маршрут, объявленный в YAML с ``controller``."""

    name: str
    path: str
    methods: tuple[str, ...]
    controller: str
    host: str = ""
    condition: str = ""
    origin: str = ""


@dataclass(frozen=True, slots=True)
class ApiImport:
    """Подключение API Platform: ``type: api_platform`` с префиксом."""

    prefix: str
    origin: str = ""


@dataclass(frozen=True, slots=True)
class Contour:
    """Одно окружение Symfony: что в нём подключено.

    ``api_paths`` — каталоги ресурсов API Platform (``mapping.paths``); ``None`` — не
    заданы, подходят все ресурсы.
    """

    env: str
    imports: tuple[AttributeImport, ...] = ()
    routes: tuple[YamlRoute, ...] = ()
    api: tuple[ApiImport, ...] = ()
    api_paths: tuple[str, ...] | None = None


@dataclass(frozen=True, slots=True)
class PhpClass:
    """Класс с атрибутами и его методы: всё, что нужно правилам маршрутов.

    ``methods`` — имя метода в нижнем регистре → ключ узла, имя и атрибуты.
    """

    key: str
    fqcn: str
    path: str
    line: int
    attributes: tuple[dict[str, Any], ...]
    methods: dict[str, tuple[str, str, tuple[dict[str, Any], ...]]] = field(
        default_factory=dict
    )


# --- конфигурация: край файловой системы ----------------------------------------------


class _Loader(yaml.SafeLoader):
    """Загрузчик, который принимает теги Symfony как обычные значения."""


def _any_tag(loader: yaml.SafeLoader, _suffix: str, node: yaml.Node) -> object:
    if isinstance(node, yaml.ScalarNode):
        return loader.construct_scalar(node)
    if isinstance(node, yaml.SequenceNode):
        return loader.construct_sequence(node)
    if isinstance(node, yaml.MappingNode):
        return loader.construct_mapping(node)
    return None


_Loader.add_multi_constructor("!", _any_tag)


def detect(codebase_dir: Path) -> bool:
    """Есть ли Symfony в кодовой базе: ``symfony/framework-bundle`` в ``composer.json``."""
    try:
        data = json.loads((codebase_dir / "composer.json").read_text(encoding="utf-8"))
    except OSError, ValueError:
        return False
    if not isinstance(data, dict):
        return False
    required = {**data.get("require", {}), **data.get("require-dev", {})}
    return "symfony/framework-bundle" in required


def input_files(codebase_dir: Path) -> list[Path]:
    """Файлы, изменение которых меняет маршруты: YAML маршрутов и API Platform."""
    config = codebase_dir / "config"
    found = [config / "routes.yaml", codebase_dir / "composer.json"]
    found += sorted((config / "routes").rglob("*.yaml"))
    found += sorted((config / "packages").rglob("api_platform.yaml"))
    return [path for path in found if path.is_file()]


def read_contours(
    codebase_dir: Path, envs: Iterable[str] | None = None
) -> list[Contour]:
    """Прочитать контуры кодовой базы; окружения — из каталогов ``config/routes`` и ``config/packages``."""
    config = codebase_dir / "config"
    if envs is None:
        names = {
            entry.name
            for parent in (config / "routes", config / "packages")
            if parent.is_dir()
            for entry in parent.iterdir()
            if entry.is_dir()
        }
        envs = sorted(names) or [""]
    return [_read_contour(codebase_dir, env) for env in envs]


def _read_contour(codebase_dir: Path, env: str) -> Contour:
    config = codebase_dir / "config"
    files = sorted((config / "routes" / env).glob("*.yaml")) if env else []
    files += sorted((config / "routes").glob("*.yaml"))
    files.append(config / "routes.yaml")
    collected = _Collected()
    for path in files:
        if path.is_file():
            _read_routes(codebase_dir, path, env, collected, _Options(), 0)
    return Contour(
        env=env,
        imports=tuple(collected.imports),
        routes=tuple(collected.routes),
        api=tuple(collected.api),
        api_paths=_api_paths(codebase_dir, env),
    )


@dataclass(slots=True)
class _Collected:
    imports: list[AttributeImport] = field(default_factory=list)
    routes: list[YamlRoute] = field(default_factory=list)
    api: list[ApiImport] = field(default_factory=list)


@dataclass(frozen=True, slots=True)
class _Options:
    """Параметры, которые вложенный импорт наследует от внешнего."""

    prefix: str = ""
    name_prefix: str = ""
    host: str = ""
    condition: str = ""


def _read_routes(
    codebase_dir: Path,
    path: Path,
    env: str,
    collected: _Collected,
    outer: _Options,
    depth: int,
) -> None:
    data = _load_yaml(path)
    origin = path.relative_to(codebase_dir).as_posix()
    for name, entry in _entries(data, env):
        resource = entry.get("resource")
        options = _Options(
            prefix=outer.prefix + _string(entry.get("prefix")),
            name_prefix=outer.name_prefix + _string(entry.get("name_prefix")),
            host=_string(entry.get("host")) or outer.host,
            condition=_string(entry.get("condition")) or outer.condition,
        )
        if isinstance(resource, str):
            kind = entry.get("type")
            if kind == "api_platform":
                collected.api.append(ApiImport(options.prefix, origin))
            elif resource.startswith("@") or kind not in {None, "attribute", "yaml"}:
                continue
            elif resource.endswith((".yaml", ".yml")):
                target = _join(path.parent, resource)
                if depth < _MAX_INCLUDE_DEPTH and target.is_file():
                    _read_routes(
                        codebase_dir, target, env, collected, options, depth + 1
                    )
            else:
                relative = posixpath.normpath(
                    posixpath.join(
                        path.parent.relative_to(codebase_dir).as_posix(), resource
                    )
                )
                collected.imports.append(
                    AttributeImport(
                        resource=relative.rstrip("/"),
                        prefix=options.prefix,
                        name_prefix=options.name_prefix,
                        host=options.host,
                        condition=options.condition,
                        trailing_slash_on_root=entry.get("trailing_slash_on_root")
                        is not False,
                        origin=origin,
                    )
                )
        elif "path" in entry:
            defaults = entry.get("defaults")
            controller = _string(entry.get("controller")) or (
                _string(defaults.get("_controller"))
                if isinstance(defaults, dict)
                else ""
            )
            if controller:
                collected.routes.append(
                    YamlRoute(
                        name=options.name_prefix + name,
                        path=options.prefix + _localized(entry.get("path")),
                        methods=_methods(entry.get("methods"), {}),
                        controller=controller,
                        host=options.host,
                        condition=options.condition,
                        origin=origin,
                    )
                )


def _entries(data: object, env: str) -> Iterator[tuple[str, dict[str, Any]]]:
    if not isinstance(data, dict):
        return
    for key, value in data.items():
        if not isinstance(key, str) or not isinstance(value, dict):
            continue
        if key.startswith("when@"):
            if key.removeprefix("when@") == env:
                yield from _entries(value, env)
            continue
        yield key, value


def _api_paths(codebase_dir: Path, env: str) -> tuple[str, ...] | None:
    """Каталоги ресурсов API Platform из ``mapping.paths``, относительно кодовой базы."""
    packages = codebase_dir / "config" / "packages"
    candidates = [packages / "api_platform.yaml"]
    if env:
        candidates.append(packages / env / "api_platform.yaml")
    paths: list[str] = []
    for candidate in candidates:
        paths += _mapping_paths(candidate, env, 0)
    if not paths:
        return None
    return tuple(
        posixpath.normpath(
            path.replace("%kernel.project_dir%", "").lstrip("/") or "."
        ).rstrip("/")
        for path in paths
    )


def _mapping_paths(path: Path, env: str, depth: int) -> list[str]:
    data = _load_yaml(path)
    if not isinstance(data, dict) or depth > _MAX_INCLUDE_DEPTH:
        return []
    found: list[str] = []
    imports = data.get("imports")
    if isinstance(imports, list):
        for item in imports:
            resource = item.get("resource") if isinstance(item, dict) else item
            if isinstance(resource, str):
                found += _mapping_paths(_join(path.parent, resource), env, depth + 1)
    blocks = [data, data.get(f"when@{env}")] if env else [data]
    for block in blocks:
        if not isinstance(block, dict):
            continue
        section = block.get("api_platform")
        mapping = section.get("mapping") if isinstance(section, dict) else None
        listed = mapping.get("paths") if isinstance(mapping, dict) else None
        if isinstance(listed, list):
            found += [item for item in listed if isinstance(item, str)]
    return found


def _join(directory: Path, relative: str) -> Path:
    """Путь относительно каталога без раскрытия симлинков: ``relative_to`` должен сработать."""
    return Path(os.path.normpath(directory / relative))


def _load_yaml(path: Path) -> object:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return None
    try:
        return yaml.load(text, Loader=_Loader)  # noqa: S506  # загрузчик на базе SafeLoader
    except yaml.YAMLError:
        return None


# --- маршруты из атрибутов: чистые функции --------------------------------------------


def routes(
    contours: Iterable[Contour],
    classes: Iterable[PhpClass],
    constants: Mapping[str, object],
    codebase: str,
) -> list[RouteSpec]:
    """Маршруты всех контуров; одинаковые маршруты разных контуров слиты в один."""
    classes = list(classes)
    by_fqcn = {php_class.fqcn.lower(): php_class for php_class in classes}
    merged: dict[tuple[Any, ...], RouteSpec] = {}
    for contour in contours:
        for spec in _contour_routes(contour, classes, by_fqcn, constants, codebase):
            identity = (spec.owner, spec.framework, spec.path, spec.methods, spec.name)
            known = merged.get(identity)
            if known is None:
                merged[identity] = spec
            elif contour.env not in known.apps:
                merged[identity] = _with_app(known, contour.env)
    return list(merged.values())


def _with_app(spec: RouteSpec, env: str) -> RouteSpec:
    return RouteSpec(
        framework=spec.framework,
        path=spec.path,
        methods=spec.methods,
        name=spec.name,
        owner=spec.owner,
        line=spec.line,
        targets=spec.targets,
        apps=(*spec.apps, env),
        meta=spec.meta,
    )


def _contour_routes(
    contour: Contour,
    classes: list[PhpClass],
    by_fqcn: Mapping[str, PhpClass],
    constants: Mapping[str, object],
    codebase: str,
) -> Iterator[RouteSpec]:
    apps = (contour.env,) if contour.env else ()
    for attribute_import in contour.imports:
        resource = _project_path(codebase, attribute_import.resource)
        for php_class in classes:
            if not _under(php_class.path, resource):
                continue
            for spec in _attribute_routes(php_class, constants, contour.env):
                yield _imported(spec, attribute_import, apps)
    for yaml_route in contour.routes:
        explicit = _yaml_route(yaml_route, by_fqcn, apps)
        if explicit is not None:
            yield explicit
    for api_import in contour.api:
        roots = (
            None
            if contour.api_paths is None
            else [_project_path(codebase, path) for path in contour.api_paths]
        )
        for php_class in classes:
            if roots is not None and not any(_under(php_class.path, r) for r in roots):
                continue
            yield from _api_routes(php_class, api_import, apps, constants)


def _project_path(codebase: str, path: str) -> str:
    joined = path if codebase in {"", "."} else posixpath.join(codebase, path)
    return posixpath.normpath(joined)


def _under(path: str, resource: str) -> bool:
    if resource in {"", "."}:
        return True
    return path == resource or path.startswith(f"{resource}/")


@dataclass(frozen=True, slots=True)
class _RouteArgs:
    paths: tuple[str, ...]
    name: str
    methods: tuple[str, ...]
    host: str
    condition: str
    env: str
    line: int


def _attribute_routes(
    php_class: PhpClass, constants: Mapping[str, object], env: str
) -> Iterator[RouteSpec]:
    """Маршруты класса без префиксов импорта; правила вложения как у ``AttributeClassLoader``."""
    class_routes = [
        _route_args(a, constants) for a in php_class.attributes if _is_route(a)
    ]
    method_routes = [
        (key, name, _route_args(a, constants))
        for key, name, attributes in php_class.methods.values()
        for a in attributes
        if _is_route(a)
    ]
    if method_routes:
        outer = class_routes or [_RouteArgs(("",), "", (), "", "", "", 0)]
        for key, method_name, inner in method_routes:
            for prefix in outer:
                if not _in_env(inner, env) or not _in_env(prefix, env):
                    continue
                for base in prefix.paths:
                    for path in inner.paths:
                        yield _spec(
                            php_class,
                            path=base + path,
                            name=prefix.name + inner.name,
                            args=inner,
                            methods=inner.methods or prefix.methods,
                            host=inner.host or prefix.host,
                            condition=inner.condition or prefix.condition,
                            target=Target(
                                EdgeKind.ROUTES_TO,
                                "symfony.route.attribute",
                                keys=(key,),
                            ),
                            method=method_name,
                        )
        return
    invoke = php_class.methods.get("__invoke")
    if invoke is None:
        return
    for args in class_routes:
        if not _in_env(args, env):
            continue
        for path in args.paths:
            yield _spec(
                php_class,
                path=path,
                name=args.name,
                args=args,
                methods=args.methods,
                host=args.host,
                condition=args.condition,
                target=Target(
                    EdgeKind.ROUTES_TO, "symfony.route.attribute", keys=(invoke[0],)
                ),
                method=invoke[1],
            )


def _spec(
    php_class: PhpClass,
    *,
    path: str,
    name: str,
    args: _RouteArgs,
    methods: tuple[str, ...],
    host: str,
    condition: str,
    target: Target,
    method: str,
) -> RouteSpec:
    meta: dict[str, Any] = {
        "origin": {"attribute": f"{php_class.path}:{args.line}"},
        "controller": f"{php_class.fqcn}::{method}",
    }
    if host:
        meta["host"] = host
    if condition:
        meta["condition"] = condition
    return RouteSpec(
        framework="symfony",
        path=path or "/",
        methods=methods,
        name=name,
        owner=php_class.key,
        line=args.line or php_class.line,
        targets=(target,),
        meta=meta,
    )


def _imported(
    spec: RouteSpec, attribute_import: AttributeImport, apps: tuple[str, ...]
) -> RouteSpec:
    """Применить префиксы импорта к маршруту из атрибута."""
    prefix = attribute_import.prefix
    if spec.path == "/" and prefix and not attribute_import.trailing_slash_on_root:
        path = prefix
    else:
        path = prefix + spec.path if prefix else spec.path
    meta = dict(spec.meta)
    meta["origin"] = {**meta["origin"], "import": attribute_import.origin}
    if attribute_import.host and "host" not in meta:
        meta["host"] = attribute_import.host
    if attribute_import.condition and "condition" not in meta:
        meta["condition"] = attribute_import.condition
    name = attribute_import.name_prefix + spec.name if spec.name else ""
    return RouteSpec(
        framework=spec.framework,
        path=path.replace("//", "/") or "/",
        methods=spec.methods,
        name=name,
        owner=spec.owner,
        line=spec.line,
        targets=spec.targets,
        apps=apps,
        meta=meta,
    )


def _yaml_route(
    route: YamlRoute, by_fqcn: Mapping[str, PhpClass], apps: tuple[str, ...]
) -> RouteSpec | None:
    fqcn, _, method = route.controller.lstrip("\\").partition("::")
    php_class = by_fqcn.get(fqcn.lower())
    if php_class is None:
        return None
    found = php_class.methods.get((method or "__invoke").lower())
    target_keys = (found[0],) if found is not None else (php_class.key,)
    meta: dict[str, Any] = {
        "origin": {"yaml": route.origin},
        "controller": route.controller,
    }
    if route.host:
        meta["host"] = route.host
    if route.condition:
        meta["condition"] = route.condition
    return RouteSpec(
        framework="symfony",
        path=route.path or "/",
        methods=route.methods,
        name=route.name,
        owner=php_class.key,
        line=php_class.line,
        targets=(Target(EdgeKind.ROUTES_TO, "symfony.route.yaml", keys=target_keys),),
        apps=apps,
        meta=meta,
    )


def _is_route(attribute: dict[str, Any]) -> bool:
    return str(attribute.get("name", "")).lower().endswith(_ROUTE_ATTRIBUTES)


def _route_args(
    attribute: dict[str, Any], constants: Mapping[str, object]
) -> _RouteArgs:
    named = attribute.get("named") or {}
    positional = attribute.get("args") or []
    raw_path = named.get("path", positional[0] if positional else "")
    raw_name = named.get("name", positional[1] if len(positional) > 1 else "")
    if isinstance(raw_path, dict) and "const" not in raw_path:
        paths = tuple(
            value
            for item in raw_path.values()
            if (value := _scalar(item, constants)) is not None
        )
    else:
        paths = (_scalar(raw_path, constants) or "",)
    return _RouteArgs(
        paths=paths or ("",),
        name=_scalar(raw_name, constants) or "",
        methods=_methods(named.get("methods"), constants),
        host=_scalar(named.get("host"), constants) or "",
        condition=_scalar(named.get("condition"), constants) or "",
        env=_scalar(named.get("env"), constants) or "",
        line=int(attribute.get("line") or 0),
    )


def _in_env(args: _RouteArgs, env: str) -> bool:
    return not args.env or args.env == env


def _scalar(value: object, constants: Mapping[str, object]) -> str | None:
    """Строка из литерала или константы класса; неизвестная константа — ``None``."""
    if isinstance(value, str):
        return value
    if isinstance(value, int) and not isinstance(value, bool):
        return str(value)
    if isinstance(value, dict) and isinstance(value.get("const"), str):
        name = str(value["const"])
        known = constants.get(name.lower())
        if isinstance(known, str | int):
            return str(known)
        # WHY: Request::METHOD_POST живёт в vendor и не проиндексирован; значение
        # константы совпадает с её именем после префикса.
        tail = name.rpartition("::")[2]
        if tail.startswith("METHOD_"):
            return tail.removeprefix("METHOD_")
    return None


def _methods(value: object, constants: Mapping[str, object]) -> tuple[str, ...]:
    if isinstance(value, str):
        items: list[object] = [*value.split("|")]
    elif isinstance(value, list):
        items = value
    else:
        return ()
    return tuple(
        sorted(
            {
                found.upper()
                for item in items
                if (found := _scalar(item, constants)) is not None and found
            }
        )
    )


def _string(value: object) -> str:
    return value if isinstance(value, str) else ""


def _localized(value: object) -> str:
    if isinstance(value, dict):
        return next((item for item in value.values() if isinstance(item, str)), "")
    return _string(value)


# --- API Platform ---------------------------------------------------------------------


def _api_routes(
    php_class: PhpClass,
    api_import: ApiImport,
    apps: tuple[str, ...],
    constants: Mapping[str, object],
) -> Iterator[RouteSpec]:
    for attribute in php_class.attributes:
        name = str(attribute.get("name", "")).lower()
        if name == _API_RESOURCE:
            resource = attribute.get("named") or {}
            operations = resource.get("operations")
            if isinstance(operations, list):
                listed = [
                    op for op in operations if isinstance(op, dict) and "new" in op
                ]
            else:
                listed = [
                    {"new": f"ApiPlatform\\Metadata\\{op}"}
                    for op in _DEFAULT_OPERATIONS
                ]
            for operation in listed:
                spec = _operation(
                    php_class,
                    resource,
                    operation,
                    attribute,
                    api_import,
                    apps,
                    constants,
                )
                if spec is not None:
                    yield spec
        elif (
            name.startswith("apiplatform\\metadata\\")
            and name.rpartition("\\")[2] in _API_OPERATIONS
        ):
            operation = {
                "new": attribute["name"],
                "named": attribute.get("named") or {},
            }
            spec = _operation(
                php_class, {}, operation, attribute, api_import, apps, constants
            )
            if spec is not None:
                yield spec


def _operation(
    php_class: PhpClass,
    resource: Mapping[str, Any],
    operation: Mapping[str, Any],
    attribute: Mapping[str, Any],
    api_import: ApiImport,
    apps: tuple[str, ...],
    constants: Mapping[str, object],
) -> RouteSpec | None:
    kind = str(operation["new"]).rpartition("\\")[2].lower()
    if kind not in _API_OPERATIONS:
        return None
    named = operation.get("named") or {}
    method, suffix = _API_OPERATIONS[kind]
    if not method:
        method = (_scalar(named.get("method"), constants) or "GET").upper()
        suffix = method.lower()
    route_prefix = (
        _scalar(named.get("routePrefix", resource.get("routePrefix")), constants) or ""
    )
    template = _scalar(named.get("uriTemplate", resource.get("uriTemplate")), constants)
    meta: dict[str, Any] = {
        "origin": {
            "attribute": f"{php_class.path}:{attribute.get('line', 0)}",
            "import": api_import.origin,
        },
        "operation": str(operation["new"]).rpartition("\\")[2],
    }
    if template is None:
        short = (
            _scalar(resource.get("shortName"), constants)
            or php_class.fqcn.rpartition("\\")[2]
        )
        template = f"/{_snake(short)}s" + ("/{id}" if kind in _ITEM_OPERATIONS else "")
        meta["path_inferred"] = True
    template = _FORMAT_SUFFIX.sub("", template)
    name = (
        _scalar(named.get("name"), constants)
        or f"_api_{route_prefix}{template}_{suffix}"
    )
    targets: list[Target] = []
    for key, method_name, via in (
        ("provider", "provide", "api-platform.provider"),
        ("processor", "process", "api-platform.processor"),
    ):
        fqcn = _class(named.get(key, resource.get(key)))
        if fqcn is not None:
            targets.append(
                Target(EdgeKind.ROUTES_TO, via, names=(f"{fqcn}::{method_name}", fqcn))
            )
    for key in ("input", "output"):
        fqcn = _class(named.get(key, resource.get(key)))
        if fqcn is not None:
            targets.append(
                Target(EdgeKind.REFERENCES, f"api-platform.{key}", names=(fqcn,))
            )
    targets.extend(
        Target(EdgeKind.REFERENCES, "api-platform.extra", names=(fqcn,))
        for fqcn in _classes_in(named.get("extraProperties"))
    )
    return RouteSpec(
        framework="api-platform",
        path=(api_import.prefix + route_prefix + template).replace("//", "/") or "/",
        methods=(method,),
        name=name,
        owner=php_class.key,
        line=int(attribute.get("line") or php_class.line),
        targets=tuple(targets),
        apps=apps,
        meta=meta,
    )


def _class(value: object) -> str | None:
    if isinstance(value, dict) and isinstance(value.get("class"), str):
        return str(value["class"])
    return None


def _classes_in(value: object) -> Iterator[str]:
    if isinstance(value, dict):
        if isinstance(value.get("class"), str):
            yield str(value["class"])
            return
        for item in value.values():
            yield from _classes_in(item)
    elif isinstance(value, list):
        for item in value:
            yield from _classes_in(item)


def _snake(name: str) -> str:
    return re.sub(r"(?<=[a-z0-9])(?=[A-Z])", "_", name).lower()
