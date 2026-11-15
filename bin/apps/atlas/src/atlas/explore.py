"""Команда ``explore``: ответ на «как устроено X» и «как X доходит до Y» за один вызов.

Посев из названных символов и полнотекстового поиска, подграф вокруг них, ранжирование
случайным блужданием с возвратом к посеву, цепочка вызовов между названными символами,
исходники с диска в пределах бюджета, места динамической диспетчеризации и радиус
влияния. Ранжирование, цепочка и отбор фрагментов — чистые функции; SQLite и чтение
файлов — в ``explore`` и загрузчиках.
"""

from collections import deque
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

from atlas import boundaries, query, search
from atlas.errors import AtlasError, ExitCode
from atlas.resolve import implementations as implemented

if TYPE_CHECKING:
    from collections.abc import Iterable, Mapping, Sequence
    from pathlib import Path

    from atlas.records import NodeRow
    from atlas.store.db import Store

MAX_DEPTH = 3
MAX_NODES = 200
ALPHA = 0.25
ITERATIONS = 25
SEARCH_SEEDS = 5
MAX_DOCS = 3
FULL_BODIES = 10
MAX_BOUNDARIES = 20
TOP_CALLERS = 5
MAX_SEED_DEFINITIONS = query.MAX_INLINE_CANDIDATES

_TRAVERSE_KINDS = (
    "calls",
    "instantiates",
    "references",
    "renders",
    "routes_to",
    "requests",
    "extends",
    "implements",
    "uses_trait",
)
_CALL_KINDS = frozenset({"calls", "instantiates", "renders", "routes_to", "requests"})
_CALLABLE = frozenset({"function", "method"})
_MEMBER_KINDS = frozenset({"property", "constant"})
_DOC_SCOPES = frozenset({"documentation", "agent"})


@dataclass(slots=True)
class Subgraph:
    """Окрестность посева: соседи без направления и вызовы с номерами строк."""

    nodes: set[int] = field(default_factory=set)
    adjacency: dict[int, dict[int, float]] = field(default_factory=dict)
    calls: dict[int, dict[int, list[int]]] = field(default_factory=dict)

    def link(self, source: int, target: int, kind: str, line: int) -> None:
        """Добавить ребро: вес соседства растёт с числом связей между парой."""
        for a, b in ((source, target), (target, source)):
            self.adjacency.setdefault(a, {})
            self.adjacency[a][b] = self.adjacency[a].get(b, 0.0) + 1.0
        if kind in _CALL_KINDS:
            lines = self.calls.setdefault(source, {}).setdefault(target, [])
            if line and line not in lines:
                lines.append(line)


