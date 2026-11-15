"""Полнотекстовый поиск по коду и документации.

FTS5 отбирает кандидатов: по отдельному ``MATCH`` на каждое слово запроса, выше те, где
совпало больше слов, затем bm25. Окончательный порядок считается здесь по полям записи.
Ответ честно говорит, каких слов нет нигде в проекте и сколько слов совпало у каждой записи.
"""

import json
import math
import re
import zlib
from dataclasses import dataclass
from pathlib import PurePosixPath
from typing import TYPE_CHECKING, Any, NotRequired, TypedDict

from atlas import lang
from atlas.errors import AtlasError
from atlas.query import location
from atlas.text import normalize

if TYPE_CHECKING:
    from collections.abc import Callable, Mapping, Sequence

    from atlas.store.db import Store

CANDIDATE_LIMIT = 300
PER_FILE = 2
PREFIX_MIN_LENGTH = 4
BUILTIN_SCOPES = ("source", "test", "documentation", "agent")
# Виды записей — виды узлов графа, как в ``node``; ``section`` — раздел Markdown.
KINDS = (
    "class",
    "interface",
    "trait",
    "enum",
    "type",
    "function",
    "method",
    "file",
    "section",
)
# Места совпадения от сильного к слабому: имя из кода целиком, имя символа или заголовок,
# путь, импорты файла, текст.
MATCH_ORDER = ("exact", "name", "path", "links", "text")
_MATCH_FIELD = {
    "title": "name",
    "symbols": "name",
    "path": "path",
    "relations": "links",
    "content": "text",
}

STOP_WORDS = frozenset(
    {
        "а",
        "без",
        "в",
        "во",
        "где",
        "для",
        "до",
        "и",
        "из",
        "или",
        "как",
        "какая",
        "какие",
        "какой",
        "кто",
        "куда",
        "ли",
        "на",
        "о",
        "об",
        "от",
        "по",
        "при",
        "про",
        "с",
        "со",
        "что",
        "это",
        "через",
        "между",
        "после",
        "перед",
        "над",
        "под",
        "к",
        "ко",
        "у",
        "за",
        "же",
        "бы",
        "не",
        "нет",
        "есть",
        "был",
        "была",
        "было",
        "будет",
        "можно",
        "нужно",
        "надо",
        "этот",
        "эта",
        "эти",
        "тот",
        "та",
        "те",
        "все",
        "всё",
        "так",
        "там",
        "тут",
        "если",
        "когда",
        "чтобы",
        "также",
        "только",
        "уже",
        "ещё",
        "еще",
        "мы",
        "вы",
        "он",
        "она",
        "они",
        "его",
        "её",
        "их",
        "происходит",
        "работает",
        "устроен",
        "устроено",
        "устроена",
        "сделан",
        "сделано",
        # Глаголы вопроса «как X доходит до Y»: в коде их нет, а по редкости они весят
        # больше имён предметной области и поднимают случайные комментарии
        # («доходит» → «доход»).
        "доходит",
        "доходят",
        "попадает",
        "попадают",
        "приходит",
        "приходят",
        "проходит",
        "проходят",
        "идёт",
        "идет",
        "идут",
        "работают",
        "устроены",
        "происходят",
        "вызывается",
        "вызываются",
        "используется",
        "используются",
        "связан",
        "связана",
        "связано",
        "связаны",
        "a",
        "an",
        "and",
        "do",
        "does",
        "for",
        "from",
        "how",
        "in",
        "is",
        "of",
        "on",
        "or",
        "the",
        "to",
        "what",
        "where",
        "which",
        "who",
        "with",
    }
)
_FIELD_WEIGHTS_PHRASE = {
    "title": 120,
    "path": 90,
    "symbols": 110,
    "relations": 45,
    "content": 25,
}
_FIELD_WEIGHTS_TERM = {
    "title": 24,
    "path": 18,
    "symbols": 22,
    "relations": 9,
    "content": 5,
}
# Документы выше кода, но умеренно: при +120 большие документы, где упомянуто всё
# (docs/api-platform.md), вытесняли код из ответов на вопросы. Подобрано на стенде bench/.
_SCOPE_BONUS = {"agent": 60, "documentation": 60, "source": 10, "test": 10}
IDENTIFIER = re.compile(r"[A-Za-z_$][\w$]*(?:(?:\\|::|\.)[A-Za-z_$][\w$]*)*")


@dataclass(frozen=True, slots=True)
class _Group:
    """Слово запроса и его варианты: само слово и синонимы из ``.atlas.json``."""

    word: str
    variants: tuple[str, ...]


