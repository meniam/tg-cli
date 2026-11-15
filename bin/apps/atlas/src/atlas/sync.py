"""Сверка индекса с рабочей копией перед каждой командой.

Файл перечитывается, только если изменились размер или mtime, и переразбирается, только
если изменился хеш. Переразрешаются все ссылки файлов, чьи зависимости изменились:
имена, типы, импорты и промежуточные реэкспорты. Запись идёт одной транзакцией под блокировкой.
"""

import fcntl
import hashlib
import json
import os
import time
from collections.abc import Callable, Iterable, Iterator
from concurrent.futures import ProcessPoolExecutor
from contextlib import contextmanager
from dataclasses import dataclass, field, replace
from pathlib import Path
from typing import TYPE_CHECKING, Any

from atlas.frameworks import layer
from atlas.generated import is_generated
from atlas.model import FileExtract, Language, Node, NodeKind, RefStatus, node_id
from atlas.parse import chunks, ecmascript, go, php, python
from atlas.progress import Progress
from atlas.records import NodeRow
from atlas.resolve import go_implements, implementations
from atlas.resolve.index import node_keys
from atlas.resolve.inputs import ResolutionInputs, read_inputs
from atlas.resolve.resolver import Resolver
from atlas.scan import (
    Classifier,
    ScannedFile,
    directories,
    git_changed_between,
    git_state,
    git_untracked,
    scan,
)
from atlas.store.db import FileRow, RefRow, Store, dumps

if TYPE_CHECKING:
    from atlas.config import Config
    from atlas.resolve.common import Resolution

type ResolverFactory = Callable[
    [Iterable[NodeRow], set[str], ResolutionInputs], Resolver
]

LOCK_TIMEOUT_SECONDS = 2.0
PARALLEL_THRESHOLD = 64
PROGRESS_THRESHOLD = 200
# Страховочный полный обход: быстрая проверка через git полна, но ошибка в ней не должна жить вечно.
FULL_SCAN_INTERVAL_SECONDS = 6 * 3600


@dataclass(frozen=True, slots=True)
class SyncReport:
    """Что изменила синхронизация; попадает в поле ``index`` каждого ответа."""

    synced_files: int = 0
    removed_files: int = 0
    resolved_refs: int = 0
    duration_ms: int = 0
    full_rebuild: bool = False
    stale_files: tuple[str, ...] = field(default=())
    phases_ms: dict[str, int] = field(default_factory=dict)
    check: str = ""


@contextmanager
def write_lock(config: Config, timeout: float = LOCK_TIMEOUT_SECONDS) -> Iterator[bool]:
    """Блокировка записи; ``False``, если её держит другой процесс дольше ``timeout``."""
    config.index_dir.mkdir(parents=True, exist_ok=True)
    _ensure_gitignore(config)
    with config.lock_path.open("a+") as handle:
        deadline = time.monotonic() + timeout
        while True:
            try:
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    yield False
                    return
                time.sleep(0.05)
                continue
            try:
                yield True
            finally:
                fcntl.flock(handle, fcntl.LOCK_UN)
            return


@dataclass(frozen=True, slots=True)
class _Parsed:
    item: ScannedFile
    sha: str
    extract: FileExtract


