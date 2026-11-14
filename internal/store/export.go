package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MaxExportPageSize bounds one database page before the daemon applies its
// smaller RPC frame budget.
const MaxExportPageSize = 500

// ExportQuery selects locally saved messages in increasing message ID order.
// Zero Since and Until values leave the corresponding date bound open.
type ExportQuery struct {
	ChatID         int64
	After          int
	Since          time.Time
	Until          time.Time
	IncludeDeleted bool
	Limit          int
}

// ExportPage contains one page and whether no matching message follows it.
// Each Message.DeletedAt records whether that saved message was deleted.
type ExportPage struct {
	Messages []Message
	Last     bool
}

// ExportMessages reads one local page without changing the database.
func (db *DB) ExportMessages(ctx context.Context, q ExportQuery) (ExportPage, error) {
	if q.Limit < 1 || q.Limit > MaxExportPageSize {
		return ExportPage{}, fmt.Errorf("export limit must be between 1 and %d", MaxExportPageSize)
	}
	if q.After < 0 {
		return ExportPage{}, errors.New("export cursor must not be negative")
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && q.Since.After(q.Until) {
		return ExportPage{}, errors.New("export since must not be later than until")
	}

	query := `SELECT ` + messageColumns + ` FROM messages WHERE chat_id=? AND id>?`
	args := []any{q.ChatID, q.After}
	if !q.Since.IsZero() {
		query += ` AND date>=?`
		args = append(args, q.Since.Unix())
	}
	if !q.Until.IsZero() {
		query += ` AND date<=?`
		args = append(args, q.Until.Unix())
	}
	if !q.IncludeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY id ASC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return ExportPage{}, fmt.Errorf("read export page: %w", err)
	}
	defer rows.Close()

	page := ExportPage{Messages: make([]Message, 0, q.Limit), Last: true}
	for rows.Next() {
		if len(page.Messages) == q.Limit {
			page.Last = false
			break
		}
		message, err := scanMessage(rows)
		if err != nil {
			return ExportPage{}, fmt.Errorf("scan export message: %w", err)
		}
		page.Messages = append(page.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return ExportPage{}, fmt.Errorf("iterate export page: %w", err)
	}
	return page, nil
}