def explore(
    store: Store,
    root: Path,
    text: str,
    *,
    synonyms: Mapping[str, Sequence[str]],
    max_files: int,
    budget: int,
) -> dict[str, Any]:
    """Подграф вокруг запроса с исходниками, цепочкой, границами и радиусом влияния.

    Returns:
        JSON-совместимый ответ; пустые разделы опущены.

    Raises:
        AtlasError: По запросу не нашлось ни одного символа (код 3).
    """
    scopes = _file_scopes(store)
    named, missing, ambiguous = _named_seeds(store, text, scopes)
    named += _word_seeds(store, text, named)
    docs: list[dict[str, Any]] = []
    found_by_search: list[NodeRow] = []
    try:
        hits = search.search(store, text, synonyms=synonyms, limit=20)["results"]
    except AtlasError:
        hits = []
    in_name = search.name_matcher(text, synonyms)
    for hit in hits:
        if hit.get("scope") in _DOC_SCOPES:
            if len(docs) < MAX_DOCS:
                docs.append(
                    {k: hit[k] for k in ("file", "title", "snippet") if k in hit}
                )
            continue
        if hit.get("scope") == "test" or len(found_by_search) >= SEARCH_SEEDS:
            continue
        # WHY: слово запроса только в комментарии или пространстве имён не делает символ
        # точкой входа: «поставщика» в докблоке ETA-сервиса уводило ответ от темы.
        if not in_name(str(hit.get("name", "")).rsplit("\\", 1)[-1]):
            continue
        path, _, lines = str(hit["file"]).rpartition(":")
        found = query.find(store, f"{path}:{lines.partition('-')[0]}")
        if (
            found
            and found[0].kind != "file"
            and found[0].end_line > found[0].start_line
        ):
            found_by_search.append(found[0])
    seeds = _unique([*named, *found_by_search])
    if not seeds:
        raise AtlasError(
            f"No symbol found for the query: {text}",
            ExitCode.NOT_FOUND,
            **({"missing_names": missing} if missing else {}),
            **({"ambiguous_names": ambiguous} if ambiguous else {}),
        )
    seed_ids = [node.id for node in seeds]
    graph = _subgraph(store, seed_ids, scopes)
    ranks = personalized_pagerank(graph.adjacency, seed_ids)
    named_ids = [node.id for node in named]
    chain_ids = longest_chain(graph.calls, named_ids)
    core = _unique_ids([*seed_ids, *chain_ids])
    nodes = store.nodes_by_ids(graph.nodes | set(core))

    result: dict[str, Any] = {"seeds": [query.summary(node) for node in seeds]}
    if missing:
        result["missing_names"] = missing
    if ambiguous:
        result["ambiguous_names"] = ambiguous
    if len(chain_ids) > 1:
        result["chain"] = _chain(chain_ids, nodes, graph)
    ordered = rank_nodes(ranks, nodes, core, scopes)
    files, more = select_fragments(ordered, core, max_files=max_files)
    rendered, omitted = _render(root, files, budget)
    result["files"] = rendered
    if more or omitted:
        result["more_files"] = sorted(set(more) | set(omitted))
    if docs:
        result["docs"] = docs
    found_boundaries = _boundaries(store, root, [nodes[i] for i in core if i in nodes])
    if found_boundaries:
        result["boundaries"] = found_boundaries
    result["impact"] = _impact(store, core, scopes)
    return result


# --- посев ----------------------------------------------------------------------------


def _named_seeds(
    store: Store, text: str, scopes: Mapping[int, str]
) -> tuple[list[NodeRow], list[str], list[dict[str, Any]]]:
    """Определения имён из кода, названных в запросе; тесты — только если других нет.

    Returns:
        Найденные узлы, ненайденные имена и слишком многозначные имена с числом определений.
    """
    seeds: list[NodeRow] = []
    missing: list[str] = []
    ambiguous: list[dict[str, Any]] = []
    for token in dict.fromkeys(search.IDENTIFIER.findall(text)):
        if not search.is_code_like(token):
            continue
        found = [node for node in query.find(store, token) if node.kind != "file"]
        own = [node for node in found if scopes.get(node.file_id) != "test"]
        found = own or found
        if not found:
            missing.append(token)
        elif len(found) > MAX_SEED_DEFINITIONS:
            # WHY: имя с десятком определений не выбирается угадыванием; агент уточнит его.
            ambiguous.append({"name": token, "definitions": len(found)})
        else:
            seeds.extend(found)
    return seeds, missing, ambiguous


def _word_seeds(store: Store, text: str, named: Sequence[NodeRow]) -> list[NodeRow]:
    """Обычные слова запроса как имена символов — только в файлах названных символов.

    Английское слово вопроса («check», «handle») совпадает с сотнями методов. Оно
    засчитывается, лишь если рядом определён другой символ из запроса: «PlaceOrderHandler
    handle» — это метод ``handle`` этого класса, а не любой ``handle``.

    Returns:
        Узлы с таким коротким именем из файлов названных символов.
    """
    files = {node.file_id for node in named}
    if not files:
        return []
    words = [
        token
        for token in dict.fromkeys(search.IDENTIFIER.findall(text))
        if not search.is_code_like(token)
        and token.islower()
        and len(token) >= 3
        and token not in search.STOP_WORDS
    ]
    return [
        node
        for word in words
        for node in store.nodes_by_name(word)
        if node.file_id in files
    ]


# --- подграф и ранжирование -----------------------------------------------------------


