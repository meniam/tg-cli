package store

import (
	"context"
	"fmt"
)

// ChatKnown reports whether local chat-owned data exists for a numeric ID.
// Messages and notes may exist even when no chat row was loaded.
func (db *DB) ChatKnown(ctx context.Context, id int64) (bool, error) {
	var known bool
	err := db.read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chats WHERE id=?) OR EXISTS(SELECT 1 FROM messages WHERE chat_id=?) OR EXISTS(SELECT 1 FROM notes WHERE chat_id=?)`, id, id, id).Scan(&known)
	if err != nil {
		return false, fmt.Errorf("check local chat: %w", err)
	}
	return known, nil
}
