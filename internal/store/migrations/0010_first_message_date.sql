-- first_message_date is the Unix time of the first message of a chat, saved in the
-- transaction that sets complete = 1. It stays empty until the history load
-- reaches the start of the chat, and a local deletion of that message does not move it.
ALTER TABLE sync_state ADD COLUMN first_message_date INTEGER;

UPDATE sync_state
SET first_message_date = (SELECT MIN(m.date) FROM messages m WHERE m.chat_id = sync_state.chat_id)
WHERE complete = 1;

-- chats_state gains history_first_message_date next to history_complete. The
-- existing first_message_date column keeps its meaning: the oldest stored message
-- that is not deleted.
DROP VIEW chats_state;
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
       s.complete AS history_complete,
       s.first_message_date AS history_first_message_date
FROM chats c
LEFT JOIN dialog_state d ON d.chat_id = c.id
LEFT JOIN sync_state s ON s.chat_id = c.id;
