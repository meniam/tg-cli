-- The author of a message may be a deleted account: Telegram sends such a user
-- without a name. 1 marks a message of a person known to be deleted.
ALTER TABLE messages ADD COLUMN sender_deleted INTEGER NOT NULL DEFAULT 0 CHECK (sender_deleted IN (0, 1));
UPDATE messages SET sender_deleted = 1
WHERE sender_id IN (SELECT id FROM people WHERE deleted = 1);