def _subgraph(
    store: Store, seeds: Sequence[int], scopes: Mapping[int, str]
) -> Subgraph:
    """Обход вширь в обе стороны от посева; тесты в подграф не входят.

    От посева дополнительно берутся его члены: класс, названный в запросе, объясняется
    своими методами.

    Returns:
        Узлы, соседство и вызовы подграфа.
    """
    graph = Subgraph(nodes=set(seeds))
    node_files = _node_files(store, seeds)
    frontier = list(seeds)
    for level in range(MAX_DEPTH):
        if not frontier or len(graph.nodes) >= MAX_NODES:
            break
        rows: list[tuple[int, int, str, int]] = []
        for offset in range(0, len(frontier), 400):
            part = frontier[offset : offset + 400]
            marks = ", ".join("?" for _ in part)
            kinds = (*_TRAVERSE_KINDS, "contains") if level == 0 else _TRAVERSE_KINDS
            kind_marks = ", ".join("?" for _ in kinds)
            rows += store.execute(
                "SELECT source, target, kind, line FROM edges "  # noqa: S608  # подставляются только знаки ?
                f"WHERE kind IN ({kind_marks}) AND (source IN ({marks}) OR "
                f"(target IN ({marks}) AND kind != 'contains')) ORDER BY source, target, line",
                [*kinds, *part, *part],
            ).fetchall()
        others = {int(s) for s, _, _, _ in rows} | {int(t) for _, t, _, _ in rows}
        node_files.update(_node_files(store, others - set(node_files)))
        added: list[int] = []
        for raw_source, raw_target, kind, line in rows:
            source, target = int(raw_source), int(raw_target)
            if any(
                scopes.get(node_files.get(n, 0)) == "test" for n in (source, target)
            ):
                continue
            for other in (source, target):
                if other not in graph.nodes and len(graph.nodes) < MAX_NODES:
                    graph.nodes.add(other)
                    added.append(other)
            if source in graph.nodes and target in graph.nodes:
                graph.link(source, target, str(kind), int(line or 0))
        frontier = added
    return graph


def personalized_pagerank(
    adjacency: Mapping[int, Mapping[int, float]],
    seeds: Sequence[int],
    *,
    alpha: float = ALPHA,
    iterations: int = ITERATIONS,
) -> dict[int, float]:
    """Случайное блуждание с возвратом к посеву с вероятностью ``alpha`` на каждом шаге.

    Узел ранжируется выше, если к нему много коротких путей от посева: посредник
    между двумя названными символами обгоняет популярную утилиту на краю подграфа.

    >>> chain = {1: {2: 1.0}, 2: {1: 1.0, 3: 1.0}, 3: {2: 1.0, 4: 1.0}, 4: {3: 1.0}}
    >>> ranks = personalized_pagerank(chain, [1])
    >>> ranks[2] > ranks[3] > ranks[4]
    True

    Returns:
        Ранг каждого узла подграфа; сумма рангов равна единице.
    """
    restart = {seed: 1.0 / len(seeds) for seed in seeds} if seeds else {}
    nodes = set(adjacency) | set(restart)
    rank = dict.fromkeys(nodes, 0.0)
    rank.update(restart)
    for _ in range(iterations):
        following = {node: alpha * restart.get(node, 0.0) for node in nodes}
        for node, value in rank.items():
            neighbors = adjacency.get(node)
            if not neighbors:
                # WHY: из тупика блуждание возвращается к посеву, а не теряет вес.
                for seed, share in restart.items():
                    following[seed] += (1 - alpha) * value * share
                continue
            total = sum(neighbors.values())
            for other, weight in neighbors.items():
                following[other] += (1 - alpha) * value * weight / total
        rank = following
    return rank


def longest_chain(
    calls: Mapping[int, Mapping[int, Sequence[int]]], named: Sequence[int]
) -> list[int]:
    """Самый длинный из кратчайших путей вызовов между двумя названными символами.

    Кратчайший путь не петляет, а самый длинный из них покрывает больше всего потока
    между тем, что агент назвал. Порядок пар — порядок имён в запросе.

    >>> longest_chain({1: {2: [5]}, 2: {3: [9]}}, [1, 3])
    [1, 2, 3]

    Returns:
        Узлы цепочки от начала к концу; пустой список, если пути нет.
    """
    best: list[int] = []
    for start in named:
        previous = _shortest_paths(calls, start)
        for end in named:
            if end == start or end not in previous:
                continue
            path = [end]
            while path[-1] != start:
                path.append(previous[path[-1]])
            path.reverse()
            if len(path) > len(best):
                best = path
    return best


