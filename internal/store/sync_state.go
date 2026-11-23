package store

import (
	"context"
	"database/sql"
	"fmt"
)

// syncProgressColumns lists the sync_state columns scanSyncProgress reads.
const syncProgressColumns = `chat_id,oldest_id,newest_id,complete,synced_at,first_message_date,gap_from,gap_to`

// SyncProgress is durable history coverage for one chat. Nil IDs mean that no
// storable message has been seen; a completed empty history can have nil IDs.
// FirstMessageDate is the Unix time of the first message of the chat. It is set
// together with Complete and stays nil while the load has not reached the start
// of the chat, and for a completed chat without stored messages.
type SyncProgress struct {
	ChatID           int64
	OldestID         *int
	NewestID         *int
	Complete         bool
	SyncedAt         *int64
	FirstMessageDate *int64
	GapFrom          *int
	GapTo            *int
}

func scanSyncProgress(row rowScanner) (SyncProgress, error) {
	var state SyncProgress
	var oldest, newest, syncedAt, firstDate, gapFrom, gapTo sql.NullInt64
	err := row.Scan(&state.ChatID, &oldest, &newest, &state.Complete, &syncedAt, &firstDate, &gapFrom, &gapTo)
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
	if firstDate.Valid {
		state.FirstMessageDate = &firstDate.Int64
	}
	if gapFrom.Valid {
		id := int(gapFrom.Int64)
		state.GapFrom = &id
	}
	if gapTo.Valid {
		id := int(gapTo.Int64)
		state.GapTo = &id
	}
	return state, nil
}

