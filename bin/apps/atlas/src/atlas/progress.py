"""Прогресс синхронизации в stderr: этапы, счётчик, скорость.

В терминале строка обновляется на месте. Без терминала (агент, CI) печатается строка
на начало и конец этапа и промежуточная не чаще раза в пять секунд, чтобы не засорять вывод.
"""

import sys
import time
from typing import TextIO

_TTY_INTERVAL = 0.2
_PLAIN_INTERVAL = 5.0


class Progress:
    """Счётчик одного этапа за другим; время этапов копится в ``phases_ms``."""

    def __init__(self, stream: TextIO | None = None, *, enabled: bool = True) -> None:
        self._stream = stream or sys.stderr
        self._enabled = enabled
        self._tty = self._stream.isatty()
        self._phase = ""
        self._total = 0
        self._done = 0
        self._started = 0.0
        self._last_print = 0.0
        self.phases_ms: dict[str, int] = {}

    def start(self, phase: str, total: int) -> None:
        """Начать этап из ``total`` единиц работы."""
        self._phase = phase
        self._total = total
        self._done = 0
        self._started = time.monotonic()
        self._last_print = self._started
        if self._enabled and not self._tty:
            self._write(f"atlas: {phase}: {total}")

    def advance(self, count: int = 1) -> None:
        """Отметить выполненную работу; печать ограничена по частоте."""
        self._done += count
        if not self._enabled:
            return
        now = time.monotonic()
        interval = _TTY_INTERVAL if self._tty else _PLAIN_INTERVAL
        if now - self._last_print >= interval:
            self._last_print = now
            self._write(self._line(now), transient=self._tty)

    def finish(self) -> None:
        """Закончить этап: итог со временем и скоростью."""
        now = time.monotonic()
        self.phases_ms[self._phase] = int((now - self._started) * 1000)
        if self._enabled:
            self._write(self._line(now) + " done")

    def _line(self, now: float) -> str:
        elapsed = max(now - self._started, 1e-6)
        rate = self._done / elapsed
        percent = f" {self._done * 100 // self._total}%" if self._total else ""
        return (
            f"atlas: {self._phase}: {self._done}/{self._total}{percent}, "
            f"{rate:,.0f}/s, {elapsed:.1f} s".replace(",", " ")
        )

    def _write(self, line: str, *, transient: bool = False) -> None:
        if self._tty:
            end = "" if transient else "\n"
            self._stream.write(f"\r\x1b[K{line}{end}")
        else:
            self._stream.write(line + "\n")
        self._stream.flush()
