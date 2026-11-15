"""Контракт привязки имени между извлекателями и разрешателями."""

from typing import TypedDict, cast

# WHY: функциональная запись позволяет типизировать ключ «from», совпадающий с
# ключевым словом Python. Формат сохраняет совместимость с JSON существующих ссылок.
Binding = TypedDict(
    "Binding",
    {
        "name": str,
        "from": str,
        "imported": str,
        "member": str,
        "local": bool,
        "recv": str,
        "type": "str | Binding",
        "self": bool,
        "static": bool,
        "attribute": bool,
        "ret": bool,
        "prop": str,
        "fqcn": str,
        "functions": list[str],
        "module": str,
        "names": list[str],
        "package": str,
    },
    total=False,
)


def read_binding(value: object) -> Binding | None:
    """Проверить привязку, прочитанную из произвольных метаданных JSON."""
    if not isinstance(value, dict):
        return None
    strings = {
        "name",
        "from",
        "imported",
        "member",
        "recv",
        "prop",
        "fqcn",
        "module",
        "package",
    }
    booleans = {"local", "self", "static", "attribute", "ret"}
    lists = {"functions", "names"}
    for key, item in value.items():
        if key in strings:
            valid = isinstance(item, str)
        elif key in booleans:
            valid = isinstance(item, bool)
        elif key in lists:
            valid = isinstance(item, list) and all(
                isinstance(part, str) for part in item
            )
        elif key == "type":
            valid = isinstance(item, str) or read_binding(item) is not None
        else:
            return None
        if not valid:
            return None
    return cast("Binding", value)
