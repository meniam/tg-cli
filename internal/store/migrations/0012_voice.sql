-- Local voice transcription: who wrote a transcription, the persistent queue of
-- transcription jobs, the chats marked for it and the daily download budget.
-- The migration only adds; existing transcriptions stay and read as Telegram's.
ALTER TABLE transcriptions ADD COLUMN source TEXT NOT NULL DEFAULT 'telegram' CHECK (source IN ('telegram', 'local'));
ALTER TABLE transcriptions ADD COLUMN engine TEXT NOT NULL DEFAULT '';
ALTER TABLE transcriptions ADD COLUMN duration REAL;

-- A chat marked for transcription of its voice messages (and of videos when
-- video = 1). Private chats need no mark.
CREATE TABLE voice_chats (
    chat_id INTEGER PRIMARY KEY,
    video INTEGER NOT NULL DEFAULT 0 CHECK (video IN (0, 1)),
    enabled_at INTEGER NOT NULL
) STRICT;

-- One row per message. priority: 0 manual, 10 incoming, 20 sync, 30 backfill
-- (smaller runs first). reason is a short machine word, never message text.
CREATE TABLE voice_queue (
    chat_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('voice', 'video_note', 'video')),
    source TEXT NOT NULL CHECK (source IN ('manual', 'incoming', 'sync', 'backfill')),
    priority INTEGER NOT NULL CHECK (priority >= 0),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'done', 'failed', 'skipped')),
    reason TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    bytes INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0),
    enqueued_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER,
    PRIMARY KEY (chat_id, message_id)
) STRICT;

CREATE INDEX voice_queue_pick_idx ON voice_queue(state, priority, enqueued_at);

-- Downloads of one local calendar day (day is YYYY-MM-DD).
CREATE TABLE voice_usage (
    day TEXT PRIMARY KEY,
    files INTEGER NOT NULL DEFAULT 0 CHECK (files >= 0),
    bytes INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0)
) STRICT;
