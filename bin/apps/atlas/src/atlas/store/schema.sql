-- Схема индекса Atlas. Версия в meta.schema_version; несовместимое изменение
-- пересоздаёт базу, миграций нет: индекс всегда можно собрать заново.
--
-- Рёбра и ссылки хранят целочисленные id узлов и файлов: текстовый ключ узла
-- (kind:path:qualified_name) длинный, и его копии в рёбрах и индексах занимали
-- больше половины базы.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL,
    codebase TEXT NOT NULL,
    scope TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    indexed_at INTEGER NOT NULL,
    parse_errors TEXT,                 -- JSON: частичный разбор не роняет сборку
    generated INTEGER NOT NULL DEFAULT 0, -- создан генератором: в выдаче ниже ручного кода
    search_relations TEXT NOT NULL DEFAULT '' -- нормализованные use/import файла, ссылки Markdown
);

CREATE TABLE IF NOT EXISTS nodes (
    -- AUTOINCREMENT: id удалённого узла не достаётся новому, поэтому старая ссылка
    -- на него не может случайно совпасть с новым ребром.
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,          -- стабильный ключ kind:path:qualified_name
    file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    path TEXT NOT NULL,
    codebase TEXT NOT NULL,
    language TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    signature TEXT,
    docblock TEXT,
    attributes TEXT,                   -- JSON: PHP-атрибуты, TS-декораторы
    flags INTEGER NOT NULL DEFAULT 0,
    meta TEXT                          -- JSON: наследование, типы свойств, экспорты
);

CREATE INDEX IF NOT EXISTS nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS nodes_qualified_name ON nodes(qualified_name);
-- Имена классов PHP нечувствительны к регистру: разрешатель ищет по lower(), и без
-- этого индекса каждый поиск — полный просмотр таблицы узлов.
CREATE INDEX IF NOT EXISTS nodes_lower_qualified_name ON nodes(lower(qualified_name));
CREATE INDEX IF NOT EXISTS nodes_path ON nodes(path);
CREATE INDEX IF NOT EXISTS nodes_file ON nodes(file_id);

CREATE TABLE IF NOT EXISTS edges (
    id INTEGER PRIMARY KEY,
    source INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    line INTEGER NOT NULL DEFAULT 0,
    col INTEGER NOT NULL DEFAULT 0,
    provenance TEXT NOT NULL,
    confidence REAL NOT NULL,
    via TEXT NOT NULL,
    origin_file INTEGER NOT NULL       -- файл, при разборе которого ребро появилось
);

CREATE UNIQUE INDEX IF NOT EXISTS edges_identity ON edges(source, target, kind, line, col);
CREATE INDEX IF NOT EXISTS edges_target ON edges(target, kind);
CREATE INDEX IF NOT EXISTS edges_heuristic ON edges(via) WHERE provenance = 'heuristic';

-- Внешние ссылки сохраняются: новый файл может превратить внешний импорт в локальный.
CREATE TABLE IF NOT EXISTS refs (
    id INTEGER PRIMARY KEY,
    source INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    file_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    name_tail TEXT NOT NULL,           -- последний сегмент: Foo\Bar::baz → baz
    kind TEXT NOT NULL,
    line INTEGER NOT NULL,
    col INTEGER NOT NULL,
    context TEXT NOT NULL,             -- JSON: кандидаты, тип получателя, привязка импорта
    status TEXT NOT NULL,              -- pending | resolved | ambiguous | unresolved | external
    target INTEGER,                    -- узел, в который разрешилась ссылка
    target_file INTEGER,               -- файл цели: его изменение требует переразрешения
    candidates TEXT                    -- JSON: id узлов-кандидатов для ambiguous
);

CREATE INDEX IF NOT EXISTS refs_source ON refs(source);
CREATE INDEX IF NOT EXISTS refs_file ON refs(file_id);
CREATE INDEX IF NOT EXISTS refs_tail ON refs(name_tail) WHERE status != 'resolved';
CREATE INDEX IF NOT EXISTS refs_target_file ON refs(target_file);
CREATE INDEX IF NOT EXISTS refs_pending ON refs(status) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS resolution_dependencies (
    file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    PRIMARY KEY (file_id, key)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS resolution_dependency_key ON resolution_dependencies(key, file_id);

-- Вызовы с путём-литералом из TS/JS: сырьё HTTP-моста. Какие из них запросы, решает мост
-- по имени вызываемой функции, поэтому извлечение не зависит от конфигурации.
CREATE TABLE IF NOT EXISTS http_calls (
    id INTEGER PRIMARY KEY,
    file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    source INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    callee TEXT NOT NULL,
    method TEXT NOT NULL,              -- из литерала method:, иначе пусто
    path TEXT NOT NULL,                -- статическая часть; вставки внутри пути заменены на {}
    line INTEGER NOT NULL,
    col INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS http_calls_file ON http_calls(file_id);

-- Записи полнотекстового поиска: участок файла от якоря до следующего, до 120 строк.
CREATE TABLE IF NOT EXISTS chunks (
    id INTEGER PRIMARY KEY,
    file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,                -- вид узла графа: class, method, file ...; section у Markdown
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    title TEXT NOT NULL,
    symbols TEXT NOT NULL,             -- JSON
    content BLOB NOT NULL,             -- текст куска, сжатый zlib
    search_content BLOB NOT NULL       -- нормализованный текст для ранжирования, сжатый zlib
);

CREATE INDEX IF NOT EXISTS chunks_file ON chunks(file_id);

-- Нормализованный текст записи; исходный лежит в chunks.content. contentless_delete
-- разрешает удалять строки по rowid без исходных значений колонок.
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    search_text,
    content = '',
    contentless_delete = 1,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- mtime каталогов, где лежат индексированные файлы: создание или удаление файла меняет
-- mtime каталога, поэтому новые неотслеживаемые файлы ищутся только в изменившихся
-- каталогах, а не обходом всего дерева.
CREATE TABLE IF NOT EXISTS dirs (
    path TEXT PRIMARY KEY,
    mtime_ns INTEGER NOT NULL
) WITHOUT ROWID;