class Candidate(TypedDict):
    """Типизированный фрагмент из полнотекстового индекса."""

    kind: str
    scope: str
    path: str
    start_line: int
    end_line: int
    title: str
    symbols: list[str]
    content: str
    search_relations: str
    search_content: str
    generated: bool


class Score(TypedDict):
    """Чистый результат ранжирования фрагмента."""

    score: int
    missed: list[str]
    coverage: float
    matched_in: list[str]
    exact: NotRequired[bool]


class SearchHit(Score):
    """Результат поиска для JSON-ответа."""

    kind: str
    scope: str
    path: str
    start_line: int
    end_line: int
    title: str
    symbols: list[str]
    snippet: str
    generated: bool


def search(
    store: Store,
    text: str,
    *,
    synonyms: Mapping[str, Sequence[str]],
    limit: int,
    kinds: Sequence[str] = (),
    scopes: Sequence[str] = (),
    files_only: bool = False,
    custom_scopes: Sequence[str] = (),
) -> dict[str, Any]:
    """Найти записи по запросу; с ``files_only`` — только файлы, лучшие первыми.

    Returns:
        JSON-совместимый ответ с результатами и сигналами полноты.

    Raises:
        AtlasError: В запросе нет значимых слов или фильтр назван неизвестным значением.
    """
    _check_filters(kinds, scopes, custom_scopes)
    normalized = normalize(text)
    groups = _groups(normalized, synonyms, text)
    if not groups:
        raise AtlasError(f"No meaningful words in the query: {text!r}")
    limit = max(1, min(500 if files_only else 50, limit))
    expressions = [_match_expression(group) for group in groups]
    frequencies = [
        int(
            store.execute(
                "SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH ?", (e,)
            ).fetchone()[0]
        )
        for e in expressions
    ]
    missing = [
        group.word
        for group, count in zip(groups, frequencies, strict=True)
        if count == 0
    ]
    weights = _weights(store, frequencies)
    candidates, total = _candidates(
        store, expressions, weights, kinds, scopes or BUILTIN_SCOPES
    )

    identifiers = [
        token
        for token in text.split()
        if IDENTIFIER.fullmatch(token) and is_code_like(token)
    ]
    results: list[SearchHit] = []
    for row in candidates:
        fields = _fields(row)
        match = _score(fields, normalized, groups, weights, row)
        bonus, exact = _identifier_bonus(row, identifiers)
        match["score"] += bonus
        if identifiers:
            match["exact"] = exact
        if len(match["missed"]) == len(groups):
            continue
        results.append(
            {
                **match,
                "kind": row["kind"],
                "scope": row["scope"],
                "path": row["path"],
                "start_line": row["start_line"],
                "end_line": row["end_line"],
                "title": row["title"],
                "symbols": row["symbols"],
                "generated": row["generated"],
                "snippet": _snippet(
                    row["content"], [v for g in groups for v in g.variants]
                ),
            }
        )
    # WHY: имя из кода в запросе делит выдачу на ярусы. Запись с точным вхождением
    # «AclDomain» всегда выше записи, где совпали только части «acl» и «domain» — например,
    # через путь src/Domain/Acl/, который есть у любого файла каталога.
    # Сгенерированный файл уступает ручному при том же совпадении: у заглушки генератора
    # то же имя, что у реализации, но ответа на вопрос в ней нет.
    # Покрытие сравнивается точным числом. Ярусы (все слова / больше половины веса /
    # остальное, или шаг 0.1) пробовали: на стенде bench/ recall пар из git падал
    # с 0.59 до 0.51 и 0.57.
    results.sort(
        key=lambda r: (
            -int(r.get("exact", False)),
            -r["coverage"],
            r["generated"],
            -r["score"],
            r["path"],
            r["start_line"],
        )
    )
    if files_only:
        exact_hits = [r for r in results if r.get("exact")]
        files = list(dict.fromkeys(str(r["path"]) for r in (exact_hits or results)))
        meta = _meta(groups, missing, total)
        if exact_hits:
            # Точные вхождения имени нашлись: файлы, где совпали только части имени,
            # не выводятся, как у rg -l, но их число видно.
            partial = {str(r["path"]) for r in results} - set(files)
            meta["partial_files"] = len(partial)
        return {"meta": meta, "files": files[:limit]}
    limited = _limit_per_file(results, limit)
    return {
        "meta": _meta(groups, missing, total),
        "results": [_public(result) for result in limited],
    }


