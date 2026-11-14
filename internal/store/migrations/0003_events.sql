CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    chat_id INTEGER,
    created_at INTEGER NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    dedup_key TEXT UNIQUE
) STRICT;

CREATE INDEX events_chat_id_idx ON events(chat_id);
CREATE INDEX events_created_at_idx ON events(created_at);