def _shortest_paths(
    calls: Mapping[int, Mapping[int, Sequence[int]]], start: int
) -> dict[int, int]:
    previous: dict[int, int] = {start: start}
    queue = deque([start])
    while queue:
        current = queue.popleft()
        for target in sorted(calls.get(current, {})):
            if target not in previous:
                previous[target] = current
                queue.append(target)
    return previous


def rank_nodes(
    ranks: Mapping[int, float],
    nodes: Mapping[int, NodeRow],
    core: Sequence[int],
    scopes: Mapping[int, str],
) -> list[NodeRow]:
    """Узлы подграфа по убыванию значимости: ядро первым, затем по рангу.

    Файлы-узлы и тесты в отрывки не идут: файл целиком не нужен, а тест — не устройство.
    Свойства и константы вне ядра тоже: их видно в теле класса и конструктора.

    Returns:
        Узлы в порядке показа.
    """
    position = {node_id: index for index, node_id in enumerate(core)}
    candidates = [
        node
        for node in nodes.values()
        if node.kind != "file"
        and scopes.get(node.file_id) != "test"
        and (node.kind not in _MEMBER_KINDS or node.id in position)
    ]
    return sorted(
        candidates,
        key=lambda n: (
            position.get(n.id, len(position)),
            -ranks.get(n.id, 0.0),
            n.path,
            n.start_line,
        ),
    )


def select_fragments(
    ordered: Sequence[NodeRow], core: Sequence[int], *, max_files: int
) -> tuple[dict[str, list[tuple[NodeRow, bool]]], list[str]]:
    """Отобрать файлы и символы для показа: тело целиком или только сигнатура.

    Ядро и первые ``FULL_BODIES`` вызываемых символов идут с телом, остальные — сигнатурой.
    Классы — сигнатурой: их содержание показывают методы.

    Returns:
        Символы по файлам в порядке значимости файлов и пути файлов сверх ``max_files``.
    """
    core_ids = set(core)
    files: dict[str, list[tuple[NodeRow, bool]]] = {}
    more: list[str] = []
    bodies = 0
    for node in ordered:
        if node.path not in files:
            if len(files) >= max_files:
                if node.path not in more:
                    more.append(node.path)
                continue
            files[node.path] = []
        full = node.kind in _CALLABLE and (node.id in core_ids or bodies < FULL_BODIES)
        if full and node.id not in core_ids:
            bodies += 1
        files[node.path].append((node, full))
    return files, more


# --- вывод ----------------------------------------------------------------------------


def _chain(
    chain_ids: Sequence[int], nodes: Mapping[int, NodeRow], graph: Subgraph
) -> list[dict[str, Any]]:
    steps: list[dict[str, Any]] = []
    for index, node_id in enumerate(chain_ids):
        node = nodes.get(node_id)
        if node is None:
            continue
        step: dict[str, Any] = {
            "name": node.qualified_name,
            "file": query.location(node.path, node.start_line, node.end_line),
        }
        if index:
            lines = graph.calls.get(chain_ids[index - 1], {}).get(node_id, [])
            if lines:
                step["at"] = sorted(lines)
        steps.append(step)
    return steps


def _render(
    root: Path,
    files: Mapping[str, Sequence[tuple[NodeRow, bool]]],
    budget: int,
) -> tuple[list[dict[str, Any]], list[str]]:
    """Исходники с диска в момент ответа; сверх бюджета тела сворачиваются до сигнатур.

    Returns:
        Файлы с отрывками и пути файлов, которые не поместились в бюджет вовсе.
    """
    rendered: list[dict[str, Any]] = []
    omitted: list[str] = []
    spent = 0
    for path, chosen in files.items():
        try:
            lines = (
                (root / path).read_text(encoding="utf-8", errors="replace").splitlines()
            )
        except OSError:
            omitted.append(path)
            continue
        shown: dict[int, str] = {}
        symbols: list[str] = []
        collapsed = False
        for node, full in sorted(chosen, key=lambda item: item[0].start_line):
            end = min(node.end_line, len(lines))
            body = (
                {n: lines[n - 1] for n in range(node.start_line, end + 1)}
                if full
                else {}
            )
            size = sum(len(text) + 8 for text in body.values())
            if full and spent + size <= budget:
                shown.update(body)
                spent += size
            else:
                signature = node.signature or (
                    lines[node.start_line - 1].strip()
                    if node.start_line <= len(lines)
                    else ""
                )
                if spent + len(signature) + 8 > budget:
                    collapsed = True
                    continue
                shown.setdefault(node.start_line, signature)
                spent += len(signature) + 8
                collapsed = collapsed or full
            symbols.append(node.name)
        if not shown:
            omitted.append(path)
            continue
        entry: dict[str, Any] = {
            "file": path,
            "symbols": list(dict.fromkeys(symbols)),
            "code": _numbered(shown),
        }
        if collapsed:
            entry["collapsed"] = True
        rendered.append(entry)
    return rendered, omitted