def synchronize(
    config: Config,
    store: Store,
    *,
    resolver_factory: ResolverFactory,
    full: bool = False,
) -> SyncReport:
    """Привести индекс в соответствие с диском.

    Изменения ищутся через git, если это возможно, иначе полным обходом (раздел про
    свежесть в ``scan``). Прогресс по этапам идёт в stderr, если работы много.

    Returns:
        Отчёт об изменениях и затраченном времени.

    """
    started = time.monotonic()
    rebuild = full or store.get_meta("paths_hash") != config.paths_hash
    progress = Progress(enabled=rebuild)
    classifier = Classifier(config)
    changes = None if rebuild else _git_changes(config, store, classifier)
    if changes is None:
        progress.start("scan", 0)
        changes = _full_changes(config, classifier, {} if rebuild else store.files())
        progress.advance(changes.scanned)
        progress.finish()
    candidates, removed, known = changes.candidates, changes.removed, changes.known
    input_dirs: dict[str, int | None] = (
        {} if changes.replace_dirs else dict(store.dirs())
    )
    input_dirs.update(changes.dirs)
    previous_inputs = json.loads(store.get_meta("input_directories") or "{}")
    inputs = read_inputs(config.root, input_dirs, previous_inputs)
    markers = json.dumps(inputs.markers)
    if not rebuild and store.get_meta("codebase_markers") != markers:
        return synchronize(config, store, resolver_factory=resolver_factory, full=True)
    inputs_changed = store.get_meta("resolution_inputs") != inputs.digest
    bases = layer.codebases(inputs.markers)
    framework_inputs = layer.input_digest(config.root, bases)
    frameworks_changed = store.get_meta("framework_inputs") != framework_inputs
    if (
        not candidates
        and not removed
        and not rebuild
        and not inputs_changed
        and not frameworks_changed
    ):
        if changes.state_changed(store) or previous_inputs != inputs.directories:
            with store.transaction():
                changes.save(store)
                store.set_meta("input_directories", json.dumps(inputs.directories))
        return SyncReport(duration_ms=_elapsed(started), check=changes.check)
    if len(candidates) > PROGRESS_THRESHOLD:
        progress = _enable(progress)

    progress.start("parse", len(candidates))
    changed: list[_Parsed] = []
    touched: list[tuple[int, ScannedFile]] = []
    for item, (sha, extract) in zip(
        candidates, _parse_all(config.root, candidates, known), strict=True
    ):
        row = known.get(item.path)
        if row is not None and row.sha256 == sha:
            touched.append((row.id, item))
        elif extract is not None:
            changed.append(_Parsed(item, sha, extract))
        progress.advance()
    progress.finish()

    with store.transaction():
        if rebuild:
            resolved = _write_full(store, changed, progress, resolver_factory, inputs)
        else:
            old_ids = [known[p.item.path].id for p in changed if p.item.path in known]
            old_ids += [known[path].id for path in removed]
            resolved = _write_incremental(
                store,
                changed,
                touched,
                old_ids,
                progress,
                resolver_factory,
                inputs,
                inputs_changed,
            )
        if changed or removed or rebuild or inputs_changed:
            progress.start("implementations", 0)
            progress.advance(go_implements.rebuild(store))
            progress.advance(implementations.rebuild(store))
            progress.finish()
        if changed or removed or rebuild or inputs_changed or frameworks_changed:
            progress.start("frameworks", 0)
            built = layer.rebuild(store, config, bases)
            progress.advance(built.routes)
            progress.finish()
            store.set_meta("frameworks", built.as_meta())
        changes.save(store)
        store.set_meta("framework_inputs", framework_inputs)
        store.set_meta("input_directories", json.dumps(inputs.directories))
        store.set_meta("resolution_inputs", inputs.digest)
        store.set_meta("codebase_markers", markers)
        store.set_meta("paths_hash", config.paths_hash)
        store.set_meta("synced_at", str(int(time.time())))
    return SyncReport(
        synced_files=len(changed),
        removed_files=len(removed),
        resolved_refs=resolved,
        duration_ms=_elapsed(started),
        full_rebuild=rebuild,
        phases_ms=dict(progress.phases_ms),
        check=changes.check,
    )


def stale_files(config: Config, store: Store) -> tuple[str, ...]:
    """Файлы, изменённые после последней синхронизации; без записи в базу."""
    classifier = Classifier(config)
    changes = _git_changes(config, store, classifier) or _full_changes(
        config, classifier, store.files()
    )
    return tuple(
        sorted({item.path for item in changes.candidates} | set(changes.removed))
    )


