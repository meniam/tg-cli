"""Поиск и проверка ``.atlas.json``.

Конфигурацию ищут от каталога пускового файла вверх, а не от текущего каталога: так
агент попадает в индекс своего проекта из любого места, а у каждого worktree свой индекс.
"""

import hashlib
import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any

from atlas.errors import AtlasError, ExitCode

if TYPE_CHECKING:
    from collections.abc import Mapping, Sequence

CONFIG_NAME = ".atlas.json"
CONFIG_ENV = "ATLAS_CONFIG"
SUPPORTED_VERSION = 1

_TOP_KEYS = frozenset(
    {"$schema", "version", "root", "index", "paths", "symfony", "bridges", "search"}
)
_PATHS_KEYS = frozenset({"code", "docs", "exclude", "scopes"})
_SYMFONY_KEYS = frozenset({"apps"})
_BRIDGES_KEYS = frozenset({"http", "http_wrappers"})
_HTTP_BRIDGE_KEYS = frozenset({"from_codebase", "path_prefix", "to_app", "rewrite"})
_REWRITE_KEYS = frozenset({"from", "to"})
_SEARCH_KEYS = frozenset({"synonyms"})


@dataclass(frozen=True, slots=True)
class HttpBridge:
    """Прокси, через который запросы одной кодовой базы уходят в контур другой."""

    from_codebase: str
    path_prefix: str
    to_app: str
    rewrite_from: str | None
    rewrite_to: str | None


@dataclass(frozen=True, slots=True)
class Config:
    """Проверенная конфигурация проекта.

    ``paths`` — относительно ``root``, ``index`` — относительно каталога ``.atlas.json``.
    """

    root: Path
    config_path: Path
    index_dir: Path
    code: tuple[str, ...]
    docs: tuple[str, ...]
    exclude: tuple[str, ...]
    scopes: Mapping[str, tuple[str, ...]]
    symfony_apps: tuple[str, ...] | None
    http_bridges: tuple[HttpBridge, ...]
    http_wrappers: tuple[str, ...]
    synonyms: Mapping[str, tuple[str, ...]]
    paths_hash: str

    @property
    def database_path(self) -> Path:
        """Файл SQLite внутри каталога индекса."""
        return self.index_dir / "atlas.sqlite"

    @property
    def lock_path(self) -> Path:
        """Файл блокировки записи рядом с базой."""
        return self.index_dir / "atlas.lock"


def discover(launcher: Path, environ: Mapping[str, str] | None = None) -> Path:
    """Найти ``.atlas.json`` для пускового файла.

    Путь пускового файла только приводится к абсолютному: символическая ссылка
    ``bin/atlas`` в проекте ищет конфигурацию своего проекта, даже если указывает на
    общую установку. ``ATLAS_CONFIG`` отключает поиск, это нужно тестам и отладке.

    Returns:
        Путь найденной конфигурации.

    Raises:
        AtlasError: Файл не найден; в ``details`` перечислены просмотренные каталоги.
    """
    env = os.environ if environ is None else environ
    explicit = env.get(CONFIG_ENV)
    if explicit:
        path = Path(explicit).absolute()
        if not path.is_file():
            raise AtlasError(
                f"{CONFIG_ENV} points to a missing file: {path}",
                ExitCode.NO_INDEX,
                searched=[str(path)],
            )
        return path

    searched: list[str] = []
    # WHY: absolute(), а не resolve(): resolve() развернёт символическую ссылку, и
    # ссылка из проекта на общую установку найдёт конфигурацию установки, а не проекта.
    directory = launcher.absolute().parent
    for candidate_dir in (directory, *directory.parents):
        searched.append(str(candidate_dir))
        candidate = candidate_dir / CONFIG_NAME
        if candidate.is_file():
            return candidate
    raise AtlasError(
        f"{CONFIG_NAME} not found above the launcher {launcher}",
        ExitCode.NO_INDEX,
        searched=searched,
    )