def _numbered(shown: Mapping[int, str]) -> str:
    """Строки с номерами, как их показывает чтение файла; пропуск строк — «…»."""
    out: list[str] = []
    previous = 0
    for number in sorted(shown):
        if previous and number > previous + 1:
            out.append("…")
        out.append(f"{number}\t{shown[number]}")
        previous = number
    return "\n".join(out)


def _boundaries(
    store: Store, root: Path, core: Sequence[NodeRow]
) -> list[dict[str, Any]]:
    """Места, где путь из ядра обрывается: динамический вызов, неоднозначность, интерфейс.

    Returns:
        Места обрыва по файлам и строкам, с ключом и кандидатами, если они известны.
    """
    found: dict[tuple[str, int], dict[str, Any]] = {}
    cache: dict[str, list[str]] = {}
    for node in core:
        if node.kind not in _CALLABLE:
            continue
        if node.path not in cache:
            try:
                cache[node.path] = (
                    (root / node.path)
                    .read_text(encoding="utf-8", errors="replace")
                    .splitlines()
                )
            except OSError:
                cache[node.path] = []
        body = "\n".join(cache[node.path][node.start_line - 1 : node.end_line])
        for place in boundaries.find(body, node.language, node.start_line):
            entry: dict[str, Any] = {
                "file": query.location(node.path, place.line),
                "form": place.form,
                "code": place.code,
            }
            if place.key:
                entry["key"] = place.key
                candidates = _key_candidates(store, place.key)
                if candidates:
                    entry["candidates"] = candidates
            found.setdefault((node.path, place.line), entry)
    ids = [node.id for node in core]
    for path, line, name, candidates in _ambiguous_refs(store, ids):
        found.setdefault(
            (path, line),
            {
                "file": query.location(path, line),
                "form": "ambiguous",
                "name": name,
                "candidates": candidates,
            },
        )
    for path, line, name, candidates in _interface_calls(store, ids):
        found.setdefault(
            (path, line),
            {
                "file": query.location(path, line),
                "form": "interface",
                "name": name,
                "candidates": candidates,
            },
        )
    return [found[key] for key in sorted(found)][:MAX_BOUNDARIES]


def _key_candidates(store: Store, key: str) -> list[str]:
    """Обработчики ключа-типа: методы, чья сигнатура принимает этот тип.

    Так находится обработчик сообщения шины: ``__invoke(PlaceOrder $command)``.

    Returns:
        Места методов; пусто, если ключ не похож на имя типа.
    """
    tail = key.rsplit("\\", 1)[-1]
    if not tail[:1].isupper() or not tail.replace("_", "").isalnum():
        return []
    rows = store.execute(
        "SELECT path, start_line, end_line, signature FROM nodes "
        "WHERE kind = 'method' AND signature LIKE ? ORDER BY path, start_line LIMIT 50",
        (f"%{tail}%",),
    ).fetchall()
    return [
        query.location(path, start, end)
        for path, start, end, signature in rows
        if _mentions(str(signature), tail)
    ][: query.MAX_INLINE_CANDIDATES]


def _mentions(signature: str, name: str) -> bool:
    arguments = signature.partition("(")[2]
    return any(
        part.strip("?\\ ").rsplit("\\", 1)[-1] == name
        for part in arguments.replace(",", " ").split()
    )


def _ambiguous_refs(
    store: Store, ids: Sequence[int]
) -> Iterable[tuple[str, int, str, list[str]]]:
    """Неоднозначные ссылки ядра: одноимённых целей несколько, выбор не угадывается."""
    marks = ", ".join("?" for _ in ids)
    rows = store.execute(
        "SELECT f.path, r.line, r.name, r.candidates FROM refs r "  # noqa: S608  # подставляются только знаки ?
        f"JOIN files f ON f.id = r.file_id WHERE r.source IN ({marks}) AND r.status = 'ambiguous' "
        "ORDER BY f.path, r.line",
        list(ids),
    ).fetchall()
    for path, line, name, raw in rows:
        yield str(path), int(line), str(name), query.candidate_files(store, raw or "[]")