@dataclass(slots=True)
class _Changes:
    """Что изменилось на диске и какое состояние сохранить для следующей быстрой проверки."""

    check: str
    candidates: list[ScannedFile]
    removed: list[str]
    scanned: int
    head: str | None
    dirty: set[str]
    untracked: set[str]
    dirs: dict[str, int | None]
    replace_dirs: bool
    # Строки индекса по путям из candidates и removed; быстрая проверка читает только их.
    known: dict[str, FileRow]

    def state_changed(self, store: Store) -> bool:
        """Нужно ли записать новое состояние, хотя индекс не изменился."""
        return (
            self.check == "full"
            or bool(self.dirs)
            or store.get_meta("git_head") != self.head
            or set(_json_list(store.get_meta("git_dirty"))) != self.dirty
            or set(_json_list(store.get_meta("git_untracked"))) != self.untracked
        )

    def save(self, store: Store) -> None:
        """Записать состояние в текущей транзакции."""
        if self.replace_dirs:
            store.replace_dirs(
                {path: mtime for path, mtime in self.dirs.items() if mtime is not None}
            )
        else:
            store.update_dirs(self.dirs)
        if self.check == "full":
            store.set_meta("full_scan_at", str(int(time.time())))
        if self.head is None:
            store.set_meta("git_head", "")
            return
        store.set_meta("git_head", self.head)
        store.set_meta("git_dirty", json.dumps(sorted(self.dirty), ensure_ascii=False))
        store.set_meta(
            "git_untracked", json.dumps(sorted(self.untracked), ensure_ascii=False)
        )


def _full_changes(
    config: Config, classifier: Classifier, known: dict[str, FileRow]
) -> _Changes:
    """Полный обход: ``stat`` каждого файла; заодно снимается состояние git для быстрых проверок."""
    scanned = {item.path: item for item in scan(config, classifier)}
    candidates = [
        item
        for path, item in scanned.items()
        if (row := known.get(path)) is None
        or (row.size, row.mtime_ns) != (item.size, item.mtime_ns)
    ]
    removed = sorted(set(known) - set(scanned))
    state = git_state(config.root)
    untracked: set[str] = set()
    if state is not None:
        found = git_untracked(config.root, classifier.pathspecs)
        untracked = (found or set()) & set(scanned)
    dirs: dict[str, int | None] = {
        path: classifier.dir_mtime(path) for path in directories(scanned)
    }
    return _Changes(
        check="full",
        candidates=candidates,
        removed=removed,
        scanned=len(scanned),
        head=state.head if state else None,
        dirty=set(state.dirty) if state else set(),
        untracked=untracked,
        dirs=dirs,
        replace_dirs=True,
        known=known,
    )