def load(config_path: Path) -> Config:
    """Прочитать и проверить конфигурацию.

    Неизвестный ключ — ошибка, а не молчаливое игнорирование: опечатка в ``paths``
    иначе выглядела бы как пустая выдача.

    Returns:
        Проверенная конфигурация проекта.

    Raises:
        AtlasError: Файл не читается, не JSON или не проходит проверку.
    """
    try:
        raw_bytes = config_path.read_bytes()
    except OSError as error:
        raise AtlasError(f"Cannot read {config_path}: {error}") from error
    try:
        data = json.loads(raw_bytes)
    except json.JSONDecodeError as error:
        raise AtlasError(f"{config_path}: invalid JSON: {error}") from error

    config_dir = config_path.parent
    obj = _object(data, "")
    _check_keys(obj, _TOP_KEYS, "")
    version = obj.get("version", SUPPORTED_VERSION)
    if version != SUPPORTED_VERSION:
        raise AtlasError(
            f"version: only {SUPPORTED_VERSION} is supported, got {version!r}"
        )

    if "index" not in obj:
        raise AtlasError(
            'index: required key, the index directory, e.g. "var/cache/atlas"'
        )
    index_dir = _index_dir(config_dir, _string(obj["index"], "index"))
    root = _root(config_dir, obj.get("root"))

    if "paths" not in obj:
        raise AtlasError("paths: required key, what to analyze (code, docs)")
    paths = _object(obj["paths"], "paths")
    _check_keys(paths, _PATHS_KEYS, "paths")
    code = _patterns(paths.get("code", []), "paths.code")
    docs = _patterns(paths.get("docs", []), "paths.docs")
    if not code and not docs:
        raise AtlasError("paths: at least one path in code or docs is required")
    exclude = _patterns(paths.get("exclude", []), "paths.exclude")
    scopes_raw = _object(paths.get("scopes", {}), "paths.scopes")
    scopes = {
        name: _patterns(value, f"paths.scopes.{name}")
        for name, value in scopes_raw.items()
    }

    symfony_apps: tuple[str, ...] | None = None
    if "symfony" in obj:
        symfony = _object(obj["symfony"], "symfony")
        _check_keys(symfony, _SYMFONY_KEYS, "symfony")
        if "apps" in symfony:
            symfony_apps = _strings(symfony["apps"], "symfony.apps")

    bridges = _object(obj.get("bridges", {}), "bridges")
    _check_keys(bridges, _BRIDGES_KEYS, "bridges")
    http_bridges = tuple(
        _http_bridge(item, f"bridges.http[{index}]")
        for index, item in enumerate(_list(bridges.get("http", []), "bridges.http"))
    )
    http_wrappers = _strings(bridges.get("http_wrappers", []), "bridges.http_wrappers")

    search = _object(obj.get("search", {}), "search")
    _check_keys(search, _SEARCH_KEYS, "search")
    synonyms_raw = _object(search.get("synonyms", {}), "search.synonyms")
    synonyms = {
        term.lower(): _variants(variants, f"search.synonyms.{term}")
        for term, variants in synonyms_raw.items()
    }

    paths_hash = hashlib.sha256(
        json.dumps(
            {"paths": paths, "root": str(root.resolve())},
            sort_keys=True,
            ensure_ascii=False,
        ).encode()
    ).hexdigest()
    return Config(
        root=root,
        config_path=config_path,
        index_dir=index_dir,
        code=code,
        docs=docs,
        exclude=exclude,
        scopes=scopes,
        symfony_apps=symfony_apps,
        http_bridges=http_bridges,
        http_wrappers=http_wrappers,
        synonyms=synonyms,
        paths_hash=paths_hash,
    )


