CREATE TABLE chats (
    id INTEGER PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('user', 'bot', 'group', 'supergroup', 'channel')),
    title TEXT NOT NULL DEFAULT '',
    username TEXT,
    about TEXT,
    members INTEGER,
    forum INTEGER NOT NULL DEFAULT 0,
    verified INTEGER NOT NULL DEFAULT 0,
    unread INTEGER,
    settings TEXT CHECK (settings IS NULL OR json_valid(settings)),
    raw TEXT CHECK (raw IS NULL OR json_valid(raw)),
    search_text TEXT NOT NULL DEFAULT '',
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
) STRICT;

CREATE INDEX chats_username_idx ON chats(username);

CREATE TABLE people (
    id INTEGER PRIMARY KEY,
    username TEXT,
    first_name TEXT,
    last_name TEXT,
    phone TEXT,
    bio TEXT,
    is_bot INTEGER NOT NULL DEFAULT 0,
    is_contact INTEGER NOT NULL DEFAULT 0,
    raw TEXT CHECK (raw IS NULL OR json_valid(raw)),
    search_text TEXT NOT NULL DEFAULT '',
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
) STRICT;

CREATE INDEX people_username_idx ON people(username);
CREATE INDEX people_is_contact_idx ON people(is_contact);

CREATE TABLE messages (
    chat_id INTEGER NOT NULL,
    id INTEGER NOT NULL,
    date INTEGER NOT NULL,
    sender_id INTEGER,
    sender_name TEXT,
    text TEXT NOT NULL DEFAULT '',
    out INTEGER NOT NULL DEFAULT 0,
    reply_to INTEGER,
    topic_id INTEGER,
    media_type TEXT,
    edited_at INTEGER,
    pinned INTEGER NOT NULL DEFAULT 0,
    service TEXT,
    raw TEXT NOT NULL CHECK (json_valid(raw)),
    search_text TEXT NOT NULL DEFAULT '',
    deleted_at INTEGER,
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    UNIQUE (chat_id, id)
) STRICT;

CREATE INDEX messages_chat_date_idx ON messages(chat_id, date DESC);

CREATE TABLE message_versions (
    chat_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    version INTEGER NOT NULL,
    text TEXT NOT NULL,
    edited_at INTEGER,
    raw TEXT,
    saved_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, message_id, version)
) STRICT;

CREATE TABLE folders (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    raw TEXT CHECK (raw IS NULL OR json_valid(raw)),
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
) STRICT;

CREATE TABLE folder_chats (
    folder_id INTEGER NOT NULL,
    chat_id INTEGER NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (folder_id, chat_id)
) STRICT;

CREATE TABLE account_state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL CHECK (json_valid(value)),
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE sync_state (
    chat_id INTEGER PRIMARY KEY,
    oldest_id INTEGER,
    newest_id INTEGER,
    complete INTEGER NOT NULL DEFAULT 0,
    synced_at INTEGER
) STRICT;

CREATE TABLE notes (
    id INTEGER PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('chat', 'person', 'message')),
    chat_id INTEGER,
    target_id INTEGER,
    body TEXT NOT NULL,
    author TEXT NOT NULL CHECK (author IN ('owner', 'ai')),
    model TEXT,
    search_text TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (kind = 'chat' AND chat_id IS NOT NULL AND target_id IS NULL) OR
        (kind = 'person' AND chat_id IS NULL AND target_id IS NOT NULL) OR
        (kind = 'message' AND chat_id IS NOT NULL AND target_id IS NOT NULL)
    ),
    CHECK (model IS NULL OR author = 'ai')
) STRICT;

CREATE INDEX notes_target_idx ON notes(kind, chat_id, target_id);

CREATE TABLE note_tags (
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY (note_id, tag)
) STRICT;

CREATE VIRTUAL TABLE chats_fts USING fts5(search_text, content='chats', content_rowid='rowid', tokenize='unicode61');
CREATE VIRTUAL TABLE people_fts USING fts5(search_text, content='people', content_rowid='rowid', tokenize='unicode61');
CREATE VIRTUAL TABLE messages_fts USING fts5(search_text, content='messages', content_rowid='rowid', tokenize='unicode61');
CREATE VIRTUAL TABLE notes_fts USING fts5(search_text, content='notes', content_rowid='rowid', tokenize='unicode61');

CREATE TRIGGER chats_ai AFTER INSERT ON chats BEGIN
    INSERT INTO chats_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER chats_au AFTER UPDATE OF search_text ON chats BEGIN
    INSERT INTO chats_fts(chats_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
    INSERT INTO chats_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER chats_ad AFTER DELETE ON chats BEGIN
    INSERT INTO chats_fts(chats_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
END;

CREATE TRIGGER people_ai AFTER INSERT ON people BEGIN
    INSERT INTO people_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER people_au AFTER UPDATE OF search_text ON people BEGIN
    INSERT INTO people_fts(people_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
    INSERT INTO people_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER people_ad AFTER DELETE ON people BEGIN
    INSERT INTO people_fts(people_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
END;

CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER messages_au AFTER UPDATE OF search_text ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
    INSERT INTO messages_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
END;

CREATE TRIGGER notes_ai AFTER INSERT ON notes BEGIN
    INSERT INTO notes_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER notes_au AFTER UPDATE OF search_text ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
    INSERT INTO notes_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
CREATE TRIGGER notes_ad AFTER DELETE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
END;
