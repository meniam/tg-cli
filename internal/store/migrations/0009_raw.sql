ALTER TABLE chat_versions ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));
ALTER TABLE dialog_state ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));
ALTER TABLE account_sessions ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));
ALTER TABLE blocked_peers ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));
ALTER TABLE notify_defaults ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));
ALTER TABLE events ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));
ALTER TABLE transcriptions ADD COLUMN raw TEXT CHECK (raw IS NULL OR json_valid(raw));

ALTER TABLE people ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1));
UPDATE people SET deleted = 1 WHERE raw IS NOT NULL AND json_extract(raw, '$.deleted') = 1;
UPDATE people SET raw = NULL WHERE raw IS NOT NULL AND json_type(raw, '$._') IS NULL;

ALTER TABLE folders ADD COLUMN data TEXT CHECK (data IS NULL OR json_valid(data));
UPDATE folders SET data = raw, raw = NULL;

CREATE TABLE message_versions_new (
    chat_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    version INTEGER NOT NULL,
    text TEXT NOT NULL,
    edited_at INTEGER,
    raw TEXT CHECK (raw IS NULL OR json_valid(raw)),
    saved_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, message_id, version)
) STRICT;
INSERT INTO message_versions_new (chat_id, message_id, version, text, edited_at, raw, saved_at)
SELECT chat_id, message_id, version, text, edited_at,
       CASE WHEN raw IS NULL OR json_valid(raw) THEN raw END, saved_at
FROM message_versions;
DROP TABLE message_versions;
ALTER TABLE message_versions_new RENAME TO message_versions;

DROP TRIGGER chats_versions_au;
CREATE TRIGGER chats_versions_au AFTER UPDATE ON chats
WHEN (old.type IS NOT NULL AND old.type != '' AND old.type IS NOT new.type)
  OR (old.title IS NOT NULL AND old.title != '' AND old.title IS NOT new.title)
  OR (old.username IS NOT NULL AND old.username IS NOT new.username)
  OR (old.about IS NOT NULL AND old.about IS NOT new.about)
  OR (old.settings IS NOT NULL AND old.settings IS NOT new.settings)
BEGIN
    INSERT INTO chat_versions(chat_id,version,type,title,username,about,settings,raw,observed_at)
    VALUES(old.id,COALESCE((SELECT MAX(version) FROM chat_versions WHERE chat_id=old.id),0)+1,
           old.type,old.title,old.username,old.about,old.settings,old.raw,new.last_seen_at);
END;
