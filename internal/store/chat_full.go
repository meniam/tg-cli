package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// fullSyncTypes are the chat types the full-data job covers: a channel and a
// group carry the fields a direct chat does not.
var fullSyncTypes = []string{"group", "supergroup", "channel"}

// FullSyncChat is one chat the full-data job has to read.
type FullSyncChat struct {
	ID    int64
	Type  string
	Title string
}

// ChatsForFullSync returns the chats the job has to read: the groups and channels
// the access lists allow, without a successful full read yet. With refresh it
// returns every allowed group and channel, so a second start reads them again.
// A chat whose last read failed stays in the list: the job reports the failure and
// the next start tries it again.
//
// The filter applies inside the query, so a closed chat is never returned and never
// requested from Telegram.
func (db *DB) ChatsForFullSync(ctx context.Context, filter ChatFilter, refresh bool) ([]FullSyncChat, error) {
	cond, args := filter.condition("id", false)
	query := `SELECT id, type, title FROM chats WHERE type IN ('` + strings.Join(fullSyncTypes, "','") + `')` + cond
	if !refresh {
		query += ` AND full_synced_at IS NULL`
	}
	query += ` ORDER BY id`
	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read chats for the full sync: %w", err)
	}
	defer rows.Close()
	out := []FullSyncChat{}
	for rows.Next() {
		var chat FullSyncChat
		if err := rows.Scan(&chat.ID, &chat.Type, &chat.Title); err != nil {
			return nil, fmt.Errorf("read chats for the full sync: %w", err)
		}
		out = append(out, chat)
	}
	return out, rows.Err()
}

// ChatFull is the result of one full read of a chat.
type ChatFull struct {
	// About is the description Telegram returns for the chat.
	About *string
	// Members is the participant count; nil means Telegram did not give one.
	Members *int64
	// Settings is the settings object of the chat, the same one `chat info`
	// builds.
	Settings json.RawMessage
	// SyncedAt is the time of a successful read in Unix seconds.
	SyncedAt *int64
	// Error is why the last read failed; an empty value clears it.
	Error *string
}

// SaveChatFull writes the result of a full read. A successful read sets
// full_synced_at and clears full_error; a failed one records full_error and leaves
// full_synced_at alone, so the chat stays on the list for the next start.
//
// The columns of the job do not take part in the version trigger, so a repeated
// write of the same values adds no chat_versions row.
func (db *DB) SaveChatFull(ctx context.Context, chatID int64, full ChatFull) error {
	if err := validateOptionalJSON(full.Settings); err != nil {
		return fmt.Errorf("chat %d settings: %w", chatID, err)
	}
	var settings any
	if len(full.Settings) > 0 {
		settings = string(full.Settings)
	}
	result, err := db.write.ExecContext(ctx,
		`UPDATE chats SET about=COALESCE(?,about), members=COALESCE(?,members), settings=COALESCE(?,settings),
		        full_synced_at=COALESCE(?,full_synced_at), full_error=? WHERE id=?`,
		full.About, full.Members, settings, full.SyncedAt, full.Error, chatID)
	if err != nil {
		return fmt.Errorf("save the full chat %d: %w", chatID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save the full chat %d: %w", chatID, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// FullSyncProgress reports how much of the job the database already knows.
type FullSyncProgress struct {
	// Total is the number of chats the job has to read.
	Total int
	// Done is the number of chats with a successful full read.
	Done int
	// Failed is the number of chats whose last read failed.
	Failed int
}

// FullSyncProgress counts the chats of the job.
func (db *DB) FullSyncProgress(ctx context.Context, filter ChatFilter) (FullSyncProgress, error) {
	cond, args := filter.condition("id", false)
	var progress FullSyncProgress
	err := db.read.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN full_synced_at IS NOT NULL THEN 1 ELSE 0 END),0),
		        COALESCE(SUM(CASE WHEN full_synced_at IS NULL AND full_error IS NOT NULL THEN 1 ELSE 0 END),0)
		 FROM chats WHERE type IN ('`+strings.Join(fullSyncTypes, "','")+`')`+cond, args...).
		Scan(&progress.Total, &progress.Done, &progress.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return progress, nil
	}
	if err != nil {
		return progress, fmt.Errorf("count the full sync chats: %w", err)
	}
	return progress, nil
}