def _meta(groups: Sequence[_Group], missing: list[str], total: int) -> dict[str, Any]:
    """Сведения о запросе, а не записи: как понят запрос и насколько полон ответ.

    ``variants`` — только для слов, которые расширили основа, словарь или синонимы; само
    слово в своём списке не повторяется.

    Returns:
        Блок ``meta`` ответа.
    """
    meta: dict[str, Any] = {"words": [group.word for group in groups]}
    variants = {
        group.word: extra
        for group in groups
        if (extra := [v for v in group.variants if v != group.word])
    }
    if variants:
        meta["variants"] = variants
    if missing:
        meta["missing_words"] = missing
    meta["total"] = total
    return meta


def _check_filters(
    kinds: Sequence[str], scopes: Sequence[str], custom_scopes: Sequence[str]
) -> None:
    """Неизвестный вид или область — ошибка: иначе опечатка выглядит как пустая выдача."""
    known_scopes = (*BUILTIN_SCOPES, *custom_scopes)
    for scope in scopes:
        if scope not in known_scopes:
            hint = (
                f" Looks like a record kind: --kind {scope}." if scope in KINDS else ""
            )
            raise AtlasError(
                f"Unknown --scope {scope}.{hint}",
                allowed_scopes=list(known_scopes),
            )
    for kind in kinds:
        if kind not in KINDS:
            hint = (
                f" Looks like a scope: --scope {kind}." if kind in known_scopes else ""
            )
            raise AtlasError(f"Unknown --kind {kind}.{hint}", allowed_kinds=list(KINDS))


def _groups(
    normalized: str, synonyms: Mapping[str, Sequence[str]], raw: str = ""
) -> list[_Group]:
    """Слова запроса с вариантами: основа русского слова, английские термины, транслит.

    Всё это — расширение запроса; индекс не знает о русской морфологии и не должен.

    Returns:
        Значимые слова с вариантами поиска.

    """
    proper = {
        normalize(token)
        for token in raw.split()
        if re.fullmatch(r"[А-ЯЁ][а-яё]+", token)
    }
    groups: list[_Group] = []
    seen: set[tuple[str, ...]] = set()
    for term in normalized.split(" "):
        if len(term) < 2 or term in STOP_WORDS:
            continue
        candidates = [term]
        if lang.is_cyrillic(term):
            base = lang.stem(term)
            candidates = [base, *lang.english(term, base)]
            if term in proper:
                candidates.append(lang.translit(base))
            candidates += synonyms.get(base, ())
        candidates += synonyms.get(term, ())
        variants = tuple(
            v for v in dict.fromkeys(normalize(c) for c in candidates) if v
        )
        if not variants or variants in seen:
            continue
        seen.add(variants)
        groups.append(_Group(term, variants))
    return groups


def _weights(store: Store, frequencies: Sequence[int]) -> list[float]:
    """Вес слова запроса по редкости, от 0 до 1: частое «api» весит меньше редкого «прокс».

    Без весов запись, где совпали три общих слова, обгоняет запись с двумя редкими, и
    на вопрос по-русски первыми идут документы, где просто много слов «admin» и «api».

    Returns:
        Нормализованные веса редкости в порядке терминов.

    """
    total = int(store.execute("SELECT count(*) FROM chunks").fetchone()[0]) or 1
    idf = [
        math.log((total + 1) / (count + 1)) + 0.1 if count else 0.0
        for count in frequencies
    ]
    top = max(idf) or 1.0
    return [value / top for value in idf]


def _match_expression(group: _Group) -> str:
    """Выражение FTS5 для группы: варианты через OR, длинные — по префиксу."""
    parts = []
    for variant in group.variants:
        quoted = '"' + variant.replace('"', '""') + '"'
        parts.append(quoted + "*" if len(variant) >= PREFIX_MIN_LENGTH else quoted)
    return " OR ".join(parts)


