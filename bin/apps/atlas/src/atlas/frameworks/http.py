"""HTTP-мост: вызов с путём в TS/JS → маршрут, который этот путь обслуживает.

Запросом считается вызов ``fetch``, ``axios.get``, обёртки вида ``getJson`` или
``postNoContent`` и функции из ``bridges.http_wrappers``. Навигация (``router.push``,
``redirect``) под это правило не попадает.

Маршрут ищется во всех кодовых базах, побеждает самый специфичный: литерал сильнее
параметра, параметр сильнее catch-all. Два лучших с одним весом — ребра нет: неверная
связь хуже отсутствующей. Мост из ``bridges.http`` сужает поиск до контура назначения и
переписывает путь.

Исключение — ничья из-за вставки: ``${active ? 'activate' : 'deactivate'}`` одинаково
совпадает с обоими маршрутами, и код действительно ходит в оба. Если кандидатов не больше
``MAX_GUESSES``, мост связывает вызов с каждым, но как догадку.
"""

import re
from dataclasses import dataclass
from typing import TYPE_CHECKING

from atlas.frameworks.routes import Match, allows, compile_path, match

if TYPE_CHECKING:
    from collections.abc import Iterable, Sequence

    from atlas.config import HttpBridge

# WHY: регистр глагола любой, но следующая буква строчная — это другое слово: getaway, sender.
_REQUEST_CALLEE = re.compile(
    r"^(?i:fetch|request|get|post|put|patch|delete|head|download|upload|send)(?![a-z])"
)
_METHOD_PREFIX = re.compile(r"^(?i:(get|post|put|patch|delete))(?![a-z])")
MAX_GUESSES = 3
GUESS_CONFIDENCE = 0.5


@dataclass(frozen=True, slots=True)
class Call:
    """Вызов с путём из таблицы ``http_calls``."""

    source: int
    file_id: int
    codebase: str
    callee: str
    method: str
    path: str
    line: int
    col: int


@dataclass(frozen=True, slots=True)
class Endpoint:
    """Маршрут-кандидат: id узла и то, что нужно для сопоставления."""

    node: int
    path: str
    methods: tuple[str, ...]
    codebase: str
    apps: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class Link:
    """Найденное ребро ``requests``."""

    call: Call
    endpoint: Endpoint
    confidence: float
    via: str
    guess: bool = False


@dataclass(frozen=True, slots=True)
class BridgeReport:
    """Итог моста: рёбра, число вызовов со связью-догадкой и без однозначного маршрута."""

    links: tuple[Link, ...]
    guessed: int
    ambiguous: int
    unmatched: int


def is_request(callee: str, wrappers: Iterable[str] = ()) -> bool:
    """Похож ли вызов на HTTP-запрос по имени функции."""
    tail = callee.rpartition(".")[2]
    known = set(wrappers)
    return callee in known or tail in known or _REQUEST_CALLEE.match(tail) is not None


def method_of(call: Call) -> str:
    """Метод HTTP: из опций вызова, иначе из имени обёртки; пусто — неизвестен."""
    if call.method:
        return call.method
    found = _METHOD_PREFIX.match(call.callee.rpartition(".")[2])
    return found.group(1).upper() if found is not None else ""


def link(
    calls: Iterable[Call],
    endpoints: Sequence[Endpoint],
    bridges: Sequence[HttpBridge] = (),
    wrappers: Iterable[str] = (),
) -> BridgeReport:
    """Сопоставить вызовы с маршрутами."""
    wrappers = tuple(wrappers)
    compiled = [(endpoint, compile_path(endpoint.path)) for endpoint in endpoints]
    links: list[Link] = []
    guessed = ambiguous = unmatched = 0
    for call in calls:
        if not is_request(call.callee, wrappers):
            continue
        path, app, via = _bridged(call, bridges)
        method = method_of(call)
        best: list[tuple[Endpoint, Match]] = []
        for endpoint, segments in compiled:
            if app is not None and app not in endpoint.apps:
                continue
            if not allows(endpoint.methods, method):
                continue
            found = match(segments, path)
            if found is None:
                continue
            if not best or found.score > best[0][1].score:
                best = [(endpoint, found)]
            elif found.score == best[0][1].score:
                best.append((endpoint, found))
        if not method and len(best) > 1:
            # WHY: метод без литерала в опциях чаще всего GET — это умолчание fetch; из
            # маршрутов, различающихся только методом, выбирается GET.
            best = [item for item in best if "GET" in item[0].methods] or best
        nodes = {endpoint.node for endpoint, _ in best}
        if not best:
            unmatched += 1
        elif len(nodes) > 1 and "{}" in call.path and len(nodes) <= MAX_GUESSES:
            guessed += 1
            links.extend(
                Link(
                    call,
                    endpoint,
                    GUESS_CONFIDENCE,
                    "http.template-candidates",
                    guess=True,
                )
                for endpoint, _ in best
            )
        elif len(nodes) > 1:
            ambiguous += 1
        else:
            endpoint, found = best[0]
            kind = via or ("http.template" if "{}" in call.path else "http.literal")
            links.append(Link(call, endpoint, found.confidence, kind))
    return BridgeReport(tuple(links), guessed, ambiguous, unmatched)


def _bridged(call: Call, bridges: Sequence[HttpBridge]) -> tuple[str, str | None, str]:
    """Путь после моста, контур назначения и правило; без моста — путь как есть."""
    for bridge in bridges:
        if call.codebase == bridge.from_codebase and call.path.startswith(
            bridge.path_prefix
        ):
            path = call.path
            if bridge.rewrite_from is not None and bridge.rewrite_to is not None:
                path = re.sub(bridge.rewrite_from, bridge.rewrite_to, path, count=1)
            return path, bridge.to_app, "http.bridge"
    return call.path, None, ""
