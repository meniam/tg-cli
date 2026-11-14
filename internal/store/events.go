package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Event is one durable event. ChatID is nil for account-wide events.
type Event struct {
	ID        int64
	Type      string
	ChatID    *int64
	CreatedAt time.Time
	Payload   json.RawMessage
	DedupKey  string
}

// EventFilter restricts replay to these types and chat IDs. Empty lists match all.
type EventFilter struct {
	Types   []string
	ChatIDs []int64
}

// AddEvent stores one event. A repeated nonempty DedupKey returns its original
// ID with inserted=false, so an update replay does not create another event.
func (db *DB) AddEvent(ctx context.Context, event Event) (id int64, inserted bool, err error) {
	if event.Type == "" || !json.Valid(event.Payload) {
		return 0, false, errors.New("event type and JSON payload are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = db.now()
	}
	var key any
	if event.DedupKey != "" {
		key = event.DedupKey
	}
	result, err := db.write.ExecContext(ctx, `INSERT INTO events(type,chat_id,created_at,payload,dedup_key) VALUES(?,?,?,?,?) ON CONFLICT(dedup_key) DO NOTHING`, event.Type, event.ChatID, event.CreatedAt.Unix(), string(event.Payload), key)
	if err != nil {
		return 0, false, fmt.Errorf("add event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("check event insert: %w", err)
	}
	if rows == 0 {
		if err := db.read.QueryRowContext(ctx, `SELECT id FROM events WHERE dedup_key=?`, event.DedupKey).Scan(&id); err != nil {
			return 0, false, fmt.Errorf("find existing event: %w", err)
		}
		return id, false, nil
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("get event id: %w", err)
	}
	return id, true, nil
}

// EventsAfter returns events in ID order. A nonpositive limit defaults to 100;
// at most 1000 are returned by one call.
func (db *DB) EventsAfter(ctx context.Context, afterID int64, filter EventFilter, limit int) ([]Event, error) {
	if afterID < 0 {
		return nil, errors.New("event cursor must not be negative")
	}
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 1000)
	var query strings.Builder
	query.WriteString(`SELECT id,type,chat_id,created_at,payload,dedup_key FROM events WHERE id>?`)
	args := []any{afterID}
	if len(filter.Types) > 0 {
		query.WriteString(` AND type IN (` + placeholders(len(filter.Types)) + `)`)
		for _, typ := range filter.Types {
			args = append(args, typ)
		}
	}
	if len(filter.ChatIDs) > 0 {
		query.WriteString(` AND chat_id IN (` + placeholders(len(filter.ChatIDs)) + `)`)
		for _, chatID := range filter.ChatIDs {
			args = append(args, chatID)
		}
	}
	query.WriteString(` ORDER BY id LIMIT ?`)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		var unix int64
		var payload string
		var key sql.NullString
		if err := rows.Scan(&event.ID, &event.Type, &event.ChatID, &unix, &payload, &key); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		event.CreatedAt = time.Unix(unix, 0).UTC()
		event.Payload = json.RawMessage(payload)
		event.DedupKey = key.String
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return events, nil
}

// LastEventID returns zero when the journal is empty.
func (db *DB) LastEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := db.read.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("last event id: %w", err)
	}
	return id, nil
}

// EventHighWater includes the last real event removed by retention.
func (db *DB) EventHighWater(ctx context.Context) (int64, error) {
	var id int64
	if err := db.read.QueryRowContext(ctx, `SELECT MAX(COALESCE((SELECT MAX(id) FROM events),0), COALESCE((SELECT CAST(value AS INTEGER) FROM account_state WHERE key='events.high_water'),0))`).Scan(&id); err != nil {
		return 0, fmt.Errorf("event high water: %w", err)
	}
	return id, nil
}

// FirstEventID is zero when no retained events exist.
func (db *DB) FirstEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := db.read.QueryRowContext(ctx, `SELECT COALESCE(MIN(id),0) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("first event id: %w", err)
	}
	return id, nil
}

// PruneEvents removes entries older than the cutoff or outside the newest keep.
func (db *DB) PruneEvents(ctx context.Context, olderThan time.Time, keep int) (int, error) {
	if keep < 0 {
		return 0, errors.New("event retention count must not be negative")
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin event prune: %w", err)
	}
	defer tx.Rollback()
	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM events`).Scan(&last); err != nil {
		return 0, fmt.Errorf("read event high water: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(key,value,updated_at) VALUES('events.high_water',?,?) ON CONFLICT(key) DO UPDATE SET value=MAX(CAST(value AS INTEGER), CAST(excluded.value AS INTEGER)),updated_at=excluded.updated_at`, fmt.Sprint(last), db.now().Unix()); err != nil {
		return 0, fmt.Errorf("save event high water: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM events WHERE created_at<? OR id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT ?)`, olderThan.Unix(), keep)
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit event prune: %w", err)
	}
	return int(deleted), nil
}
