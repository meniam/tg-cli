package store

import (
	"context"
	"fmt"
	"strings"
)

// VoiceListPosition is the place of a job in the listing order: the page that
// follows a job starts after its position. RowID breaks the tie of jobs queued
// in the same second.
type VoiceListPosition struct {
	EnqueuedAt int64
	RowID      int64
}

// VoiceListQuery selects the jobs of the voice queue to list, newest first.
// Empty State and Kind keep every state and kind, a nil ChatID every chat. After
// continues behind the last job of the previous page; nil starts from the
// newest. Limit is the page size and must be positive.
type VoiceListQuery struct {
	State      string
	Kind       string
	ChatID     *int64
	ChatFilter ChatFilter
	Limit      int
	After      *VoiceListPosition
}

// VoiceListEntry is one listed job with the title of its chat, empty when the
// database does not know the chat. Position is the cursor value of the job.
type VoiceListEntry struct {
	VoiceJob
	ChatTitle string
	Position  VoiceListPosition
}

// ValidVoiceState reports whether state is one of the five job states.
func ValidVoiceState(state string) bool {
	switch state {
	case VoiceStateQueued, VoiceStateRunning, VoiceStateDone, VoiceStateFailed, VoiceStateSkipped:
		return true
	}
	return false
}

// ValidVoiceKind reports whether kind is one of the three job kinds.
func ValidVoiceKind(kind string) bool {
	switch kind {
	case VoiceKindVoice, VoiceKindVideoNote, VoiceKindVideo:
		return true
	}
	return false
}

// ListVoiceJobs returns up to q.Limit jobs of the voice queue, newest enqueued
// first, and whether more jobs follow. It reads the queue and the chat title
// only: no message text, no transcription. The chat filter applies before the
// limit, so a closed chat takes no place on a page.
func (db *DB) ListVoiceJobs(ctx context.Context, q VoiceListQuery) ([]VoiceListEntry, bool, error) {
	if q.Limit <= 0 {
		return nil, false, fmt.Errorf("voice list limit must be positive, got %d", q.Limit)
	}
	if q.State != "" && !ValidVoiceState(q.State) {
		return nil, false, fmt.Errorf("invalid voice job state %q", q.State)
	}
	if q.Kind != "" && !ValidVoiceKind(q.Kind) {
		return nil, false, fmt.Errorf("invalid voice job kind %q", q.Kind)
	}
	var query strings.Builder
	query.WriteString(`SELECT q.chat_id,q.message_id,q.kind,q.source,q.priority,q.state,q.reason,q.attempts,q.bytes,q.enqueued_at,q.started_at,q.finished_at,COALESCE(c.title,''),q.rowid FROM voice_queue q LEFT JOIN chats c ON c.id=q.chat_id WHERE 1=1`)
	var args []any
	if q.State != "" {
		query.WriteString(` AND q.state=?`)
		args = append(args, q.State)
	}
	if q.Kind != "" {
		query.WriteString(` AND q.kind=?`)
		args = append(args, q.Kind)
	}
	if q.ChatID != nil {
		query.WriteString(` AND q.chat_id=?`)
		args = append(args, *q.ChatID)
	}
	condition, filterArgs := q.ChatFilter.condition("q.chat_id", false)
	query.WriteString(condition)
	args = append(args, filterArgs...)
	if q.After != nil {
		query.WriteString(` AND (q.enqueued_at<? OR (q.enqueued_at=? AND q.rowid<?))`)
		args = append(args, q.After.EnqueuedAt, q.After.EnqueuedAt, q.After.RowID)
	}
	query.WriteString(` ORDER BY q.enqueued_at DESC,q.rowid DESC LIMIT ?`)
	args = append(args, q.Limit+1)
	rows, err := db.read.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, false, fmt.Errorf("list voice jobs: %w", err)
	}
	defer rows.Close()
	var entries []VoiceListEntry
	for rows.Next() {
		var e VoiceListEntry
		var started, finished *int64
		if err := rows.Scan(&e.ChatID, &e.MessageID, &e.Kind, &e.Source, &e.Priority, &e.State, &e.Reason, &e.Attempts, &e.Bytes, &e.EnqueuedAt, &started, &finished, &e.ChatTitle, &e.Position.RowID); err != nil {
			return nil, false, fmt.Errorf("list voice jobs: %w", err)
		}
		if started != nil {
			e.StartedAt = *started
		}
		if finished != nil {
			e.FinishedAt = *finished
		}
		e.Position.EnqueuedAt = e.EnqueuedAt
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list voice jobs: %w", err)
	}
	more := len(entries) > q.Limit
	if more {
		entries = entries[:q.Limit]
	}
	return entries, more, nil
}
