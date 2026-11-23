-- A deleted account is told by the object Telegram sent (`"deleted":true` in
-- people.raw, keyed by the method), but the people.deleted column did not follow it.
-- Fill the column and mark the stored messages of those people.
UPDATE people SET deleted = 1
WHERE deleted = 0
  AND (json_extract(people.raw, '$.deleted') = 1
       OR EXISTS (SELECT 1 FROM json_each(people.raw) j
                  WHERE json_valid(j.value) AND json_type(j.value) = 'object' AND json_extract(j.value, '$.deleted') = 1));

UPDATE messages SET sender_deleted = 1
WHERE sender_deleted = 0
  AND sender_id IN (SELECT id FROM people WHERE deleted = 1);

-- Names that reached people after migration 0015 (a live read of one message
-- names the person): fill the older rows of those people the same way.
UPDATE messages
SET sender_name = (
    SELECT COALESCE(
        NULLIF(TRIM(COALESCE(p.first_name, '') || ' ' || COALESCE(p.last_name, '')), ''),
        NULLIF(p.username, ''))
    FROM people p
    WHERE p.id = messages.sender_id)
WHERE (sender_name IS NULL OR sender_name = '')
  AND sender_id > 0
  AND EXISTS (SELECT 1 FROM people p
              WHERE p.id = messages.sender_id
                AND (TRIM(COALESCE(p.first_name, '') || ' ' || COALESCE(p.last_name, '')) <> ''
                     OR COALESCE(p.username, '') <> ''));
