"""Список файлов проекта, которые анализирует Atlas, и поиск изменений в нём.

Анализируется только то, что перечислено в ``paths``; поверх всегда действуют встроенные
исключения и ``.gitignore``. Каждый файл получает язык, кодовую базу и область поиска.

Изменения ищутся двумя способами. Полный обход проверяет ``stat`` каждого файла. Быстрый
спрашивает git об отслеживаемых файлах и проверяет mtime каталогов, чтобы найти новые
неотслеживаемые файлы: на catalog это 0,1 с вместо 0,4 с при той же полноте.
"""

import glob
import os
import re
import shutil
import subprocess
from dataclasses import dataclass
from functools import cache
from pathlib import Path
from typing import TYPE_CHECKING

from atlas.model import Language

if TYPE_CHECKING:
    from collections.abc import Iterable, Iterator

    from atlas.config import Config

MAX_FILE_SIZE = 1_000_000

_EXCLUDED_SEGMENTS = frozenset(
    {
        ".git",
        ".venv",
        ".next",
        ".turbo",
        "node_modules",
        "vendor",
        "var",
        "dist",
        "build",
        "coverage",
        "secrets",
        "__pycache__",
        ".tox",
        ".mypy_cache",
        ".pytest_cache",
        "site-packages",
    }
)
_EXTENSIONS: dict[str, Language] = {
    ".php": Language.PHP,
    ".ts": Language.TYPESCRIPT,
    ".mts": Language.TYPESCRIPT,
    ".cts": Language.TYPESCRIPT,
    ".tsx": Language.TSX,
    ".js": Language.JAVASCRIPT,
    ".jsx": Language.JAVASCRIPT,
    ".mjs": Language.JAVASCRIPT,
    ".cjs": Language.JAVASCRIPT,
    ".py": Language.PYTHON,
    ".pyi": Language.PYTHON,
    ".go": Language.GO,
    ".md": Language.MARKDOWN,
}
_CODEBASE_MARKERS = ("composer.json", "package.json", "pyproject.toml", "go.mod")
_TEST_FILE = re.compile(
    r"\.(test|spec)\.[cm]?[jt]sx?$|Test\.php$|(?:^|/)(?:test_[^/]*|[^/]*_test|conftest)\.py$|_test\.go$"
)


@dataclass(frozen=True, slots=True)
class ScannedFile:
    """Файл, попавший в анализ, с данными ``stat`` на момент обхода."""

    path: str
    language: Language
    size: int
    mtime_ns: int
    codebase: str
    scope: str


class PathMatcher:
    """Проверка пути по списку записей ``paths``: каталог, файл или глоб."""

    def __init__(self, patterns: Iterable[str]) -> None:
        self._prefixes: list[str] = []
        regexes: list[str] = []
        for pattern in patterns:
            if glob.has_magic(pattern):
                regexes.append(
                    glob.translate(pattern, recursive=True, include_hidden=True)
                )
            else:
                self._prefixes.append(pattern)
        self._regex = re.compile("|".join(regexes)) if regexes else None

    def matches(self, path: str) -> bool:
        """Путь совпадает с записью или лежит внутри перечисленного каталога."""
        for prefix in self._prefixes:
            if prefix in {".", ""} or path == prefix or path.startswith(prefix + "/"):
                return True
        return self._regex is not None and self._regex.match(path) is not None


class Classifier:
    """Решает, анализируется ли путь, и собирает о нём ``ScannedFile``.

    Фильтры работают со строками; объект Path создаётся только для файловой операции.
    """

    def __init__(self, config: Config) -> None:
        self._code = PathMatcher(config.code)
        self._docs = PathMatcher(config.docs)
        self._exclude = PathMatcher(config.exclude)
        self._scopes = {
            name: PathMatcher(patterns) for name, patterns in config.scopes.items()
        }
        self._root = str(config.root) + os.sep
        self._root_path = config.root
        self.pathspecs = [
            *config.code,
            *config.docs,
            *(p for ps in config.scopes.values() for p in ps),
        ]

    def classify(self, path: str) -> ScannedFile | None:
        """Файл для анализа или ``None``: путь вне ``paths``, исключён, удалён или велик."""
        dot = path.rfind(".")
        language = (
            _EXTENSIONS.get(path[dot:].lower()) if dot > path.rfind("/") else None
        )
        if language is None or _builtin_excluded(path) or self._exclude.matches(path):
            return None
        scope = next(
            (name for name, matcher in self._scopes.items() if matcher.matches(path)),
            None,
        )
        if language is Language.MARKDOWN:
            if scope is None and not self._docs.matches(path):
                return None
        elif scope is None and not self._code.matches(path):
            return None
        try:
            stat = (self._root_path / path).stat()
        except FileNotFoundError, NotADirectoryError:
            return None
        if stat.st_size > MAX_FILE_SIZE:
            return None
        return ScannedFile(
            path=path,
            language=language,
            size=stat.st_size,
            mtime_ns=stat.st_mtime_ns,
            codebase=self._codebase_of(path.rpartition("/")[0]),
            scope=scope or _default_scope(path, language),
        )

    @cache  # noqa: B019  # классификатор живёт одну синхронизацию
    def _codebase_of(self, directory: str) -> str:
        current = directory
        while current not in {".", ""}:
            base = self._root_path / current
            if any((base / marker).is_file() for marker in _CODEBASE_MARKERS):
                return current
            current = current.rpartition("/")[0]
        return "."

    def dir_mtime(self, directory: str) -> int | None:
        """Mtime каталога в наносекундах или ``None``, если его нет."""
        try:
            return (self._root_path / directory).stat().st_mtime_ns
        except FileNotFoundError, NotADirectoryError:
            return None