def _candidates(
    store: Store,
    expressions: list[str],
    weights: Sequence[float],
    kinds: Sequence[str],
    scopes: Sequence[str],
) -> tuple[list[Candidate], int]:
    """Кандидаты: сначала больший суммарный вес совпавших слов, затем bm25."""
    union = " UNION ALL ".join(
        "SELECT rowid AS id, rank, ? AS weight FROM chunks_fts WHERE chunks_fts MATCH ?"
        for _ in expressions
    )
    union_params: list[Any] = [
        value for pair in zip(weights, expressions, strict=True) for value in pair
    ]
    filters = [f"f.scope IN ({', '.join('?' for _ in scopes)})"]
    params: list[Any] = [*union_params, *scopes]
    if kinds:
        filters.append(f"c.kind IN ({', '.join('?' for _ in kinds)})")
        params.extend(kinds)
    where = " AND ".join(filters)
    hits = (
        f"SELECT id, sum(weight) AS hits, sum(rank) AS rank FROM ({union}) GROUP BY id"  # noqa: S608  # фрагменты из литералов
    )
    # WHY: материализуем только id и веса. COUNT и выдача используют один набор,
    # а тексты и JSON читаются лишь для первых CANDIDATE_LIMIT фрагментов.
    rows = store.execute(
        f"WITH hits AS MATERIALIZED ({hits}), filtered AS MATERIALIZED ("  # noqa: S608  # SQL-фрагменты — литералы, значения переданы параметрами
        f"SELECT h.id, h.hits, h.rank FROM hits h JOIN chunks c ON c.id=h.id "
        f"JOIN files f ON f.id=c.file_id WHERE {where}) "
        "SELECT c.kind, f.scope, f.path, c.start_line, c.end_line, c.title, c.symbols, "
        "c.content, f.search_relations, c.search_content, f.generated, "
        "(SELECT count(*) FROM filtered) "
        "FROM (SELECT * FROM filtered ORDER BY hits DESC, rank ASC, id LIMIT ?) h "
        "JOIN chunks c ON c.id=h.id JOIN files f ON f.id=c.file_id "
        "ORDER BY h.hits DESC, h.rank ASC, h.id",
        [*params, CANDIDATE_LIMIT],
    ).fetchall()
    candidates: list[Candidate] = [
        {
            "kind": kind,
            "scope": scope,
            "path": path,
            "start_line": start,
            "end_line": end,
            "title": title,
            "symbols": json.loads(symbols),
            "content": zlib.decompress(content).decode("utf-8", errors="replace"),
            "search_relations": search_relations,
            "search_content": zlib.decompress(search_content).decode("utf-8"),
            "generated": bool(generated),
        }
        for (
            kind,
            scope,
            path,
            start,
            end,
            title,
            symbols,
            content,
            search_relations,
            search_content,
            generated,
            _total,
        ) in rows
    ]
    return candidates, int(rows[0][-1]) if rows else 0


def _fields(row: Candidate) -> dict[str, str]:
    return {
        "title": normalize(str(row["title"])),
        "path": normalize(str(row["path"])),
        "symbols": normalize(" ".join(row["symbols"])),
        "relations": row["search_relations"],
        "content": row["search_content"],
    }


def _score(
    fields: Mapping[str, str],
    query: str,
    groups: Sequence[_Group],
    weights: Sequence[float],
    row: Candidate,
) -> Score:
    """Очки записи: фраза целиком, каждое слово с весом его редкости, бонусы пути и области.

    ``coverage`` — доля суммарного веса слов запроса, найденных в записи: главный ключ
    порядка. ``missed`` называет слова запроса, которых в записи нет.

    Returns:
        Оценка фрагмента и покрытие запроса.

    """
    score = 0.0
    matched_in: list[str] = []
    for name, value in fields.items():
        if value == query:
            score += _FIELD_WEIGHTS_PHRASE[name] * 2
            matched_in.append(name)
        elif query in value:
            score += _FIELD_WEIGHTS_PHRASE[name]
            matched_in.append(name)
    missed: list[str] = []
    matched_weight = 0.0
    for group, weight in zip(groups, weights, strict=True):
        pattern = _term_pattern(group.variants)
        group_matched = False
        for name, value in fields.items():
            if pattern.search(value):
                score += _FIELD_WEIGHTS_TERM[name] * (0.25 + weight)
                matched_in.append(name)
                group_matched = True
        if group_matched:
            matched_weight += weight
        else:
            missed.append(group.word)
    if normalize(PurePosixPath(str(row["path"])).stem) == query:
        score += 300
    if fields["title"].endswith(query):
        score += 160
    score += _SCOPE_BONUS.get(str(row["scope"]), 0)
    total_weight = sum(weights) or 1.0
    return {
        "score": int(score),
        "missed": missed,
        "coverage": round(matched_weight / total_weight, 2),
        "matched_in": list(dict.fromkeys(matched_in)),
    }