def _git_changes(
    config: Config, store: Store, classifier: Classifier
) -> _Changes | None:
    """Быстрая проверка через git; ``None``, если она невозможна и нужен полный обход.

    Проверяются только файлы, которые могли измениться: из коммитов после прошлой
    синхронизации, изменённые сейчас, изменённые в прошлый раз (их могли откатить),
    неотслеживаемые и лежащие в каталогах, чей mtime сдвинулся (создание и удаление файла).

    Returns:
        Набор изменений или None для полного обхода.

    """
    last_head = store.get_meta("git_head")
    full_at = store.get_meta("full_scan_at")
    if (
        not last_head
        or full_at is None
        or time.time() - int(full_at) > FULL_SCAN_INTERVAL_SECONDS
    ):
        return None
    state = git_state(config.root)
    if state is None:
        return None
    committed = git_changed_between(config.root, last_head, state.head)
    if committed is None:
        return None
    last_dirty = set(_json_list(store.get_meta("git_dirty")))
    last_untracked = set(_json_list(store.get_meta("git_untracked")))

    stored_dirs = store.dirs()
    changed_dirs: dict[str, int | None] = {}
    for directory, mtime in stored_dirs.items():
        current = classifier.dir_mtime(directory)
        if current != mtime:
            changed_dirs[directory] = current
    # WHY: вся таблица файлов нужна, только когда сдвинулся mtime каталога, то есть файл
    # создан или удалён. В остальных запросах хватает строк проверяемых путей, а чтение
    # всех строк стоило ~40 мс на каждый запрос.
    known = store.files() if changed_dirs else {}
    fresh, new_dirs = _files_in_dirs(
        config.root, [d for d, m in changed_dirs.items() if m is not None], known
    )
    new_untracked: set[str] = set()
    if fresh or new_dirs:
        specs = [*fresh, *(f"{d}/" for d in new_dirs)]
        found = git_untracked(config.root, specs)
        if found is None:
            return None
        new_untracked = found

    in_changed_dirs = {
        path for path in known if path.rpartition("/")[0] in changed_dirs
    }
    check = (
        committed
        | state.dirty
        | last_dirty
        | last_untracked
        | new_untracked
        | in_changed_dirs
    )
    if not changed_dirs:
        known = store.files_at(check)
    items = {path: classifier.classify(path) for path in check}
    candidates = [
        item
        for path, item in items.items()
        if item is not None
        and (
            (row := known.get(path)) is None
            or (row.size, row.mtime_ns) != (item.size, item.mtime_ns)
        )
    ]
    removed = sorted(
        path for path, item in items.items() if item is None and path in known
    )
    untracked = {
        path
        for path in (last_untracked | new_untracked) - committed
        if items.get(path) is not None or (path not in items and path in known)
    }
    for item in candidates:
        for directory in directories([item.path]):
            if directory not in changed_dirs and directory not in stored_dirs:
                changed_dirs[directory] = classifier.dir_mtime(directory)
    return _Changes(
        check="git",
        candidates=sorted(candidates, key=lambda item: item.path),
        removed=removed,
        scanned=len(items),
        head=state.head,
        dirty=set(state.dirty),
        untracked=untracked,
        dirs=changed_dirs,
        replace_dirs=False,
        known=known,
    )


def _files_in_dirs(
    root: Path, dirs: list[str], known: dict[str, FileRow]
) -> tuple[list[str], list[str]]:
    """Новые файлы и новые подкаталоги прямо внутри изменившихся каталогов."""
    known_dirs = directories(known)
    fresh: list[str] = []
    new_dirs: list[str] = []
    for directory in dirs:
        prefix = f"{directory}/" if directory else ""
        try:
            entries = list(os.scandir(root / directory if directory else root))
        except FileNotFoundError, NotADirectoryError:
            continue
        for entry in entries:
            path = prefix + entry.name
            if entry.is_dir(follow_symlinks=False):
                if path not in known_dirs and entry.name not in {
                    ".git",
                    "node_modules",
                    "vendor",
                    "var",
                }:
                    new_dirs.append(path)
            elif path not in known:
                fresh.append(path)
    return fresh, new_dirs


def _enable(progress: Progress) -> Progress:
    """Включить вывод посреди синхронизации, сохранив накопленное время этапов."""
    enabled = Progress()
    enabled.phases_ms.update(progress.phases_ms)
    return enabled


def _insert(
    store: Store, parsed: _Parsed, *, with_refs: bool
) -> tuple[int, dict[str, int]]:
    item = parsed.item
    file_id = store.insert_file(
        item.path,
        item.language,
        item.codebase,
        item.scope,
        (item.size, item.mtime_ns),
        parsed.sha,
        parsed.extract.errors,
        generated=parsed.extract.generated,
    )
    return file_id, store.insert_extract(
        parsed.extract, file_id, item.codebase, with_refs=with_refs
    )