def scan(config: Config, classifier: Classifier | None = None) -> list[ScannedFile]:
    """Полный обход: все анализируемые файлы проекта, отсортированные по пути."""
    classifier = classifier or Classifier(config)
    result = [
        item
        for path in _candidate_paths(config.root, classifier.pathspecs)
        if (item := classifier.classify(path)) is not None
    ]
    result.sort(key=lambda item: item.path)
    return result


def directories(paths: Iterable[str]) -> set[str]:
    """Все каталоги-предки путей, включая корень (``""``)."""
    result: set[str] = {""}
    for path in paths:
        directory = path.rpartition("/")[0]
        while directory and directory not in result:
            result.add(directory)
            directory = directory.rpartition("/")[0]
    return result


# --- git ---------------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class GitState:
    """Состояние рабочей копии, которое быстрый поиск изменений сравнивает с прошлым."""

    head: str
    dirty: frozenset[str]


def git_state(root: Path) -> GitState | None:
    """Текущий коммит и изменённые отслеживаемые файлы; ``None``, если корень не в git."""
    # WHY: три независимых вызова идут параллельно: каждый запрос платит за самый долгий
    # из них (diff-files, stat всех отслеживаемых файлов), а не за сумму.
    head, worktree, staged = _git_parallel(
        root,
        ("rev-parse", "HEAD"),
        ("diff-files", "--name-only", "-z", "--relative"),
        ("diff-index", "--cached", "--name-only", "-z", "--relative", "HEAD"),
    )
    if head is None or worktree is None or staged is None:
        return None
    return GitState(
        head=head.decode().strip(),
        dirty=frozenset(_split_z(worktree) | _split_z(staged)),
    )


def git_changed_between(root: Path, old: str, new: str) -> set[str] | None:
    """Файлы, изменённые коммитами между ``old`` и ``new``; ``None``, если ``old`` пропал."""
    if old == new:
        return set()
    return _git_paths_z(
        root, "diff", "--name-only", "-z", "--relative", "--no-renames", old, new
    )


def git_untracked(root: Path, pathspecs: Iterable[str]) -> set[str] | None:
    """Неотслеживаемые неигнорируемые файлы под ``pathspecs``."""
    specs = [_pathspec(spec) for spec in pathspecs]
    if not specs:
        return set()
    return _git_paths_z(
        root, "ls-files", "-z", "--others", "--exclude-standard", "--", *specs
    )


def _git(root: Path, *args: str) -> bytes | None:
    git = shutil.which("git")
    if git is None:
        return None
    completed = subprocess.run(  # noqa: S603  # аргументы — литералы и пути из конфигурации
        [git, "-C", str(root), *args], capture_output=True, check=False
    )
    return completed.stdout if completed.returncode == 0 else None


def _git_parallel(root: Path, *commands: tuple[str, ...]) -> list[bytes | None]:
    git = shutil.which("git")
    if git is None:
        return [None] * len(commands)
    processes = [
        subprocess.Popen(  # noqa: S603  # аргументы — литералы и пути из конфигурации
            [git, "-C", str(root), *args],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
        for args in commands
    ]
    # Процессы читаются по очереди: заполненный канал только приостановит git до чтения.
    outputs = [process.communicate()[0] for process in processes]
    return [
        output if process.returncode == 0 else None
        for process, output in zip(processes, outputs, strict=True)
    ]


def _git_paths_z(root: Path, *args: str) -> set[str] | None:
    output = _git(root, *args)
    return None if output is None else _split_z(output)


def _split_z(output: bytes) -> set[str]:
    return {item.decode() for item in output.split(b"\0") if item}


def _pathspec(spec: str) -> str:
    return f":(glob){spec}" if glob.has_magic(spec) else spec


def _candidate_paths(root: Path, pathspecs: list[str]) -> Iterator[str]:
    # WHY: git сам применяет все .gitignore, включая вложенные и глобальный; повторять
    # его правила вручную — значит расходиться с ним на краевых случаях. Пути из
    # .atlas.json передаются ему же: фильтр на C вдвое быстрее, чем по всему дереву в Python.
    listed = _git_paths_z(
        root,
        "ls-files",
        "-z",
        "--cached",
        "--others",
        "--exclude-standard",
        "--",
        *(_pathspec(spec) for spec in pathspecs),
    )
    if listed is not None:
        yield from sorted(listed)
        return
    yield from _walk_paths(root)


def _walk_paths(root: Path) -> Iterator[str]:
    for directory, dirnames, filenames in os.walk(root, followlinks=True):
        dirnames[:] = sorted(
            name for name in dirnames if name not in _EXCLUDED_SEGMENTS
        )
        relative = Path(directory).relative_to(root).as_posix()
        prefix = "" if relative == "." else relative + "/"
        for filename in filenames:
            yield prefix + filename


def _builtin_excluded(path: str) -> bool:
    parts = path.split("/")
    if any(part in _EXCLUDED_SEGMENTS for part in parts[:-1]):
        return True
    name = parts[-1]
    return name.startswith(".env") or name.endswith((".min.js", ".min.mjs"))


def _default_scope(path: str, language: Language) -> str:
    if language is Language.MARKDOWN:
        name = path.rsplit("/", 1)[-1]
        agent = name in {"AGENTS.md", "CLAUDE.md"} or path.startswith(".agents/")
        return "agent" if agent else "documentation"
    parts = path.split("/")
    if "tests" in parts[:-1] or "__tests__" in parts[:-1] or _TEST_FILE.search(path):
        return "test"
    return "source"
