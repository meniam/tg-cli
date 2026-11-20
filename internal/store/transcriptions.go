package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Transcription is the persisted result for one message.
type Transcription struct {
	ChatID          int64
	MessageID       int
	TranscriptionID int64
	Text            string
	Pending         bool
	UpdatedAt       int64
	Raw             json.RawMessage
	// Source is who wrote the text: TranscriptionSourceTelegram or
	// TranscriptionSourceLocal. Writing an empty Source stores Telegram's.
	Source string
	// Engine names the local engine (for example "gigaam-v3"); empty for Telegram.
	Engine string
	// Duration is the length of the audio in seconds, nil when unknown.
	Duration *float64
}

// Sources of a transcription text.
const (
	TranscriptionSourceTelegram = "telegram"
	TranscriptionSourceLocal    = "local"
)

// GetTranscription retrieves a previously requested transcription.
func (db *DB) GetTranscription(ctx context.Context, chatID int64, messageID int) (Transcription, error) {
	var t Transcription
	var duration sql.NullFloat64
	err := db.read.QueryRowContext(ctx, `SELECT chat_id,message_id,transcription_id,text,pending,updated_at,source,engine,duration FROM transcriptions WHERE chat_id=? AND message_id=?`, chatID, messageID).Scan(&t.ChatID, &t.MessageID, &t.TranscriptionID, &t.Text, &t.Pending, &t.UpdatedAt, &t.Source, &t.Engine, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return Transcription{}, ErrNotFound
	}
	if duration.Valid {
		t.Duration = &duration.Float64
	}
	return t, err
}

// PutTranscription records the result of an explicit request. A finished text
// also enters the search text of its voice message in the same transaction.
func (db *DB) PutTranscription(ctx context.Context, t Transcription) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transcription save: %w", err)
	}
	defer tx.Rollback()
	path, value := oneRawKey(t.Raw)
	source := t.Source
	if source == "" {
		source = TranscriptionSourceTelegram
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transcriptions(chat_id,message_id,transcription_id,text,pending,updated_at,source,engine,duration,raw) VALUES(?,?,?,?,?,?,?,?,?,CASE WHEN ? IS NULL THEN NULL ELSE json_set('{}', ?, json(?)) END) ON CONFLICT(chat_id,message_id) DO UPDATE SET transcription_id=excluded.transcription_id,text=excluded.text,pending=excluded.pending,updated_at=excluded.updated_at,source=excluded.source,engine=excluded.engine,duration=excluded.duration,raw=CASE WHEN ? IS NULL THEN transcriptions.raw ELSE json_set(COALESCE(transcriptions.raw,'{}'), ?, json(?)) END`, t.ChatID, t.MessageID, t.TranscriptionID, t.Text, t.Pending, db.now().Unix(), source, t.Engine, t.Duration, value, path, value, value, path, value)
	if err != nil {
		return err
	}
	if err := refreshTranscribedSearchText(ctx, tx, t.ChatID, t.MessageID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateTranscription applies an update only to the matching requested result.
// When it applies, the search text of the voice message follows it. An empty
// Source or Engine and a nil Duration keep what the row already has.
func (db *DB) UpdateTranscription(ctx context.Context, t Transcription) (bool, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin transcription update: %w", err)
	}
	defer tx.Rollback()
	path, value := oneRawKey(t.Raw)
	r, err := tx.ExecContext(ctx, `UPDATE transcriptions SET text=?,pending=?,updated_at=?,source=COALESCE(NULLIF(?,''),source),engine=COALESCE(NULLIF(?,''),engine),duration=COALESCE(?,duration),raw=CASE WHEN ? IS NULL THEN raw ELSE json_set(COALESCE(raw,'{}'), ?, json(?)) END WHERE chat_id=? AND message_id=? AND transcription_id=? AND pending=1`, t.Text, t.Pending, db.now().Unix(), t.Source, t.Engine, t.Duration, value, path, value, t.ChatID, t.MessageID, t.TranscriptionID)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if err := refreshTranscribedSearchText(ctx, tx, t.ChatID, t.MessageID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// FinishedTranscriptions returns the finished transcriptions (not pending ones)
// of the given messages of one chat, by message id. A message with no
// transcription, or with one still pending, is absent from the map. A text may
// be empty: a recording the engine found no speech in. It reads the database
// only.
func (db *DB) FinishedTranscriptions(ctx context.Context, chatID int64, messageIDs []int) (map[int]Transcription, error) {
	found := make(map[int]Transcription, len(messageIDs))
	const chunk = 400
	for start := 0; start < len(messageIDs); start += chunk {
		part := messageIDs[start:min(start+chunk, len(messageIDs))]
		args := make([]any, 0, len(part)+1)
		args = append(args, chatID)
		for _, id := range part {
			args = append(args, id)
		}
		rows, err := db.read.QueryContext(ctx, `SELECT message_id,text,source,engine FROM transcriptions WHERE chat_id=? AND pending=0 AND message_id IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("read transcriptions: %w", err)
		}
		for rows.Next() {
			t := Transcription{ChatID: chatID}
			if err := rows.Scan(&t.MessageID, &t.Text, &t.Source, &t.Engine); err != nil {
				rows.Close()
				return nil, fmt.Errorf("read transcriptions: %w", err)
			}
			found[t.MessageID] = t
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("read transcriptions: %w", err)
		}
	}
	return found, nil
}