def _interface_calls(
    store: Store, ids: Sequence[int]
) -> Iterable[tuple[str, int, str, list[str]]]:
    """Вызовы метода интерфейса, у которого больше одной реализации в проекте."""
    marks = ", ".join("?" for _ in ids)
    rows = store.execute(
        "SELECT s.path, e.line, m.qualified_name, m.name, i.id, i.language FROM edges e "  # noqa: S608  # подставляются только знаки ?
        "JOIN nodes s ON s.id = e.source JOIN nodes m ON m.id = e.target AND m.kind = 'method' "
        "JOIN edges c ON c.target = m.id AND c.kind = 'contains' "
        "JOIN nodes i ON i.id = c.source AND i.kind = 'interface' "
        f"WHERE e.kind = 'calls' AND e.source IN ({marks}) ORDER BY s.path, e.line",
        list(ids),
    ).fetchall()
    for path, line, qualified, method, interface_id, language in rows:
        implementations = [
            query.location(p, start, end)
            for _, p, start, end in implemented.implementing_methods(
                store, int(interface_id), str(method), str(language)
            )
        ]
        if len(implementations) > 1:
            yield (
                str(path),
                int(line),
                str(qualified),
                implementations[: query.MAX_INLINE_CANDIDATES],
            )


def _impact(
    store: Store, core: Sequence[int], scopes: Mapping[int, str]
) -> dict[str, Any]:
    """Сколько символов и файлов зависит от ядра на два уровня вверх, и главные вызывающие.

    Returns:
        Число зависимых символов, файлов, тестов и первые вызывающие.
    """
    seen = set(core)
    frontier = list(core)
    direct: dict[int, int] = {}
    for level in range(2):
        found: list[int] = []
        for offset in range(0, len(frontier), 400):
            part = frontier[offset : offset + 400]
            marks = ", ".join("?" for _ in part)
            sql = f"SELECT source FROM edges WHERE target IN ({marks}) AND kind != 'contains'"  # noqa: S608  # подставляются только знаки ?
            for (raw,) in store.execute(sql, part):
                source = int(raw)
                if level == 0 and source not in core:
                    direct[source] = direct.get(source, 0) + 1
                if source not in seen:
                    seen.add(source)
                    found.append(source)
        frontier = found
    dependents = seen - set(core)
    files = _node_files(store, dependents)
    top = sorted(direct, key=lambda n: (-direct[n], n))[:TOP_CALLERS]
    callers = store.nodes_by_ids(top)
    counts = {
        "symbols": len(dependents),
        "files": len(set(files.values())),
        "tests": len({f for f in files.values() if scopes.get(f) == "test"}),
    }
    # WHY: «symbols: 0» — сведение (символ можно менять свободно), остальные нули — нет.
    impact: dict[str, Any] = {
        key: value for key, value in counts.items() if value or key == "symbols"
    }
    if top:
        impact["top_callers"] = [query.summary(callers[n]) for n in top if n in callers]
    return impact


# --- загрузка -------------------------------------------------------------------------


def _file_scopes(store: Store) -> dict[int, str]:
    return {int(i): str(s) for i, s in store.execute("SELECT id, scope FROM files")}


def _node_files(store: Store, ids: Iterable[int]) -> dict[int, int]:
    batch = sorted(set(ids))
    result: dict[int, int] = {}
    for offset in range(0, len(batch), 900):
        part = batch[offset : offset + 900]
        marks = ", ".join("?" for _ in part)
        sql = f"SELECT id, file_id FROM nodes WHERE id IN ({marks})"  # noqa: S608  # подставляются только знаки ?
        result.update((int(n), int(f)) for n, f in store.execute(sql, part))
    return result


def _unique(nodes: Iterable[NodeRow]) -> list[NodeRow]:
    return list({node.id: node for node in nodes}.values())


def _unique_ids(ids: Iterable[int]) -> list[int]:
    return list(dict.fromkeys(ids))