def _write_full(
    store: Store,
    changed: list[_Parsed],
    progress: Progress,
    resolver_factory: ResolverFactory,
    inputs: ResolutionInputs,
) -> int:
    """Полная пересборка: узлы пишутся первыми, ссылки разрешаются из памяти и пишутся один раз.

    Ссылка не проходит круг «вставить pending → прочитать → обновить», а индексы ссылок и
    рёбер строятся один раз в конце, а не поддерживаются на каждой вставке.

    Returns:
        Число записанных разрешённых рёбер.

    """
    store.drop_bulk_indexes()
    store.delete_all()
    progress.start("write", len(changed))
    written: list[tuple[_Parsed, int, dict[str, int]]] = []
    for parsed in changed:
        file_id, ids = _insert(store, parsed, with_refs=False)
        written.append((parsed, file_id, ids))
        progress.advance()
    progress.finish()

    paths = {p.item.path for p in changed}
    resolver = resolver_factory(store.all_nodes(), paths, inputs)
    progress.start("resolve", sum(len(p.extract.refs) for p in changed))
    batch = _Batch(store, full=True)
    for parsed, file_id, ids in written:
        for ref in parsed.extract.refs:
            source = ids.get(ref.source)
            if source is None:
                continue
            row = RefRow(
                id=0,
                source=source,
                file_id=file_id,
                path=parsed.item.path,
                name=ref.name,
                kind=ref.kind,
                line=ref.line,
                col=ref.col,
                context=ref.context,
                status=RefStatus.PENDING,
                target=None,
            )
            batch.add(row, ref.name_tail, resolver.resolve(row, parsed.item.language))
        progress.advance(len(parsed.extract.refs))
    batch.flush()
    store.save_dependencies(resolver.index.dependencies)
    progress.finish()

    progress.start("indexes", 0)
    store.create_bulk_indexes()
    progress.finish()
    return batch.edges


def _write_incremental(
    store: Store,
    changed: list[_Parsed],
    touched: list[tuple[int, ScannedFile]],
    old_ids: list[int],
    progress: Progress,
    resolver_factory: ResolverFactory,
    inputs: ResolutionInputs,
    inputs_changed: bool,
) -> int:
    """Обычная синхронизация: заменить изменённые файлы и переразрешить затронутые ссылки."""
    progress.start("write", len(changed))
    old_paths = store.file_paths()
    changed_keys = {
        key for node in store.nodes_in_files(old_ids) for key in node_keys(node)
    }
    store.delete_files(old_ids)
    for file_id, item in touched:
        store.touch_file(file_id, item.size, item.mtime_ns)
    new_ids: list[int] = []
    for parsed in changed:
        file_id, _ = _insert(store, parsed, with_refs=True)
        new_ids.append(file_id)
        progress.advance()
    changed_keys.update(
        key for node in store.nodes_in_files(new_ids) for key in node_keys(node)
    )
    if old_paths != store.file_paths():
        changed_keys.update({"modules:python", "modules:ecmascript", "modules:go"})
    progress.finish()

    refs = store.refs_to_resolve(changed_keys, all_refs=inputs_changed)
    progress.start("resolve", len(refs))
    if not refs:
        progress.finish()
        return 0
    files = store.files()
    paths = set(files)
    resolver = resolver_factory(store.all_nodes(), paths, inputs)
    store.delete_ref_edges(refs)
    batch = _Batch(store, full=False)
    for ref in refs:
        language = files[ref.path].language if ref.path in files else ""
        batch.add(ref, "", resolver.resolve(ref, language))
        progress.advance()
    batch.flush()
    store.save_dependencies(resolver.index.dependencies)
    progress.finish()
    return batch.edges


