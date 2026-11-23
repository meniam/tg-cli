ALTER TABLE sync_state ADD COLUMN gap_from INTEGER;
ALTER TABLE sync_state ADD COLUMN gap_to INTEGER;

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
       s.first_message_date AS history_first_message_date,
       s.gap_from AS history_gap_from,
       s.gap_to AS history_gap_to
FROM chats c
LEFT JOIN dialog_state d ON d.chat_id = c.id
LEFT JOIN sync_state s ON s.chat_id = c.id;
