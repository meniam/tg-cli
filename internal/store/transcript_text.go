package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tg-cli/internal/textnorm"
)

// A finished transcription is the text of a voice message or a round video: it
// is searchable by words, cut into fragments and shown in search results. The
// message row keeps an empty text for such media, so the transcription is mixed
// in at three places: messages.search_text (word legs), the fragment source and
// the fragment text. Only a transcription that is not pending and not empty
// counts, and only for these two media types.

// transcriptJoin attaches the finished transcription of messages aliased as m
// as t; t.text is NULL when there is none (or the media is something else).
const transcriptJoin = ` LEFT JOIN transcriptions t ON t.chat_id=m.chat_id AND t.message_id=m.id AND t.pending=0 AND t.text<>'' AND m.media_type IN ('voice','video_note')`

// transcribedMedia reports whether a message of this media type can have a
// transcription.
func transcribedMedia(mediaType *string) bool {
	return mediaType != nil && (*mediaType == "voice" || *mediaType == "video_note")
}

// composeMessageText is the text of a message with its finished transcription:
// the caption, a line feed and the transcription; only one of them when the
// other is empty.
func composeMessageText(text, transcript string) string {
	switch {
	case transcript == "":
		return text
	case text == "":
		return transcript
	}
	return text + "\n" + transcript
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readyTranscript returns the finished transcription of a message, empty if
// there is none, it is pending or it is empty.
func readyTranscript(ctx context.Context, q rowQuerier, chatID int64, messageID int) (string, error) {
	var text string
	err := q.QueryRowContext(ctx, `SELECT text FROM transcriptions WHERE chat_id=? AND message_id=? AND pending=0`, chatID, messageID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read transcription %d/%d: %w", chatID, messageID, err)
	}
	return text, nil
}

// applyTranscript recomputes the search text of a message about to be written,
// so that an upsert (a repeated read of the same message from Telegram) does not
// drop the transcription from the word index.
func applyTranscript(ctx context.Context, tx *sql.Tx, m *Message) error {
	if !transcribedMedia(m.MediaType) {
		return nil
	}
	transcript, err := readyTranscript(ctx, tx, m.ChatID, m.ID)
	if err != nil {
		return err
	}
	m.SearchText = textnorm.Normalize(composeMessageText(m.Text, transcript))
	return nil
}

// refreshTranscribedSearchText rewrites messages.search_text of one stored
// message after its transcription changed. The FTS triggers of search_text
// update both word indexes. A missing message or other media is left alone.
func refreshTranscribedSearchText(ctx context.Context, tx *sql.Tx, chatID int64, messageID int) error {
	var text, searchText string
	var mediaType *string
	err := tx.QueryRowContext(ctx, `SELECT text,media_type,search_text FROM messages WHERE chat_id=? AND id=?`, chatID, messageID).Scan(&text, &mediaType, &searchText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read message %d/%d: %w", chatID, messageID, err)
	}
	if !transcribedMedia(mediaType) {
		return nil
	}
	transcript, err := readyTranscript(ctx, tx, chatID, messageID)
	if err != nil {
		return err
	}
	want := textnorm.Normalize(composeMessageText(text, transcript))
	if want == searchText {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET search_text=? WHERE chat_id=? AND id=?`, want, chatID, messageID); err != nil {
		return fmt.Errorf("update search text %d/%d: %w", chatID, messageID, err)
	}
	return nil
}

// backfillTranscribedSearchText brings messages.search_text of voice messages
// that already have a finished transcription in line with it. It runs when the
// database opens, so transcriptions saved before the word index knew about them
// (or by an older program that wrote search_text without them) become
// searchable without a schema migration. It is idempotent and touches only rows
// whose search text differs; transcriptions are few, they are paid commands.
func backfillTranscribedSearchText(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT m.chat_id,m.id FROM transcriptions t JOIN messages m ON m.chat_id=t.chat_id AND m.id=t.message_id WHERE t.pending=0 AND t.text<>'' AND m.media_type IN ('voice','video_note')`)
	if err != nil {
		return fmt.Errorf("list transcribed messages: %w", err)
	}
	type key struct {
		chat int64
		id   int
	}
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.chat, &k.id); err != nil {
			rows.Close()
			return fmt.Errorf("list transcribed messages: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list transcribed messages: %w", err)
	}
	rows.Close()
	if len(keys) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transcript backfill: %w", err)
	}
	defer tx.Rollback()
	for _, k := range keys {
		if err := refreshTranscribedSearchText(ctx, tx, k.chat, k.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transcript backfill: %w", err)
	}
	return nil
}
