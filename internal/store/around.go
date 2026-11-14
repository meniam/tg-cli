package store

import (
	"context"
	"errors"
	"fmt"
)

// MessagesAround returns a locally stored, nondeleted window in ID order.
func (db *DB) MessagesAround(ctx context.Context, chatID int64, id, before, after int) ([]Message, error) {
	anchor, err := db.GetMessage(ctx, chatID, id)
	if err != nil {
		return nil, err
	}
	if anchor.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if before < 0 || after < 0 {
		return nil, errors.New("context window must not be negative")
	}
	query := func(sqlText string, args ...any) ([]Message, error) {
		rows, err := db.read.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Message
		for rows.Next() {
			message, err := scanMessage(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, message)
		}
		return out, rows.Err()
	}
	older, err := query(`SELECT `+messageColumns+` FROM messages WHERE chat_id=? AND id<=? AND deleted_at IS NULL ORDER BY id DESC LIMIT ?`, chatID, id, before+1)
	if err != nil {
		return nil, fmt.Errorf("read older local messages: %w", err)
	}
	newer, err := query(`SELECT `+messageColumns+` FROM messages WHERE chat_id=? AND id>? AND deleted_at IS NULL ORDER BY id ASC LIMIT ?`, chatID, id, after)
	if err != nil {
		return nil, fmt.Errorf("read newer local messages: %w", err)
	}
	for i, j := 0, len(older)-1; i < j; i, j = i+1, j-1 {
		older[i], older[j] = older[j], older[i]
	}
	return append(older, newer...), nil
}
