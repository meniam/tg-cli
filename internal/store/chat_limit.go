package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const chatMessageLimitPrefix = "chat.message_limit."

// Bounds of a per-chat message limit. The floor keeps a typo from wiping a chat.
const (
	MinChatMessageLimit = 100
	MaxChatMessageLimit = 100000000
	// ChatLimitKeepPercent is the share of the limit that a cleanup leaves, so a
	// cleanup does not run again after every new message.
	ChatLimitKeepPercent = 95
	chatTrimBatch        = 2000
)

// ChatLimitKeep returns how many messages a cleanup leaves for a limit.
func ChatLimitKeep(limit int) int { return limit * ChatLimitKeepPercent / 100 }

func chatMessageLimitKey(chatID int64) string {
	return chatMessageLimitPrefix + strconv.FormatInt(chatID, 10)
}

// ChatMessageLimit returns the message limit of a chat, or found=false when the
// chat has none.
func (db *DB) ChatMessageLimit(ctx context.Context, chatID int64) (limit int, found bool, err error) {
	raw, ok, err := db.GetAccountState(ctx, chatMessageLimitKey(chatID))
	if err != nil || !ok {
		return 0, false, err
	}
	if err := json.Unmarshal(raw, &limit); err != nil || limit < MinChatMessageLimit {
		return 0, false, nil
	}
	return limit, true, nil
}

// SetChatMessageLimit stores the limit of a chat.
func (db *DB) SetChatMessageLimit(ctx context.Context, chatID int64, limit int) error {
	if chatID == 0 {
		return fmt.Errorf("message limit chat id must be nonzero")
	}
	if limit < MinChatMessageLimit || limit > MaxChatMessageLimit {
		return fmt.Errorf("message limit must be between %d and %d", MinChatMessageLimit, MaxChatMessageLimit)
	}
	value, _ := json.Marshal(limit)
	return db.SetAccountState(ctx, chatMessageLimitKey(chatID), value)
}

// ClearChatMessageLimit removes the limit and reports whether the chat had one.
func (db *DB) ClearChatMessageLimit(ctx context.Context, chatID int64) (bool, error) {
	res, err := db.write.ExecContext(ctx, `DELETE FROM account_state WHERE key=?`, chatMessageLimitKey(chatID))
	if err != nil {
		return false, fmt.Errorf("clear message limit %d: %w", chatID, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ChatMessageLimitEntry is one chat with a limit.
type ChatMessageLimitEntry struct {
	ChatID int64
	Limit  int
}

// ListChatMessageLimits returns every stored limit ordered by chat id.
func (db *DB) ListChatMessageLimits(ctx context.Context) ([]ChatMessageLimitEntry, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT key,value FROM account_state WHERE key LIKE ?`, chatMessageLimitPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list message limits: %w", err)
	}
	defer rows.Close()
	var out []ChatMessageLimitEntry
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("list message limits: %w", err)
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, chatMessageLimitPrefix), 10, 64)
		var limit int
		if err != nil || json.Unmarshal([]byte(value), &limit) != nil || limit < MinChatMessageLimit {
			continue
		}
		out = append(out, ChatMessageLimitEntry{ChatID: id, Limit: limit})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list message limits: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
	return out, nil
}

// CountChatMessages returns how many message rows a chat holds, messages marked
// deleted included: they take room in the database too.
func (db *DB) CountChatMessages(ctx context.Context, chatID int64) (int, error) {
	var n int
	if err := db.read.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE chat_id=?`, chatID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count messages %d: %w", chatID, err)
	}
	return n, nil
}

// TrimChatMessages cuts a chat down to ChatLimitKeep(limit) messages when it
// holds more than limit, and returns how many messages it removed. The oldest
// messages go first, by date then id, together with their versions,
// transcriptions and finished voice jobs. Notes stay. It works in batches, each
// in its own transaction, so the writer is never held for long. A cancelled
// context stops it between batches; what was removed stays removed.
func (db *DB) TrimChatMessages(ctx context.Context, chatID int64, limit int) (int, error) {
	if limit < MinChatMessageLimit {
		return 0, fmt.Errorf("message limit must be at least %d", MinChatMessageLimit)
	}
	count, err := db.CountChatMessages(ctx, chatID)
	if err != nil || count <= limit {
		return 0, err
	}
	keep := ChatLimitKeep(limit)
	var boundDate int64
	var boundID int
	err = db.read.QueryRowContext(ctx,
		`SELECT date,id FROM messages WHERE chat_id=? ORDER BY date DESC, id DESC LIMIT 1 OFFSET ?`,
		chatID, keep-1).Scan(&boundDate, &boundID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find cleanup boundary %d: %w", chatID, err)
	}
	removed := 0
	for {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		n, err := db.trimBatch(ctx, chatID, boundDate, boundID)
		removed += n
		if err != nil {
			return removed, err
		}
		if n < chatTrimBatch {
			return removed, nil
		}
	}
}

// trimBatch removes up to chatTrimBatch messages older than (date, id).
func (db *DB) trimBatch(ctx context.Context, chatID, date int64, id int) (int, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin cleanup %d: %w", chatID, err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM messages WHERE chat_id=? AND (date,id) < (?,?) ORDER BY date, id LIMIT ?`,
		chatID, date, id, chatTrimBatch)
	if err != nil {
		return 0, fmt.Errorf("select cleanup batch %d: %w", chatID, err)
	}
	var ids []any
	for rows.Next() {
		var messageID int
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("select cleanup batch %d: %w", chatID, err)
		}
		ids = append(ids, messageID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("select cleanup batch %d: %w", chatID, err)
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}
	in := placeholders(len(ids))
	args := append([]any{chatID}, ids...)
	for _, q := range []string{
		`DELETE FROM message_versions WHERE chat_id=? AND message_id IN (` + in + `)`,
		`DELETE FROM transcriptions WHERE chat_id=? AND message_id IN (` + in + `)`,
		`DELETE FROM voice_queue WHERE chat_id=? AND state<>'running' AND message_id IN (` + in + `)`,
		`DELETE FROM messages WHERE chat_id=? AND id IN (` + in + `)`,
	} {
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return 0, fmt.Errorf("cleanup %d: %w", chatID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit cleanup %d: %w", chatID, err)
	}
	return len(ids), nil
}
