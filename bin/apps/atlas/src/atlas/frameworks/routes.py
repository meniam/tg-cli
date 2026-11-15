r"""Маршрут как данные и сопоставление пути запроса с шаблоном маршрута.

Шаблоны двух видов: Symfony (``/a/{id<\\d+>}``, ``/{_locale?en}``) и Next.js (``/a/:id``,
``/a/:path+``, ``/a/:path*``). Путь запроса может нести ``{}`` на месте вставки
шаблонной строки: такой сегмент совпадает с любым сегментом маршрута, но слабее литерала.
"""

import re
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from atlas.model import EdgeKind

_PLACEHOLDER = re.compile(r"\{!?(\w+)(?:<([^>]*)>)?(\?[^}]*)?\}")
_SLASH_REQUIREMENTS = frozenset({".+", ".*", ".+?", ".*?"})

# Вес сегмента: литерал против литерала сильнее параметра, параметр сильнее вставки.
_STATIC = 3
_PARAM = 2
_WILDCARD = 1

CONFIDENCE_STATIC = 0.9
CONFIDENCE_PARAM = 0.7
CONFIDENCE_WILDCARD = 0.6
CONFIDENCE_CATCH_ALL = 0.5


@dataclass(frozen=True, slots=True)
class Target:
    """Куда ведёт маршрут: узел по ключу или по полному имени, в порядке предпочтения."""

    edge: EdgeKind
    via: str
    keys: tuple[str, ...] = ()
    names: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class RouteSpec:
    """Маршрут до записи в граф.

    ``owner`` — ключ узла, в чьём файле живёт маршрут: класс контроллера, класс ресурса,
    файл страницы. ``methods`` пуст, если маршрут принимает любой метод. ``apps`` — контуры
    Symfony, где маршрут подключён; у Next.js пусто.
    """

    framework: str
    path: str
    methods: tuple[str, ...]
    name: str
    owner: str
    line: int
    targets: tuple[Target, ...] = ()
    apps: tuple[str, ...] = ()
    meta: dict[str, Any] = field(default_factory=dict)

    @property
    def title(self) -> str:
        """Метод и путь: ``GET /api/v1/x``, ``ANY /login``."""
        return f"{'|'.join(self.methods) or 'ANY'} {self.path}"


@dataclass(frozen=True, slots=True)
class _Segment:
    kind: str  # static | param | rest
    text: str
    pattern: re.Pattern[str] | None = None
    optional: bool = False
    minimum: int = 0


@dataclass(frozen=True, slots=True)
class Match:
    """Совпадение пути с маршрутом: вес для выбора лучшего и уверенность ребра."""

    score: int
    confidence: float


def normalize(path: str) -> str:
    """Схлопнуть повторные слэши и убрать конечный, кроме корня."""
    collapsed = re.sub(r"/{2,}", "/", path)
    if len(collapsed) > 1:
        collapsed = collapsed.rstrip("/")
    return collapsed or "/"


def compile_path(path: str) -> tuple[_Segment, ...]:
    """Разобрать шаблон маршрута в сегменты."""
    segments: list[_Segment] = []
    for raw in _split(normalize(path).strip("/")):
        if not raw:
            continue
        if raw.startswith(":"):
            name = raw[1:]
            if name.endswith(("+", "*")):
                segments.append(
                    _Segment("rest", raw, minimum=1 if name.endswith("+") else 0)
                )
            else:
                segments.append(_Segment("param", raw, re.compile(r"[^/]+")))
            continue
        placeholders = list(_PLACEHOLDER.finditer(raw))
        if not placeholders:
            segments.append(_Segment("static", raw))
            continue
        whole = placeholders[0]
        if len(placeholders) == 1 and whole.group(0) == raw:
            requirement = whole.group(2)
            optional = whole.group(3) is not None
            if requirement in _SLASH_REQUIREMENTS or "/" in (requirement or ""):
                segments.append(_Segment("rest", raw, minimum=0 if optional else 1))
                continue
            segments.append(
                _Segment("param", raw, _requirement(requirement), optional=optional)
            )
            continue
        pattern = ""
        cursor = 0
        for placeholder in placeholders:
            pattern += re.escape(raw[cursor : placeholder.start()])
            requirement = placeholder.group(2)
            pattern += f"(?:{requirement})" if requirement else "[^/]+"
            cursor = placeholder.end()
        pattern += re.escape(raw[cursor:])
        segments.append(_Segment("param", raw, re.compile(pattern)))
    # WHY: у Symfony необязателен только хвост из необязательных параметров; такой же
    # параметр перед обязательным сегментом обязателен.
    trailing = True
    for index in range(len(segments) - 1, -1, -1):
        segment = segments[index]
        if segment.optional and not trailing:
            segments[index] = _Segment(segment.kind, segment.text, segment.pattern)
        trailing = trailing and (
            segment.optional or (segment.kind == "rest" and segment.minimum == 0)
        )
    return tuple(segments)


def _split(path: str) -> list[str]:
    """Сегменты пути; слэш внутри ``{...}`` — часть требования, а не граница сегмента."""
    parts: list[str] = []
    current = ""
    depth = 0
    for char in path:
        if char == "/" and depth == 0:
            parts.append(current)
            current = ""
            continue
        depth += {"{": 1, "}": -1}.get(char, 0)
        current += char
    parts.append(current)
    return parts


def _requirement(requirement: str | None) -> re.Pattern[str]:
    if not requirement:
        return re.compile(r"[^/]+")
    try:
        return re.compile(f"(?:{requirement.strip('^$')})")
    except re.error:
        return re.compile(r"[^/]+")


def match(route: tuple[_Segment, ...], path: str) -> Match | None:
    """Совпадение пути запроса с шаблоном или ``None``."""
    parts = tuple(part for part in normalize(path).strip("/").split("/") if part)
    return _match(route, parts, 0, 0)


def _match(
    route: tuple[_Segment, ...], parts: tuple[str, ...], i: int, j: int
) -> Match | None:
    if i == len(route):
        return Match(0, CONFIDENCE_STATIC) if j == len(parts) else None
    segment = route[i]
    if segment.kind == "rest":
        best: Match | None = None
        for count in range(segment.minimum, len(parts) - j + 1):
            rest = _match(route, parts, i + 1, j + count)
            if rest is not None and (best is None or rest.score > best.score):
                best = rest
        return (
            None
            if best is None
            else Match(best.score, min(best.confidence, CONFIDENCE_CATCH_ALL))
        )
    skipped = _match(route, parts, i + 1, j) if segment.optional else None
    if j == len(parts):
        return skipped
    part = parts[j]
    wildcard = "{}" in part
    if segment.kind == "static":
        if part == segment.text:
            weight, confidence = _STATIC, CONFIDENCE_STATIC
        elif wildcard:
            weight, confidence = _WILDCARD, CONFIDENCE_WILDCARD
        else:
            return skipped
    elif wildcard or (
        segment.pattern is not None and segment.pattern.fullmatch(part) is not None
    ):
        weight, confidence = _PARAM, CONFIDENCE_PARAM
    else:
        return skipped
    rest = _match(route, parts, i + 1, j + 1)
    taken = (
        None
        if rest is None
        else Match(rest.score + weight, min(rest.confidence, confidence))
    )
    if taken is None or (skipped is not None and skipped.score > taken.score):
        return skipped
    return taken


def allows(methods: tuple[str, ...], method: str) -> bool:
    """Принимает ли маршрут метод; пустой метод запроса — неизвестный, подходит любой."""
    if not methods or not method:
        return True
    return method in methods or (method == "HEAD" and "GET" in methods)
