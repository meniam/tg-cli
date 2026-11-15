package store

import (
	"context"
	"database/sql"
	"errors"
)

// Transcription is the persisted result for one message.
type Transcription struct {
	ChatID          int64
	MessageID       int
	TranscriptionID int64
	Text            string
	Pending         bool
	UpdatedAt       int64
}

// GetTranscription retrieves a previously requested transcription.
func (db *DB) GetTranscription(ctx context.Context, chatID int64, messageID int) (Transcription, error) {
	var t Transcription
	err := db.read.QueryRowContext(ctx, `SELECT chat_id,message_id,transcription_id,text,pending,updated_at FROM transcriptions WHERE chat_id=? AND message_id=?`, chatID, messageID).Scan(&t.ChatID, &t.MessageID, &t.TranscriptionID, &t.Text, &t.Pending, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Transcription{}, ErrNotFound
	}
	return t, err
}

// PutTranscription records the result of an explicit request.
func (db *DB) PutTranscription(ctx context.Context, t Transcription) error {
	_, err := db.write.ExecContext(ctx, `INSERT INTO transcriptions(chat_id,message_id,transcription_id,text,pending,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(chat_id,message_id) DO UPDATE SET transcription_id=excluded.transcription_id,text=excluded.text,pending=excluded.pending,updated_at=excluded.updated_at`, t.ChatID, t.MessageID, t.TranscriptionID, t.Text, t.Pending, db.now().Unix())
	return err
}

// UpdateTranscription applies an update only to the matching requested result.
func (db *DB) UpdateTranscription(ctx context.Context, t Transcription) (bool, error) {
	r, err := db.write.ExecContext(ctx, `UPDATE transcriptions SET text=?,pending=?,updated_at=? WHERE chat_id=? AND message_id=? AND transcription_id=? AND pending=1`, t.Text, t.Pending, db.now().Unix(), t.ChatID, t.MessageID, t.TranscriptionID)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n != 0, err
}
