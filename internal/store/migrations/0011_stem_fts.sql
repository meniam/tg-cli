-- Stemmed full-text index over messages. stem_text is derived from search_text
-- by the driver function tg_stem() (registered in internal/store before any
-- connection opens). messages_fts stays: it matches numbers, part numbers and
-- names exactly as written.
ALTER TABLE messages ADD COLUMN stem_text TEXT NOT NULL DEFAULT '';

UPDATE messages SET stem_text = tg_stem(search_text);

CREATE VIRTUAL TABLE messages_stem_fts USING fts5(stem_text, content='messages', content_rowid='rowid', tokenize='unicode61');

INSERT INTO messages_stem_fts(messages_stem_fts) VALUES('rebuild');

-- stem_text is rewritten inside the triggers, which fire only on search_text,
-- so no trigger watches stem_text itself: the FTS delete command always gets
-- the exact value that was indexed (old.stem_text).
CREATE TRIGGER messages_stem_ai AFTER INSERT ON messages BEGIN
    UPDATE messages SET stem_text = tg_stem(new.search_text) WHERE rowid = new.rowid;
    INSERT INTO messages_stem_fts(rowid, stem_text) VALUES (new.rowid, tg_stem(new.search_text));
END;
CREATE TRIGGER messages_stem_au AFTER UPDATE OF search_text ON messages BEGIN
    INSERT INTO messages_stem_fts(messages_stem_fts, rowid, stem_text) VALUES ('delete', old.rowid, old.stem_text);
    UPDATE messages SET stem_text = tg_stem(new.search_text) WHERE rowid = new.rowid;
    INSERT INTO messages_stem_fts(rowid, stem_text) VALUES (new.rowid, tg_stem(new.search_text));
END;
CREATE TRIGGER messages_stem_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_stem_fts(messages_stem_fts, rowid, stem_text) VALUES ('delete', old.rowid, old.stem_text);
END;
