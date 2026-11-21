CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL CHECK (json_valid(value)),
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE owner_secret (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE settings_meta (
    key TEXT PRIMARY KEY,
    value TEXT
) STRICT;
