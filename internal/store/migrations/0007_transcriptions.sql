CREATE TABLE transcriptions (
    chat_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    transcription_id INTEGER NOT NULL,
    text TEXT NOT NULL,
    pending INTEGER NOT NULL CHECK (pending IN (0, 1)),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, message_id)
) STRICT;
