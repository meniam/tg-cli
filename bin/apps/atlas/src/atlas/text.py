"""Нормализация текста для полнотекстового индекса и запросов.

Одна функция на обе стороны: запись поиска и запрос нормализуются одинаково, иначе
``ListSuppliersHandler`` в коде не найдётся по запросу «list suppliers».
"""

import re
import unicodedata

_CAMEL = re.compile(r"(?<=[a-zа-яё\d])(?=[A-ZА-ЯЁ])|(?<=[A-Z])(?=[A-Z][a-z])")
_NON_WORD = re.compile(r"[^\w]+|_+")
_SPACES = re.compile(r"\s+")
_LATIN_MARKS = re.compile(r"(?<=[a-z])[\u0300-\u036f]+")


def normalize(value: str) -> str:
    """Разбить camelCase, привести к нижнему регистру, снять диакритику с латиницы.

    >>> normalize("ListSuppliersHandler::handle")
    'list suppliers handler handle'
    >>> normalize("Ёлка HTTPClient Café")
    'ёлка http client cafe'

    Returns:
        Нормализованная строка для поиска.

    """
    value = _CAMEL.sub(" ", value).lower()
    if not value.isascii():
        # WHY: быстрый путь для ASCII (почти весь код): разложение Unicode на каждый
        # символ было половиной времени поиска. Диакритика снимается только после
        # латинской буквы: «й» и «ё» остаются собой.
        value = unicodedata.normalize(
            "NFC", _LATIN_MARKS.sub("", unicodedata.normalize("NFD", value))
        )
    value = _NON_WORD.sub(" ", value)
    return _SPACES.sub(" ", value).strip()
