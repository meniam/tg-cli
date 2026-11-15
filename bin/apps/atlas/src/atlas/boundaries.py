"""Места, где статический путь обрывается: цель вызова выбирается во время работы.

Вычисляемый вызов метода, ``getattr``, шина сообщений, контейнер, динамический импорт.
Ребро в такое место не угадывается: неверная связь хуже отсутствующей. Вместо этого
ответ называет место обрыва, форму вызова и ключ, если он виден в коде: строку,
``new Type`` или ``Type::class``. По ключу агент сам сужает круг кандидатов.

Поиск идёт регулярными выражениями по тексту символа, из которого вырезаны комментарии
и содержимое строк: закомментированный код и строка, похожая на вызов, не срабатывают.
Вырезание сохраняет смещения, поэтому отрывок и ключ берутся из исходного текста по тем
же позициям. Граф не меняется, всё считается во время запроса.
"""

import re
from dataclasses import dataclass

MAX_CODE = 160
_KEY_WINDOW = 160

# Форма вызова и шаблон; шаблоны применяются к тексту без комментариев и строк.
_FORMS: dict[str, tuple[tuple[str, re.Pattern[str]], ...]] = {
    "php": (
        ("bus", re.compile(r"(?:->|::)dispatch\s*\(")),
        (
            "container",
            re.compile(r"(?:container|locator)\w*\s*->\s*get\s*\(", re.IGNORECASE),
        ),
        ("dynamic_method", re.compile(r"->\s*(?:\$\w+|\{)")),
        ("dynamic_static", re.compile(r"\$\w+\s*::\s*\w+\s*\(")),
        ("dynamic_class", re.compile(r"\bnew\s+\$\w+")),
        ("callable", re.compile(r"\bcall_user_func(?:_array)?\s*\(")),
    ),
    "python": (
        ("getattr", re.compile(r"\bgetattr\s*\(")),
        ("dynamic_import", re.compile(r"\b(?:importlib\.)?import_module\s*\(")),
        ("globals", re.compile(r"\b(?:globals|locals)\s*\(\s*\)\s*\[")),
        ("bus", re.compile(r"\.(?:dispatch|emit|publish)\s*\(")),
        ("computed_call", re.compile(r"[\w\)]\s*\[[^\]\n]+\]\s*\(")),
    ),
    "go": (
        ("reflect", re.compile(r"\breflect\.(?:ValueOf|TypeOf|New)\s*\(")),
        ("method_by_name", re.compile(r"\.MethodByName\s*\(")),
        ("plugin", re.compile(r"\bplugin\.Open\s*\(")),
        ("bus", re.compile(r"\.(?:dispatch|emit|publish)\s*\(", re.IGNORECASE)),
    ),
    "ecmascript": (
        ("computed_call", re.compile(r"[\w\)]\s*\[[^\]\n]+\]\s*\(")),
        ("bus", re.compile(r"\.(?:dispatch|emit|publish)\s*\(")),
        ("dynamic_import", re.compile(r"\bimport\s*\(")),
    ),
}
_LANGUAGE_FAMILY = {
    "php": "php",
    "python": "python",
    "go": "go",
    "typescript": "ecmascript",
    "tsx": "ecmascript",
    "javascript": "ecmascript",
}
_COMMENTS = {
    "php": ("//", "#"),
    "python": ("#",),
    "go": ("//",),
    "ecmascript": ("//",),
}
_KEY = re.compile(
    r"""\bnew\s+\\?([A-Z][\w\\]*)"""
    r"""|\b([A-Z][\w\\]*)::class\b"""
    r"""|(['"`])([^'"`\n]{1,120})\3"""
)


@dataclass(frozen=True, slots=True)
class Boundary:
    """Место динамической диспетчеризации: строка, форма вызова, отрывок, ключ."""

    line: int
    form: str
    code: str
    key: str | None


def find(source: str, language: str, first_line: int = 1) -> list[Boundary]:
    """Места динамической диспетчеризации в тексте символа, по порядку строк.

    ``first_line`` — номер строки, с которой начинается ``source`` в файле.

    >>> [
    ...     b.form
    ...     for b in find(
    ...         "$this->bus->dispatch(new PlaceOrder($id)); // $x->$m()", "php"
    ...     )
    ... ]
    ['bus']
    >>> find("$this->bus->dispatch(new PlaceOrder($id));", "php")[0].key
    'PlaceOrder'

    Returns:
        Найденные места; одна строка даёт не больше одной записи.
    """
    family = _LANGUAGE_FAMILY.get(language)
    if family is None:
        return []
    stripped = strip(source, family)
    found: dict[int, Boundary] = {}
    lines = source.splitlines()
    for form, pattern in _FORMS[family]:
        for match in pattern.finditer(stripped):
            index = stripped.count("\n", 0, match.start())
            if index in found:
                continue
            found[index] = Boundary(
                line=first_line + index,
                form=form,
                code=lines[index].strip()[:MAX_CODE] if index < len(lines) else "",
                key=_key(source, match.end()),
            )
    return [found[index] for index in sorted(found)]


def strip(source: str, family: str) -> str:
    """Заменить пробелами комментарии и содержимое строк, сохранив кавычки и переводы строк.

    >>> strip("a('x // y') // z", "ecmascript")
    "a('      ')     "

    Returns:
        Текст той же длины, где комментарии и строки не видны шаблонам.
    """
    line_comments = _COMMENTS.get(family, ())
    block = family != "python"
    out = list(source)
    index = 0
    length = len(source)

    def blank(start: int, end: int) -> None:
        for position in range(start, min(end, length)):
            if out[position] != "\n":
                out[position] = " "

    while index < length:
        char = source[index]
        if block and source.startswith("/*", index):
            end = source.find("*/", index + 2)
            end = length if end < 0 else end + 2
            blank(index, end)
            index = end
        elif any(source.startswith(marker, index) for marker in line_comments):
            end = source.find("\n", index)
            end = length if end < 0 else end
            blank(index, end)
            index = end
        elif char in "'\"`":
            quote = source[index : index + 3] if family == "python" else char
            if quote not in {"'''", '"""'}:
                quote = char
            end = _string_end(source, index + len(quote), quote)
            blank(index + len(quote), end - len(quote))
            index = end
        else:
            index += 1
    return "".join(out)


def _string_end(source: str, start: int, quote: str) -> int:
    """Позиция сразу за закрывающей кавычкой; экранированная кавычка строку не закрывает."""
    index = start
    while index < len(source):
        if source[index] == "\\":
            index += 2
            continue
        if source.startswith(quote, index):
            return index + len(quote)
        if len(quote) == 1 and quote != "`" and source[index] == "\n":
            return index
        index += 1
    return len(source)


def _key(source: str, start: int) -> str | None:
    """Ключ диспетчеризации в аргументах вызова: класс сообщения или строковый литерал."""
    window = source[start : start + _KEY_WINDOW]
    depth = 0
    for index, char in enumerate(window):
        if char in "([{":
            depth += 1
        elif char in ")]}":
            if depth == 0:
                window = window[:index]
                break
            depth -= 1
    match = _KEY.search(window)
    if match is None:
        return None
    return (match.group(1) or match.group(2) or match.group(4)).lstrip("\\")
