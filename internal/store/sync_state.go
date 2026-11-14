package store

import (
	"context"
	"database/sql"
	"fmt"
)

// SyncProgress is durable history coverage for one chat. Nil IDs mean that no
// storable message has been seen; a completed empty history can have nil IDs.
type SyncProgress struct {
	ChatID   int64
	OldestID *int
	NewestID *int
	Complete bool
	SyncedAt *int64
}

func scanSyncProgress(row rowScanner) (SyncProgress, error) {
	var state SyncProgress
	var oldest, newest, syncedAt sql.NullInt64
	err := row.Scan(&state.ChatID, &oldest, &newest, &state.Complete, &syncedAt)
	if err != nil {
		return SyncProgress{}, err
	}
	if oldest.Valid {
		id := int(oldest.Int64)
		state.OldestID = &id
	}
	if newest.Valid {
		id := int(newest.Int64)
		state.NewestID = &id
	}
	if syncedAt.Valid {
		state.SyncedAt = &syncedAt.Int64
	}
	return state, nil
}

// SyncState returns the saved progress for a chat, or found=false when absent.
func (db *DB) SyncState(ctx context.Context, chatID int64) (state SyncProgress, found bool, err error) {
	state, err = scanSyncProgress(db.read.QueryRowContext(ctx,
		`SELECT chat_id,oldest_id,newest_id,complete,synced_at FROM sync_state WHERE chat_id=?`, chatID))
	if err == sql.ErrNoRows {
		return SyncProgress{}, false, nil
	}
	if err != nil {
		return SyncProgress{}, false, fmt.Errorf("read sync state %d: %w", chatID, err)
	}
	return state, true, nil
}

// SaveHistoryPage stores a complete converted page and its progress atomically.
// complete is supplied by the caller only when the raw Telegram page was empty.
func (db *DB) SaveHistoryPage(ctx context.Context, chatID int64, messages []Message, people []Person, complete bool) (SyncProgress, error) {
	if chatID == 0 {
		return SyncProgress{}, fmt.Errorf("history page chat id must be nonzero")
	}
	last, err := messageLastIndexes(messages, true)
	if err != nil {
		return SyncProgress{}, err
	}
	var oldest, newest *int
	for _, message := range messages {
		if message.ChatID != chatID || message.ID <= 0 || message.Date <= 0 {
			return SyncProgress{}, fmt.Errorf("invalid history message %d/%d", message.ChatID, message.ID)
		}
		if oldest == nil || message.ID < *oldest {
			id := message.ID
			oldest = &id
		}
		if newest == nil || message.ID > *newest {
			id := message.ID
			newest = &id
		}
	}
	for _, person := range people {
		if err := validateOptionalJSON(person.Raw); err != nil {
			return SyncProgress{}, fmt.Errorf("person %d raw: %w", person.ID, err)
		}
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return SyncProgress{}, fmt.Errorf("begin history page: %w", err)
	}
	defer tx.Rollback()
	now := db.now().Unix()
	if len(messages) > 0 {
		if err := upsertMessagesTx(ctx, tx, messages, last, now); err != nil {
			return SyncProgress{}, err
		}
	}
	if len(people) > 0 {
		if err := upsertPeopleTx(ctx, tx, people, now); err != nil {
			return SyncProgress{}, err
		}
	}
	const upsert = `INSERT INTO sync_state(chat_id,oldest_id,newest_id,complete,synced_at) VALUES(?,?,?,?,?)
		ON CONFLICT(chat_id) DO UPDATE SET
		oldest_id=CASE WHEN excluded.oldest_id IS NULL THEN sync_state.oldest_id
			WHEN sync_state.oldest_id IS NULL THEN excluded.oldest_id
			ELSE MIN(sync_state.oldest_id,excluded.oldest_id) END,
		newest_id=CASE WHEN excluded.newest_id IS NULL THEN sync_state.newest_id
			WHEN sync_state.newest_id IS NULL THEN excluded.newest_id
			ELSE MAX(sync_state.newest_id,excluded.newest_id) END,
		complete=MAX(sync_state.complete,excluded.complete),synced_at=excluded.synced_at`
	if _, err := tx.ExecContext(ctx, upsert, chatID, oldest, newest, complete, now); err != nil {
		return SyncProgress{}, fmt.Errorf("save sync state %d: %w", chatID, err)
	}
	state, err := scanSyncProgress(tx.QueryRowContext(ctx,
		`SELECT chat_id,oldest_id,newest_id,complete,synced_at FROM sync_state WHERE chat_id=?`, chatID))
	if err != nil {
		return SyncProgress{}, fmt.Errorf("read saved sync state %d: %w", chatID, err)
	}
	if err := tx.Commit(); err != nil {
		return SyncProgress{}, fmt.Errorf("commit history page: %w", err)
	}
	return state, nil
}

// ResetSyncState removes durable history progress for one chat.
func (db *DB) ResetSyncState(ctx context.Context, chatID int64) error {
	if _, err := db.write.ExecContext(ctx, `DELETE FROM sync_state WHERE chat_id=?`, chatID); err != nil {
		return fmt.Errorf("reset sync state %d: %w", chatID, err)
	}
	return nil
}
