"""Диспетчер языковых правил и зависимостей разрешения."""

from typing import TYPE_CHECKING

from atlas.model import Language
from atlas.resolve.common import _UNRESOLVED, Resolution

if TYPE_CHECKING:
    from atlas.records import RefRow
    from atlas.resolve.ecmascript import EcmaResolver
    from atlas.resolve.go import GoResolver
    from atlas.resolve.index import NodeIndex
    from atlas.resolve.php import PhpResolver
    from atlas.resolve.python import PythonResolver


class Resolver:
    """Выбирает стратегию и собирает зависимости исходного файла."""

    def __init__(
        self,
        index: NodeIndex,
        php: PhpResolver,
        ecma: EcmaResolver,
        python: PythonResolver,
        go: GoResolver,
    ) -> None:
        self.index = index
        self._php = php
        self._ecma = ecma
        self._python = python
        self._go = go

    def resolve(self, ref: RefRow, language: str) -> Resolution:
        """Разрешить ссылку и запомнить прочитанные ключи снимка."""
        self.index.start(ref.file_id)
        if language == Language.PHP:
            return self._php.resolve(ref)
        if language == Language.PYTHON:
            self.index.track("modules:python")
            return self._python.resolve(ref)
        if language == Language.GO:
            self.index.track("modules:go")
            return self._go.resolve(ref)
        if language in {Language.TYPESCRIPT, Language.TSX, Language.JAVASCRIPT}:
            self.index.track("modules:ecmascript")
            return self._ecma.resolve(ref)
        return _UNRESOLVED