// SyncState returns the saved progress for a chat, or found=false when absent.
func (db *DB) SyncState(ctx context.Context, chatID int64) (state SyncProgress, found bool, err error) {
	state, err = scanSyncProgress(db.read.QueryRowContext(ctx,
		`SELECT `+syncProgressColumns+` FROM sync_state WHERE chat_id=?`, chatID))
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
// The transaction that sets complete also saves first_message_date, the earliest
// date among the stored messages of the chat, deleted ones included; a chat that
// was already complete keeps its date.
func (db *DB) SaveHistoryPage(ctx context.Context, chatID int64, messages []Message, people []Person, complete bool, gapPass GapPass) (SyncProgress, error) {
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
	var gapFrom, gapTo sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT gap_from,gap_to FROM sync_state WHERE chat_id=?`, chatID).Scan(&gapFrom, &gapTo)
	if err != nil && err != sql.ErrNoRows {
		return SyncProgress{}, fmt.Errorf("read gap: %w", err)
	}
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
	if gapPass.Active && gapPass.Narrow && (!gapFrom.Valid || !gapTo.Valid) {
		state, err := scanSyncProgress(tx.QueryRowContext(ctx, `SELECT `+syncProgressColumns+` FROM sync_state WHERE chat_id=?`, chatID))
		if err != nil && err != sql.ErrNoRows {
			return SyncProgress{}, fmt.Errorf("read sync state %d: %w", chatID, err)
		}
		if err := tx.Commit(); err != nil {
			return SyncProgress{}, fmt.Errorf("commit history page: %w", err)
		}
		return state, nil
	}
	pass := gapPass.Active && gapFrom.Valid && gapTo.Valid && (gapPass.Start == 0 || gapPass.Start >= int(gapTo.Int64))
	closed := pass && (complete || oldest != nil && *oldest <= int(gapFrom.Int64))
	pageOldest, pageNewest := oldest, newest
	if pass {
		pageOldest = nil
		pageNewest = nil
	}
	if gapFrom.Valid && pageNewest != nil && *pageNewest > int(gapFrom.Int64) {
		if gapFrom.Int64 == 0 {
			pageNewest = nil
		} else {
			id := int(gapFrom.Int64)
			pageNewest = &id
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
	if _, err := tx.ExecContext(ctx, upsert, chatID, pageOldest, pageNewest, complete && !pass, now); err != nil {
		return SyncProgress{}, fmt.Errorf("save sync state %d: %w", chatID, err)
	}
	if closed {
		top := max(gapPass.Top, gapPass.Start)
		if _, err := tx.ExecContext(ctx, `UPDATE sync_state SET gap_from=NULL,gap_to=NULL,newest_id=CASE WHEN ?>0 THEN MAX(COALESCE(newest_id,0),?) ELSE newest_id END WHERE chat_id=?`, top, top, chatID); err != nil {
			return SyncProgress{}, fmt.Errorf("close gap: %w", err)
		}
	} else if pass && gapPass.Narrow && oldest != nil && *oldest < int(gapTo.Int64) && *oldest > int(gapFrom.Int64) {
		if _, err := tx.ExecContext(ctx, `UPDATE sync_state SET gap_to=? WHERE chat_id=?`, *oldest, chatID); err != nil {
			return SyncProgress{}, fmt.Errorf("narrow gap: %w", err)
		}
	}
	if complete && !pass {
		const firstDate = `UPDATE sync_state
			SET first_message_date=(SELECT MIN(date) FROM messages WHERE chat_id=sync_state.chat_id)
			WHERE chat_id=? AND first_message_date IS NULL`
		if _, err := tx.ExecContext(ctx, firstDate, chatID); err != nil {
			return SyncProgress{}, fmt.Errorf("save first message date %d: %w", chatID, err)
		}
	}
	state, err := scanSyncProgress(tx.QueryRowContext(ctx,
		`SELECT `+syncProgressColumns+` FROM sync_state WHERE chat_id=?`, chatID))
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

// MessageBounds returns how many messages of a chat are saved and the Unix time
// of the oldest one. A deleted message does not count. A chat without saved
// messages gives zero and zero. The history load uses it to decide whether a
// depth limit is met.
func (db *DB) MessageBounds(ctx context.Context, chatID int64) (count int, oldest int64, err error) {
	var min sql.NullInt64
	err = db.read.QueryRowContext(ctx,
		`SELECT count(*), min(date) FROM messages WHERE chat_id=? AND deleted_at IS NULL`, chatID).Scan(&count, &min)
	if err != nil {
		return 0, 0, fmt.Errorf("read message bounds %d: %w", chatID, err)
	}
	if min.Valid {
		oldest = min.Int64
	}
	return count, oldest, nil
}

// MarkHistoryLimitReached marks the history of a chat as completely loaded
// because a limit of the load was met, not because the first message was
// reached. first_message_date stays as it is (null for a chat that was not
// complete), since the oldest saved message is not the first one of the chat.
// A chat without a sync_state row gets one.
func (db *DB) MarkHistoryLimitReached(ctx context.Context, chatID int64) (SyncProgress, error) {
	if chatID == 0 {
		return SyncProgress{}, fmt.Errorf("history limit chat id must be nonzero")
	}
	// A new row takes its bounds from the saved messages: a row without them would
	// read as a chat with no message at all.
	const upsert = `INSERT INTO sync_state(chat_id,oldest_id,newest_id,complete,synced_at)
		SELECT ?, MIN(id), MAX(id), 1, ? FROM messages WHERE chat_id=? AND true
		ON CONFLICT(chat_id) DO UPDATE SET complete=1,synced_at=excluded.synced_at`
	if _, err := db.write.ExecContext(ctx, upsert, chatID, db.now().Unix(), chatID); err != nil {
		return SyncProgress{}, fmt.Errorf("save history limit %d: %w", chatID, err)
	}
	state, err := scanSyncProgress(db.write.QueryRowContext(ctx,
		`SELECT `+syncProgressColumns+` FROM sync_state WHERE chat_id=?`, chatID))
	if err != nil {
		return SyncProgress{}, fmt.Errorf("read saved sync state %d: %w", chatID, err)
	}
	return state, nil
}

// GapPass describes a continuous pass through a gap. Active distinguishes a
// pass from an ordinary history page; Start is its initial offset, Top is its
// highest covered ID, and Narrow marks the last page of a limited pass.
type GapPass struct {
	Active bool
	Start  int
	Top    int
	Narrow bool
}

// SaveCatchupPage stores messages and people without changing history coverage.
func (db *DB) SaveCatchupPage(ctx context.Context, chatID int64, messages []Message, people []Person) error {
	if chatID == 0 {
		return fmt.Errorf("history page chat id must be nonzero")
	}
	last, err := messageLastIndexes(messages, true)
	if err != nil {
		return err
	}
	for _, m := range messages {
		if m.ChatID != chatID || m.ID <= 0 || m.Date <= 0 {
			return fmt.Errorf("invalid history message %d/%d", m.ChatID, m.ID)
		}
	}
	for _, p := range people {
		if err := validateOptionalJSON(p.Raw); err != nil {
			return fmt.Errorf("person %d raw: %w", p.ID, err)
		}
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin catchup page: %w", err)
	}
	defer tx.Rollback()
	now := db.now().Unix()
	if len(messages) > 0 {
		if err := upsertMessagesTx(ctx, tx, messages, last, now); err != nil {
			return err
		}
	}
	if len(people) > 0 {
		if err := upsertPeopleTx(ctx, tx, people, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catchup page: %w", err)
	}
	return nil
}

// MarkCatchupGap records the missing interval and merges an existing gap.
func (db *DB) MarkCatchupGap(ctx context.Context, chatID int64, gapTo int, aliveAt int64) error {
	if chatID == 0 || gapTo <= 0 {
		return fmt.Errorf("invalid catchup gap")
	}
	_, err := db.write.ExecContext(ctx, `INSERT INTO sync_state(chat_id,gap_from,gap_to,complete)
		VALUES(?,COALESCE((SELECT MAX(id) FROM messages WHERE chat_id=? AND id<? AND date<=?),0),?,0)
		ON CONFLICT(chat_id) DO UPDATE SET
		gap_from=CASE WHEN sync_state.gap_from IS NULL THEN excluded.gap_from ELSE MIN(sync_state.gap_from,excluded.gap_from) END,
		gap_to=CASE WHEN sync_state.gap_to IS NULL THEN excluded.gap_to ELSE MAX(sync_state.gap_to,excluded.gap_to) END`, chatID, chatID, gapTo, aliveAt, gapTo)
	if err != nil {
		return fmt.Errorf("mark catchup gap: %w", err)
	}
	return nil
}
