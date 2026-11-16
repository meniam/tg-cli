ALTER TABLE chats ADD COLUMN created_at INTEGER;
ALTER TABLE chats ADD COLUMN scam INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN fake INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN restricted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN restriction_reason TEXT CHECK (restriction_reason IS NULL OR json_valid(restriction_reason));
ALTER TABLE chats ADD COLUMN full_synced_at INTEGER;
ALTER TABLE chats ADD COLUMN full_error TEXT;

CREATE INDEX IF NOT EXISTS messages_chat_out_idx ON messages(chat_id, out) WHERE deleted_at IS NULL;

-- chats_state is one row per chat with what the local database knows about it:
-- its own fields, the saved dialog state, the number of stored messages, the first
-- and the last stored message and whether the history is loaded completely.
--
-- Every message aggregate is a correlated subquery on the chat id. With the
-- partial index messages_chat_out_idx and the index messages_chat_date_idx each
-- subquery reads only the rows of that chat, so the cost follows the number of
-- chats in the answer, not the size of the messages table: a window function over
-- the whole table was about 14 times slower on 100 000 messages.
CREATE VIEW chats_state AS
SELECT c.id AS chat_id,
       c.type AS type,
       c.title AS title,
       c.username AS username,
       c.created_at AS created_at,
       COALESCE(json_extract(d.data, '$.mute_until'), 0) > CAST(strftime('%s', 'now') AS INTEGER) AS muted,
       json_extract(d.data, '$.mute_until') AS mute_until,
       json_extract(d.data, '$.archived') AS archived,
       json_extract(d.data, '$.pinned') AS pinned,
       json_extract(d.data, '$.unread') AS unread,
       json_extract(d.data, '$.unread_mentions') AS unread_mentions,
       json_extract(d.data, '$.unread_mark') AS unread_mark,
       d.synced_at AS state_synced_at,
       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL) AS messages,
       (SELECT COALESCE(SUM(m.out), 0) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL) AS my_messages,
       (SELECT m.id FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL ORDER BY m.date, m.id LIMIT 1) AS first_message_id,
       (SELECT m.date FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL ORDER BY m.date, m.id LIMIT 1) AS first_message_date,
       (SELECT m.text FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL ORDER BY m.date, m.id LIMIT 1) AS first_message_text,
       (SELECT m.id FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL ORDER BY m.date DESC, m.id DESC LIMIT 1) AS last_message_id,
       (SELECT m.date FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL ORDER BY m.date DESC, m.id DESC LIMIT 1) AS last_message_date,
       (SELECT m.text FROM messages m WHERE m.chat_id = c.id AND m.deleted_at IS NULL ORDER BY m.date DESC, m.id DESC LIMIT 1) AS last_message_text,
       s.complete AS history_complete
FROM chats c
LEFT JOIN dialog_state d ON d.chat_id = c.id
LEFT JOIN sync_state s ON s.chat_id = c.id;
