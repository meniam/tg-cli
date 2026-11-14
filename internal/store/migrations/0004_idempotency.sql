CREATE TABLE idempotency (
    chat_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    method TEXT NOT NULL,
    params_hash BLOB NOT NULL,
    result BLOB,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, key)
);
CREATE INDEX idempotency_created_at ON idempotency(created_at);