def _index_dir(config_dir: Path, value: str) -> Path:
    path = Path(value)
    if path.is_absolute():
        return path
    resolved = (config_dir / path).resolve()
    if not resolved.is_relative_to(config_dir.resolve()):
        raise AtlasError(f"index: relative path escapes the config directory: {value}")
    return config_dir / path


def _root(config_dir: Path, value: object) -> Path:
    """Корень анализируемого проекта; по умолчанию каталог конфигурации."""
    if value is None:
        return config_dir
    path = Path(_string(value, "root"))
    root = path if path.is_absolute() else (config_dir / path).resolve()
    if not root.is_dir():
        raise AtlasError(f"root: directory does not exist: {root}")
    return root


def _http_bridge(value: object, where: str) -> HttpBridge:
    obj = _object(value, where)
    _check_keys(obj, _HTTP_BRIDGE_KEYS, where)
    for required in ("from_codebase", "path_prefix", "to_app"):
        if required not in obj:
            raise AtlasError(f"{where}.{required}: required key")
    rewrite_from: str | None = None
    rewrite_to: str | None = None
    if "rewrite" in obj:
        rewrite = _object(obj["rewrite"], f"{where}.rewrite")
        _check_keys(rewrite, _REWRITE_KEYS, f"{where}.rewrite")
        rewrite_from = _string(rewrite.get("from"), f"{where}.rewrite.from")
        rewrite_to = _string(rewrite.get("to"), f"{where}.rewrite.to")
    return HttpBridge(
        from_codebase=_string(obj["from_codebase"], f"{where}.from_codebase"),
        path_prefix=_string(obj["path_prefix"], f"{where}.path_prefix"),
        to_app=_string(obj["to_app"], f"{where}.to_app"),
        rewrite_from=rewrite_from,
        rewrite_to=rewrite_to,
    )


def _check_keys(obj: Mapping[str, Any], allowed: frozenset[str], where: str) -> None:
    unknown = sorted(set(obj) - allowed)
    if unknown:
        prefix = f"{where}: " if where else ""
        raise AtlasError(
            f"{prefix}unknown keys {unknown}; allowed: {sorted(allowed)}",
            unknown_keys=unknown,
        )


def _object(value: object, where: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise AtlasError(f"{where or 'root'}: object expected")
    return value


def _list(value: object, where: str) -> list[Any]:
    if not isinstance(value, list):
        raise AtlasError(f"{where}: array expected")
    return value


def _string(value: object, where: str) -> str:
    if not isinstance(value, str) or not value:
        raise AtlasError(f"{where}: non-empty string expected")
    return value


def _strings(value: object, where: str) -> tuple[str, ...]:
    return tuple(
        _string(item, f"{where}[{index}]")
        for index, item in enumerate(_list(value, where))
    )


def _variants(value: object, where: str) -> tuple[str, ...]:
    """Варианты синонима: массив строк или одна строка через запятую."""
    if isinstance(value, str):
        value = [part.strip() for part in value.split(",")]
    return _strings(value, where)


def _patterns(value: object, where: str) -> tuple[str, ...]:
    patterns = _strings(value, where)
    for pattern in patterns:
        if pattern.startswith("/") or ".." in Path(pattern).parts:
            raise AtlasError(
                f"{where}: path must be relative and inside the project: {pattern}"
            )
    return tuple(pattern.rstrip("/") for pattern in patterns)


def schema_text() -> str:
    """JSON Schema для редактора: печатается командой ``atlas schema``."""
    return (Path(__file__).parent / "schema" / "atlas.schema.json").read_text(
        encoding="utf-8"
    )


def keys_in_schema() -> dict[str, Sequence[str]]:
    """Ключи, которые понимает проверка; тест сверяет их со схемой."""
    return {
        "": sorted(_TOP_KEYS),
        "paths": sorted(_PATHS_KEYS),
        "symfony": sorted(_SYMFONY_KEYS),
        "bridges": sorted(_BRIDGES_KEYS),
        "search": sorted(_SEARCH_KEYS),
    }
