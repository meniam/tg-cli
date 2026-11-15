CREATE TABLE chat_versions (
    chat_id INTEGER NOT NULL, version INTEGER NOT NULL, type TEXT, title TEXT,
    username TEXT, about TEXT, settings TEXT CHECK (settings IS NULL OR json_valid(settings)),
    observed_at INTEGER NOT NULL, PRIMARY KEY (chat_id, version)
) STRICT;
CREATE TRIGGER chats_versions_au AFTER UPDATE ON chats
WHEN (old.type IS NOT NULL AND old.type != '' AND old.type IS NOT new.type)
  OR (old.title IS NOT NULL AND old.title != '' AND old.title IS NOT new.title)
  OR (old.username IS NOT NULL AND old.username IS NOT new.username)
  OR (old.about IS NOT NULL AND old.about IS NOT new.about)
  OR (old.settings IS NOT NULL AND old.settings IS NOT new.settings)
BEGIN
    INSERT INTO chat_versions(chat_id,version,type,title,username,about,settings,observed_at)
    VALUES(old.id,COALESCE((SELECT MAX(version) FROM chat_versions WHERE chat_id=old.id),0)+1,
           old.type,old.title,old.username,old.about,old.settings,new.last_seen_at);
END;
