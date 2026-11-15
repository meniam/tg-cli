"""Хранилище индекса в SQLite.

Запись идёт из одного процесса одной транзакцией на синхронизацию; читатели работают
в режиме WAL и видят либо старое, либо новое состояние целиком.
"""

import json
import re
import sqlite3
import time
import zlib
from contextlib import contextmanager
from pathlib import Path
from typing import TYPE_CHECKING, Any

from atlas.model import FileExtract, Node, RefStatus
from atlas.records import FileRow as FileRow
from atlas.records import NodeRow as NodeRow
from atlas.records import RefRow as RefRow

if TYPE_CHECKING:
    from collections.abc import Iterable, Iterator, Sequence

SCHEMA_VERSION = "7"
_SCHEMA_SQL = (Path(__file__).parent / "schema.sql").read_text(encoding="utf-8")
_BULK_INDEX_DDL = tuple(
    statement.strip() + ";"
    for statement in _SCHEMA_SQL.split(";")
    if re.search(r"CREATE INDEX IF NOT EXISTS (refs|edges)_", statement)
)
_BULK_INDEXES = tuple(
    match.group(1)
    for statement in _BULK_INDEX_DDL
    if (match := re.search(r"CREATE INDEX IF NOT EXISTS (\w+)", statement)) is not None
)


_NODE_COLUMNS = (
    "id, key, file_id, kind, name, qualified_name, path, codebase, language, start_line, "
    "end_line, signature, flags, meta"
)
_REF_SELECT = (
    "SELECT r.id, r.source, r.file_id, f.path, r.name, r.kind, r.line, r.col, r.context, "
    "r.status, r.target FROM refs r JOIN files f ON f.id = r.file_id"
)


def dumps(value: object) -> str:
    """Компактный детерминированный JSON для колонок базы."""
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True)


