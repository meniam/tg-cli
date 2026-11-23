-- A message of a basic group arrives as a short update that carries the author's
-- id but not the name. Fill the missing names from the people table: the name is
-- first and last name, then the username, as for a user in a history page.
-- search_text holds only the message text, so no index needs a rebuild.
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