def name_matcher(
    text: str, synonyms: Mapping[str, Sequence[str]]
) -> Callable[[str], bool]:
    r"""Проверка «слово запроса есть в имени», с теми же вариантами, что у поиска.

    >>> in_name = name_matcher("список поставщиков", {"поставщик": ["supplier"]})
    >>> in_name("ListSuppliersHandler::handle"), in_name("EtaService::calculate")
    (True, False)

    Returns:
        Функция от короткого имени записи.
    """
    patterns = [
        _term_pattern(group.variants)
        for group in _groups(normalize(text), synonyms, text)
    ]

    def matches(name: str) -> bool:
        value = normalize(name)
        return any(pattern.search(value) for pattern in patterns)

    return matches


def is_code_like(token: str) -> bool:
    """Токен похож на имя из кода: camelCase, PascalCase, snake_case или с разделителем."""
    return bool(re.search(r"[a-z][A-Z]|_|\\|::|\.", token)) or (
        token[:1].isupper() and len(token) > 3
    )


def _identifier_bonus(row: Candidate, identifiers: Sequence[str]) -> tuple[int, bool]:
    """Бонус и признак точного вхождения имени из запроса в запись.

    ``AclDomain`` в запросе должен поднять класс ``AclDomain`` и места его использования
    выше класса ``SameDomainAcl``, у которого совпадают те же слова в другом порядке.

    Returns:
        Бонус и признак точного совпадения идентификатора.

    """
    bonus = 0
    exact = False
    for identifier in identifiers:
        tail = re.split(r"\\|::|\.", identifier)[-1]
        if str(row["title"]).endswith(identifier) or tail in row["symbols"]:
            bonus += 300
            exact = True
        elif identifier in str(row["title"]):
            bonus += 150
            exact = True
        if re.search(
            rf"(?<![\w$]){re.escape(identifier)}(?![\w$])", str(row["content"])
        ):
            bonus += 120
            exact = True
    return bonus, exact


def _term_pattern(variants: Sequence[str]) -> re.Pattern[str]:
    alternatives = [
        re.escape(v) + ("" if len(v) >= PREFIX_MIN_LENGTH else r"(?= |$)")
        for v in variants
    ]
    return re.compile(r"(?:^| )(?:" + "|".join(alternatives) + ")")


def _snippet(content: str, variants: Sequence[str]) -> str:
    lines = content.splitlines()
    for line in lines:
        normalized = normalize(line)
        if any(variant in normalized for variant in variants):
            return line.strip()[:240]
    return (lines[0].strip() if lines else "")[:240]


def _public(result: SearchHit) -> dict[str, Any]:
    """Запись ответа: что открыть, что это, затем оценки; пустое и подразумеваемое опущено."""
    path = str(result["path"])
    public: dict[str, Any] = {
        "file": location(path, int(result["start_line"]), int(result["end_line"])),
        "kind": result["kind"],
    }
    title = _short_title(str(result["title"]), path)
    if title:
        public["title" if result["kind"] == "section" else "name"] = title
    public["snippet"] = result["snippet"]
    public["score"] = result["score"]
    public["match"] = _strongest(result)
    if result["missed"]:
        public["missed"] = result["missed"]
        public["coverage"] = result["coverage"]
    if result["scope"] != "source":
        public["scope"] = result["scope"]
    if result["generated"]:
        public["generated"] = True
    return public


def _strongest(result: SearchHit) -> str:
    """Самое сильное место совпадения записи по ``MATCH_ORDER``."""
    if result.get("exact"):
        return "exact"
    found = {_MATCH_FIELD[name] for name in result["matched_in"]}
    return next((place for place in MATCH_ORDER if place in found), "text")


def _short_title(title: str, path: str) -> str | None:
    r"""Заголовок без того, что уже есть в пути.

    >>> _short_title("App\\Acl\\AclRegistry::get", "src/Acl/AclRegistry.php")
    'AclRegistry::get'
    >>> _short_title("docs/acl.md", "docs/acl.md") is None
    True

    Returns:
        Короткий заголовок или ``None``, если заголовок совпадает с путём.
    """
    if title == path:
        return None
    short = title.rsplit("\\", 1)[-1]
    # WHY: пространство имён повторяет каталоги пути; короткое имя однозначно рядом с путём.
    return short if short.split("::", 1)[0] in path else title


def _limit_per_file(results: list[SearchHit], limit: int) -> list[SearchHit]:
    limited: list[SearchHit] = []
    per_file: dict[str, int] = {}
    for result in results:
        path = str(result["path"])
        if per_file.get(path, 0) >= PER_FILE:
            continue
        limited.append(result)
        per_file[path] = per_file.get(path, 0) + 1
        if len(limited) == limit:
            break
    return limited