class Store:
    """Доступ к базе индекса. Один экземпляр на процесс CLI."""

    def __init__(self, connection: sqlite3.Connection) -> None:
        self._db = connection

    @classmethod
    def open(cls, path: Path) -> Store:
        """Открыть базу, создав её при необходимости.

        База с другой версией схемы пересоздаётся: индекс — производные данные, миграции
        для него дороже полной пересборки.

        Returns:
            Открытое хранилище с текущей схемой.

        """
        path.parent.mkdir(parents=True, exist_ok=True)
        connection = cls._connect(path)
        version = cls._schema_version(connection)
        if version not in {None, SCHEMA_VERSION}:
            connection.close()
            for suffix in ("", "-wal", "-shm"):
                Path(f"{path}{suffix}").unlink(missing_ok=True)
            connection = cls._connect(path)
        connection.executescript(_SCHEMA_SQL)
        connection.execute(
            "INSERT OR IGNORE INTO meta (key, value) VALUES ('schema_version', ?)",
            (SCHEMA_VERSION,),
        )
        return cls(connection)

    @staticmethod
    def _connect(path: Path) -> sqlite3.Connection:
        connection = sqlite3.connect(
            path, isolation_level=None, check_same_thread=False
        )
        connection.execute("PRAGMA journal_mode = WAL")
        connection.execute("PRAGMA synchronous = NORMAL")
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA temp_store = MEMORY")
        connection.execute("PRAGMA cache_size = -65536")
        return connection

    @staticmethod
    def _schema_version(connection: sqlite3.Connection) -> str | None:
        exists = connection.execute(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'meta'"
        ).fetchone()
        if exists is None:
            return None
        row = connection.execute(
            "SELECT value FROM meta WHERE key = 'schema_version'"
        ).fetchone()
        return None if row is None else str(row[0])

    def close(self) -> None:
        """Закрыть соединение; журнал WAL сливается в базу и усекается."""
        self._db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
        self._db.close()

    @contextmanager
    def transaction(self) -> Iterator[None]:
        """Одна транзакция записи; при исключении откатывается целиком."""
        self._db.execute("BEGIN IMMEDIATE")
        try:
            yield
        except BaseException:
            self._db.execute("ROLLBACK")
            raise
        self._db.execute("COMMIT")

    def execute(self, sql: str, params: Sequence[Any] = ()) -> sqlite3.Cursor:
        """Выполнить запрос; для модулей запросов, которым нужен свой SQL."""
        return self._db.execute(sql, params)

    # --- meta ---------------------------------------------------------------------------

    def get_meta(self, key: str) -> str | None:
        """Значение служебного ключа или ``None``."""
        row = self._db.execute(
            "SELECT value FROM meta WHERE key = ?", (key,)
        ).fetchone()
        return None if row is None else str(row[0])

    def set_meta(self, key: str, value: str) -> None:
        """Записать служебный ключ."""
        self._db.execute(
            "INSERT INTO meta (key, value) VALUES (?, ?) "
            "ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            (key, value),
        )

    # --- файлы --------------------------------------------------------------------------

    def files(self) -> dict[str, FileRow]:
        """Все файлы индекса по пути."""
        rows = self._db.execute(
            "SELECT id, path, language, codebase, scope, size, mtime_ns, sha256 FROM files"
        )
        return {row[1]: FileRow(*row) for row in rows}

    def files_at(self, paths: Iterable[str]) -> dict[str, FileRow]:
        """Файлы индекса с указанными путями; путей, которых нет в индексе, в ответе нет."""
        rows = self._db.execute(
            "SELECT id, path, language, codebase, scope, size, mtime_ns, sha256 FROM files "
            "WHERE path IN (SELECT value FROM json_each(?))",
            (json.dumps(sorted(paths), ensure_ascii=False),),
        )
        return {row[1]: FileRow(*row) for row in rows}

    def delete_files(self, file_ids: Iterable[int]) -> None:
        """Удалить файлы вместе с узлами, рёбрами, ссылками и записями поиска."""
        ids = [(file_id,) for file_id in file_ids]
        self._db.executemany(
            "DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE file_id = ?)",
            ids,
        )
        self._db.executemany("DELETE FROM refs WHERE file_id = ?", ids)
        self._db.executemany("DELETE FROM files WHERE id = ?", ids)

    def delete_all(self) -> None:
        """Очистить индекс перед полной пересборкой."""
        for table in (
            "chunks_fts",
            "chunks",
            "http_calls",
            "refs",
            "edges",
            "nodes",
            "files",
        ):
            self._db.execute(f"DELETE FROM {table}")  # noqa: S608  # имя таблицы — литерал

    def touch_file(self, file_id: int, size: int, mtime_ns: int) -> None:
        """Обновить ``stat`` файла, содержимое которого не изменилось."""
        self._db.execute(
            "UPDATE files SET size = ?, mtime_ns = ? WHERE id = ?",
            (size, mtime_ns, file_id),
        )

    def insert_file(
        self,
        path: str,
        language: str,
        codebase: str,
        scope: str,
        stat: tuple[int, int],
        sha256: str,
        errors: Sequence[str],
        *,
        generated: bool = False,
    ) -> int:
        """Записать файл и вернуть его id. ``stat`` — размер и mtime в наносекундах."""
        cursor = self._db.execute(
            "INSERT INTO files (path, language, codebase, scope, size, mtime_ns, sha256, "
            "indexed_at, parse_errors, generated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                path,
                language,
                codebase,
                scope,
                stat[0],
                stat[1],
                sha256,
                int(time.time()),
                dumps(list(errors)) if errors else None,
                int(generated),
            ),
        )
        return int(cursor.lastrowid or 0)

    def insert_extract(
        self,
        extract: FileExtract,
        file_id: int,
        codebase: str,
        *,
        with_refs: bool = True,
    ) -> dict[str, int]:
        """Записать узлы, синтаксические рёбра, записи поиска и, если нужно, ссылки файла.

        Возвращает отображение ключа узла в его id: полная пересборка пишет ссылки сама,
        уже разрешёнными.

        Returns:
            Соответствие стабильных ключей узлов их числовым id.

        """
        self._db.executemany(
            "INSERT OR REPLACE INTO nodes (key, file_id, kind, name, qualified_name, path, "
            "codebase, language, start_line, end_line, signature, docblock, attributes, flags, "
            "meta) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (_node_values(node, file_id, codebase) for node in extract.nodes),
        )
        ids = {
            str(key): int(node_id)
            for key, node_id in self._db.execute(
                "SELECT key, id FROM nodes WHERE file_id = ?", (file_id,)
            )
        }
        self._db.executemany(
            "INSERT OR IGNORE INTO edges (source, target, kind, line, col, provenance, "
            "confidence, via, origin_file) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                (
                    ids[edge.source],
                    ids[edge.target],
                    edge.kind,
                    edge.line or 0,
                    edge.col or 0,
                    edge.provenance,
                    edge.confidence,
                    edge.via,
                    file_id,
                )
                for edge in extract.edges
                if edge.source in ids and edge.target in ids
            ),
        )
        if extract.chunks:
            self._db.execute(
                "UPDATE files SET search_relations = ? WHERE id = ?",
                (extract.chunks[0].search_relations, file_id),
            )
        for chunk in extract.chunks:
            cursor = self._db.execute(
                "INSERT INTO chunks (file_id, kind, start_line, end_line, title, symbols, "
                "content, search_content) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    file_id,
                    chunk.kind,
                    chunk.start_line,
                    chunk.end_line,
                    chunk.title,
                    dumps(list(chunk.symbols)),
                    zlib.compress(chunk.content.encode("utf-8"), 6),
                    zlib.compress(chunk.search_content.encode("utf-8"), 6),
                ),
            )
            self._db.execute(
                "INSERT INTO chunks_fts (rowid, search_text) VALUES (?, ?)",
                (cursor.lastrowid, chunk.search_text),
            )
        self._db.executemany(
            "INSERT INTO http_calls (file_id, source, callee, method, path, line, col) "
            "VALUES (?, ?, ?, ?, ?, ?, ?)",
            (
                (
                    file_id,
                    ids[call.source],
                    call.callee,
                    call.method,
                    call.path,
                    call.line,
                    call.col,
                )
                for call in extract.http_calls
                if call.source in ids
            ),
        )
        if with_refs:
            self._db.executemany(
                "INSERT INTO refs (source, file_id, name, name_tail, kind, line, col, context, "
                "status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    (
                        ids[ref.source],
                        file_id,
                        ref.name,
                        ref.name_tail,
                        ref.kind,
                        ref.line,
                        ref.col,
                        dumps(ref.context),
                        RefStatus.PENDING,
                    )
                    for ref in extract.refs
                    if ref.source in ids
                ),
            )
        return ids

    # --- каталоги ----------------------------------------------------------------------

    def dirs(self) -> dict[str, int]:
        """Mtime каталогов на момент последней синхронизации."""
        return {
            str(path): int(mtime)
            for path, mtime in self._db.execute("SELECT path, mtime_ns FROM dirs")
        }

    def replace_dirs(self, dirs: dict[str, int]) -> None:
        """Заменить таблицу каталогов целиком, после полного обхода."""
        self._db.execute("DELETE FROM dirs")
        self._db.executemany(
            "INSERT INTO dirs (path, mtime_ns) VALUES (?, ?)", dirs.items()
        )

    def update_dirs(self, dirs: dict[str, int | None]) -> None:
        """Обновить mtime каталогов; ``None`` удаляет пропавший каталог."""
        self._db.executemany(
            "DELETE FROM dirs WHERE path = ?",
            ((path,) for path, mtime in dirs.items() if mtime is None),
        )
        self._db.executemany(
            "INSERT INTO dirs (path, mtime_ns) VALUES (?, ?) "
            "ON CONFLICT(path) DO UPDATE SET mtime_ns = excluded.mtime_ns",
            ((path, mtime) for path, mtime in dirs.items() if mtime is not None),
        )

    # --- ссылки и рёбра -----------------------------------------------------------------

    def refs_to_resolve(
        self, keys: Iterable[str], *, all_refs: bool = False
    ) -> list[RefRow]:
        """Все ссылки файлов, чьи зависимости изменились, и новые ссылки."""
        if all_refs:
            return [
                _ref_row(row)
                for row in self._db.execute(f"{_REF_SELECT} ORDER BY r.id")
            ]
        self._db.execute(
            "CREATE TEMP TABLE IF NOT EXISTS changed_keys (key TEXT PRIMARY KEY)"
        )
        self._db.execute("DELETE FROM changed_keys")
        self._db.executemany(
            "INSERT OR IGNORE INTO changed_keys VALUES (?)", ((key,) for key in keys)
        )
        rows = self._db.execute(
            f"{_REF_SELECT} WHERE r.status = 'pending' OR r.file_id IN ("  # noqa: S608  # подставляется постоянный SELECT; ключи идут параметрами
            "SELECT d.file_id FROM resolution_dependencies d JOIN changed_keys k ON k.key=d.key) "
            "ORDER BY r.id"
        )
        return [_ref_row(row) for row in rows]

    def save_dependencies(self, dependencies: dict[int, set[str]]) -> None:
        """Заменить зависимости полностью переразрешённых файлов."""
        self._db.executemany(
            "DELETE FROM resolution_dependencies WHERE file_id=?",
            ((file_id,) for file_id in dependencies),
        )
        self._db.executemany(
            "INSERT INTO resolution_dependencies (file_id, key) VALUES (?, ?)",
            (
                (file_id, key)
                for file_id, keys in dependencies.items()
                for key in sorted(keys)
            ),
        )

    def all_nodes(self) -> list[NodeRow]:
        """Снимок символов для вычислений без дальнейшего доступа к SQLite."""
        return self._nodes("1", ())

    def nodes_of_language(self, language: str) -> list[NodeRow]:
        """Узлы одного языка для вычислений поверх всего пакета или проекта."""
        return self._nodes("language = ?", (language,))

    def nodes_in_files(self, file_ids: Iterable[int]) -> list[NodeRow]:
        """Узлы изменяемых файлов для инвалидации зависимостей."""
        return [
            node
            for file_id in file_ids
            for node in self._nodes("file_id = ?", (file_id,))
        ]

    def set_ref_results(self, rows: Iterable[tuple[Any, ...]]) -> None:
        """Сохранить итоги пачкой: ``(status, target, target_file, candidates, id)``."""
        self._db.executemany(
            "UPDATE refs SET status = ?, target = ?, target_file = ?, candidates = ? WHERE id = ?",
            rows,
        )

    def delete_refs(self, ref_ids: Iterable[int]) -> None:
        """Удалить ссылки, которые оказались ссылками на внешний код."""
        self._db.executemany(
            "DELETE FROM refs WHERE id = ?", ((ref_id,) for ref_id in ref_ids)
        )

    def insert_resolved_refs(self, rows: Iterable[tuple[Any, ...]]) -> None:
        """Записать ссылки сразу с итогом разрешения; для полной пересборки.

        Кортеж: ``(source, file_id, name, name_tail, kind, line, col, context, status,
        target, target_file, candidates)``.
        """
        self._db.executemany(
            "INSERT INTO refs (source, file_id, name, name_tail, kind, line, col, context, "
            "status, target, target_file, candidates) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            rows,
        )

    def insert_edge_rows(self, rows: Iterable[tuple[Any, ...]]) -> None:
        """Записать рёбра пачкой; повтор игнорируется.

        Кортеж: ``(source, target, kind, line, col, provenance, confidence, via, origin_file)``.
        """
        self._db.executemany(
            "INSERT OR IGNORE INTO edges (source, target, kind, line, col, provenance, "
            "confidence, via, origin_file) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            rows,
        )

    def delete_ref_edges(self, refs: Iterable[RefRow]) -> None:
        """Удалить рёбра, созданные прошлым разрешением этих ссылок."""
        self._db.executemany(
            "DELETE FROM edges WHERE source = ? AND target = ? AND kind = ? AND line = ? "
            "AND col = ?",
            (
                (r.source, r.target, r.kind, r.line, r.col)
                for r in refs
                if r.target is not None
            ),
        )

    def delete_edges_by_via(self, via: str) -> None:
        """Удалить эвристические рёбра одного правила, чтобы пересчитать их заново."""
        self._db.execute(
            "DELETE FROM edges WHERE provenance = 'heuristic' AND via = ?", (via,)
        )

    def drop_bulk_indexes(self) -> None:
        """Снять вторичные индексы ссылок и рёбер на время массовой загрузки.

        Их поддержка на каждой вставке дороже, чем построение с нуля в конце. Индексы
        узлов остаются: по ним работает разрешение ссылок во время загрузки.
        """
        for name in _BULK_INDEXES:
            self._db.execute(f"DROP INDEX IF EXISTS {name}")

    def create_bulk_indexes(self) -> None:
        """Вернуть индексы, снятые ``drop_bulk_indexes``."""
        for statement in _BULK_INDEX_DDL:
            self._db.execute(statement)

    # --- узлы ---------------------------------------------------------------------------

    def node(self, node_id: int) -> NodeRow | None:
        """Узел по id."""
        found = self._nodes("id = ?", (node_id,))
        return found[0] if found else None

    def node_by_key(self, key: str) -> NodeRow | None:
        """Узел по стабильному ключу ``kind:path:qualified_name``."""
        found = self._nodes("key = ?", (key,))
        return found[0] if found else None

    def nodes_by_lower_qualified_name(self, qualified_name: str) -> list[NodeRow]:
        """Узлы с полным именем без учёта регистра: имена классов PHP нечувствительны к нему."""
        return self._nodes("lower(qualified_name) = lower(?)", (qualified_name,))

    def nodes_by_name(self, name: str, codebase: str | None = None) -> list[NodeRow]:
        """Узлы с коротким именем, при необходимости в пределах кодовой базы."""
        if codebase is None:
            return self._nodes("name = ?", (name,))
        return self._nodes("name = ? AND codebase = ?", (name, codebase))

    def nodes_by_kind(self, kind: str) -> list[NodeRow]:
        """Все узлы одного вида."""
        return self._nodes("kind = ?", (kind,))

    def nodes_in_file(self, path: str) -> list[NodeRow]:
        """Узлы файла по порядку строк."""
        return self._nodes("path = ?", (path,))

    def names_in_files(self, file_ids: Iterable[int]) -> set[str]:
        """Короткие имена всех узлов файлов."""
        names: set[str] = set()
        for file_id in file_ids:
            rows = self._db.execute(
                "SELECT name FROM nodes WHERE file_id = ?", (file_id,)
            )
            names.update(str(row[0]) for row in rows)
        return names

    def nodes_by_ids(self, node_ids: Iterable[int]) -> dict[int, NodeRow]:
        """Загрузить узлы пачками, не выполняя SQL для каждого соседа."""
        ids = sorted(set(node_ids))
        result: dict[int, NodeRow] = {}
        for offset in range(0, len(ids), 1000):
            batch = ids[offset : offset + 1000]
            placeholders = ",".join("?" for _ in batch)
            result.update(
                (node.id, node)
                for node in self._nodes(f"id IN ({placeholders})", batch)
            )
        return result

    def keys(self, node_ids: Iterable[int]) -> list[str]:
        """Ключи узлов в исходном порядке; пропавшие узлы пропускаются."""
        ids = list(node_ids)
        nodes = self.nodes_by_ids(ids)
        return [nodes[node_id].key for node_id in ids if node_id in nodes]

    def file_paths(self) -> set[str]:
        """Пути всех файлов индекса."""
        return {str(row[0]) for row in self._db.execute("SELECT path FROM files")}

    def _nodes(self, where: str, params: Sequence[Any]) -> list[NodeRow]:
        rows = self._db.execute(
            f"SELECT {_NODE_COLUMNS} FROM nodes WHERE {where} ORDER BY path, start_line, id",  # noqa: S608  # where — литерал вызывающего
            params,
        )
        return [_node_row(row) for row in rows]


def _node_values(node: Node, file_id: int, codebase: str) -> tuple[Any, ...]:
    return (
        node.id,
        file_id,
        node.kind,
        node.name,
        node.qualified_name,
        node.path,
        codebase,
        node.language,
        node.start_line,
        node.end_line,
        node.signature,
        node.docblock,
        dumps(list(node.attributes)) if node.attributes else None,
        node.flags,
        dumps(node.meta) if node.meta else None,
    )


def _node_row(row: Sequence[Any]) -> NodeRow:
    return NodeRow(
        id=row[0],
        key=row[1],
        file_id=row[2],
        kind=row[3],
        name=row[4],
        qualified_name=row[5],
        path=row[6],
        codebase=row[7],
        language=row[8],
        start_line=row[9],
        end_line=row[10],
        signature=row[11],
        flags=row[12],
        meta=json.loads(row[13]) if row[13] else {},
    )


def _ref_row(row: Sequence[Any]) -> RefRow:
    return RefRow(
        id=row[0],
        source=row[1],
        file_id=row[2],
        path=row[3],
        name=row[4],
        kind=row[5],
        line=row[6],
        col=row[7],
        context=json.loads(row[8]),
        status=row[9],
        target=row[10],
    )
