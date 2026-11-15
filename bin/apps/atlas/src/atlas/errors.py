"""Ошибки, которые CLI превращает в JSON-ответ и код выхода."""

from enum import IntEnum


class ExitCode(IntEnum):
    """Коды выхода CLI, общие для всех команд."""

    OK = 0
    ERROR = 1
    NO_INDEX = 2
    NOT_FOUND = 3
    AMBIGUOUS = 4


class AtlasError(Exception):
    """Ошибка, о которой агент должен узнать из JSON, а не из трассировки.

    Несёт код выхода и дополнительные поля ответа: список просмотренных каталогов,
    кандидатов неоднозначного имени и тому подобное.
    """

    def __init__(
        self, message: str, code: ExitCode = ExitCode.ERROR, **details: object
    ) -> None:
        super().__init__(message)
        self.code = code
        self.details = details