class _Batch:
    """Копит итоги разрешения и пишет их пачками: одиночные запросы — половина времени этапа.

    Внешние ссылки сохраняются для повторного разрешения при появлении модулей.
    """

    SIZE = 20_000

    def __init__(self, store: Store, *, full: bool) -> None:
        self._store = store
        self._full = full
        self._refs: list[tuple[Any, ...]] = []
        self._edges: list[tuple[Any, ...]] = []
        self.edges = 0

    def add(self, ref: RefRow, name_tail: str, result: Resolution) -> None:
        target = result.target
        target_id = target.id if target is not None else None
        target_file = target.file_id if target is not None else None
        candidates = (
            dumps([node.id for node in result.candidates])
            if result.candidates
            else None
        )
        if self._full:
            self._refs.append(
                (
                    ref.source,
                    ref.file_id,
                    ref.name,
                    name_tail,
                    ref.kind,
                    ref.line,
                    ref.col,
                    dumps(ref.context),
                    result.status,
                    target_id,
                    target_file,
                    candidates,
                )
            )
        else:
            self._refs.append(
                (result.status, target_id, target_file, candidates, ref.id)
            )
        if (
            result.status is RefStatus.RESOLVED
            and target is not None
            and target.id != ref.source
        ):
            self._edges.append(
                (
                    ref.source,
                    target.id,
                    result.kind or ref.kind,
                    ref.line,
                    ref.col,
                    result.provenance,
                    result.confidence,
                    result.via,
                    ref.file_id,
                )
            )
            self.edges += 1
        if len(self._refs) >= self.SIZE:
            self.flush()

    def flush(self) -> None:
        if self._full:
            self._store.insert_resolved_refs(self._refs)
        else:
            self._store.set_ref_results(self._refs)
        self._store.insert_edge_rows(self._edges)
        self._refs.clear()
        self._edges.clear()


def _parse_all(
    root: Path, items: list[ScannedFile], known: dict[str, FileRow]
) -> Iterator[tuple[str, FileExtract | None]]:
    """Результаты разбора в порядке ``items``, по мере готовности."""
    jobs = [
        (
            str(root),
            item.path,
            item.language,
            known[item.path].sha256 if item.path in known else None,
        )
        for item in items
    ]
    if len(jobs) < PARALLEL_THRESHOLD:
        yield from map(_parse_job, jobs)
        return
    workers = max(1, min(os.cpu_count() or 1, 8))
    with ProcessPoolExecutor(max_workers=workers) as pool:
        yield from pool.map(_parse_job, jobs, chunksize=16)


def _parse_job(
    job: tuple[str, str, Language, str | None],
) -> tuple[str, FileExtract | None]:
    root, path, language, previous_hash = job
    source = (Path(root) / path).read_bytes()
    sha = hashlib.sha256(source).hexdigest()
    return sha, None if sha == previous_hash else parse_source(path, source, language)


def parse_source(path: str, source: bytes, language: Language) -> FileExtract:
    """Извлечь граф и записи поиска одного файла; ошибка разбора не роняет синхронизацию."""
    generated = is_generated(path, source)
    try:
        match language:
            case Language.MARKDOWN:
                return replace(
                    chunks.markdown_extract(path, source), generated=generated
                )
            case Language.PHP:
                extract = php.extract(path, source)
            case Language.TYPESCRIPT | Language.TSX | Language.JAVASCRIPT:
                extract = ecmascript.extract(path, source, language)
            case Language.PYTHON:
                extract = python.extract(path, source)
            case Language.GO:
                extract = go.extract(path, source)
    except (ValueError, RecursionError, UnicodeError) as error:
        extract = _file_only(path, language, source)
        return replace(extract, errors=(f"parse error: {error}",), generated=generated)
    text = source.decode("utf-8", errors="replace")
    return replace(
        extract, chunks=chunks.code_chunks(extract, text), generated=generated
    )


def _file_only(path: str, language: Language, source: bytes) -> FileExtract:
    node = Node(
        id=node_id(NodeKind.FILE, path, path),
        kind=NodeKind.FILE,
        name=Path(path).name,
        qualified_name=path,
        path=path,
        language=language,
        start_line=1,
        end_line=source.count(b"\n") + 1,
    )
    return FileExtract(path, language, (node,), (), ())


def _ensure_gitignore(config: Config) -> None:
    """Каталог индекса вне ``var/`` не должен попасть в git."""
    gitignore = config.index_dir / ".gitignore"
    if not gitignore.exists():
        gitignore.write_text("*\n", encoding="utf-8")


def _json_list(raw: str | None) -> list[str]:
    if not raw:
        return []
    value = json.loads(raw)
    return [str(item) for item in value] if isinstance(value, list) else []


def _elapsed(started: float) -> int:
    return int((time.monotonic() - started) * 1000)
