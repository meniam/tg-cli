package store

import (
	"context"
	"fmt"
	"time"
)

// HistoryGroupCount is the history load of one group of chats: how many chats
// the group has and how many of them are loaded to the first message.
type HistoryGroupCount struct {
	Total    int
	Complete int
}

// DashboardStats are the database counters of the dashboard. Chats and messages
// follow the access filter; the event counters cover the whole table.
type DashboardStats struct {
	// Chats and Messages count the visible chats and the messages in them that
	// are not marked deleted.
	Chats    int64
	Messages int64
	// History is indexed by BackgroundGroupPeople, BackgroundGroupFolders and
	// BackgroundGroupOther: the same groups the scheduled load picks from.
	History [3]HistoryGroupCount
	// EventsSince counts the events created at or after the time given to
	// DashboardStats; EventsTotal counts every stored event.
	EventsSince int64
	EventsTotal int64
}

// DashboardStats reads the counters of the dashboard. The access filter applies
// inside every query, so a closed chat is in no counter. It only reads.
func (db *DB) DashboardStats(ctx context.Context, filter ChatFilter, eventsSince time.Time) (DashboardStats, error) {
	var out DashboardStats
	cond, args := filter.condition("c.id", false)
	rows, err := db.read.QueryContext(ctx, `SELECT `+backgroundGroupSQL+` AS grp, COUNT(*), COALESCE(SUM(s.complete = 1), 0)
	FROM chats c
	LEFT JOIN sync_state s ON s.chat_id = c.id
	WHERE 1=1`+cond+`
	GROUP BY grp`, args...)
	if err != nil {
		return out, fmt.Errorf("count the history of chats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var group int
		var count HistoryGroupCount
		if err := rows.Scan(&group, &count.Total, &count.Complete); err != nil {
			return out, fmt.Errorf("count the history of chats: %w", err)
		}
		if group < 0 || group >= len(out.History) {
			continue
		}
		out.History[group] = count
		out.Chats += int64(count.Total)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("count the history of chats: %w", err)
	}
	if err := db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m JOIN chats c ON c.id = m.chat_id
	WHERE m.deleted_at IS NULL`+cond, args...).Scan(&out.Messages); err != nil {
		return out, fmt.Errorf("count messages: %w", err)
	}
	if err := db.read.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(created_at >= ?), 0) FROM events`, eventsSince.Unix()).Scan(&out.EventsTotal, &out.EventsSince); err != nil {
		return out, fmt.Errorf("count events: %w", err)
	}
	return out, nil
}
