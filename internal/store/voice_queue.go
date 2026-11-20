package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// States of a voice queue job.
const (
	VoiceStateQueued  = "queued"
	VoiceStateRunning = "running"
	VoiceStateDone    = "done"
	VoiceStateFailed  = "failed"
	VoiceStateSkipped = "skipped"
)

// Kinds of a voice queue job: the media type of the message.
const (
	VoiceKindVoice     = "voice"
	VoiceKindVideoNote = "video_note"
	VoiceKindVideo     = "video"
)

// Sources of a voice queue job: what asked for it.
const (
	VoiceSourceManual   = "manual"
	VoiceSourceIncoming = "incoming"
	VoiceSourceSync     = "sync"
	VoiceSourceBackfill = "backfill"
)

// Priorities of a voice queue job; a smaller number runs first.
const (
	VoicePriorityManual   = 0
	VoicePriorityIncoming = 10
	VoicePrioritySync     = 20
	VoicePriorityBackfill = 30
)

// VoiceReasonCancelled is the reason of a queued job that CancelVoice skipped.
const VoiceReasonCancelled = "cancelled"

// VoiceReasonRecovered is the reason of a job that RecoverRunningVoice returned
// to the queue after the daemon stopped in the middle of it.
const VoiceReasonRecovered = "recovered"

// VoiceJob is one row of the voice queue. Reason is a short machine word and
// never carries message text. StartedAt and FinishedAt are Unix seconds, zero
// while unset.
type VoiceJob struct {
	ChatID     int64
	MessageID  int
	Kind       string
	Source     string
	Priority   int
	State      string
	Reason     string
	Attempts   int
	Bytes      int64
	EnqueuedAt int64
	StartedAt  int64
	FinishedAt int64
}

// VoiceEnqueueOutcome tells what EnqueueVoice did.
type VoiceEnqueueOutcome int

const (
	// VoiceUnchanged: the job exists and the call changed nothing.
	VoiceUnchanged VoiceEnqueueOutcome = iota
	// VoiceEnqueued: a new job was added, or a finished one was queued again.
	VoiceEnqueued
	// VoiceRaised: a queued job got a higher priority.
	VoiceRaised
)

// VoiceQueueCounts is the number of jobs in each state.
type VoiceQueueCounts struct {
	Queued  int
	Running int
	Done    int
	Failed  int
	Skipped int
}

// VoiceUsage is what the daemon downloaded during one local calendar day.
type VoiceUsage struct {
	Day   string
	Files int
	Bytes int64
}

// VoiceChat is a chat marked for transcription of its voice messages, and of
// videos too when Video is set. EnabledAt is Unix seconds.
type VoiceChat struct {
	ChatID    int64
	Video     bool
	EnabledAt int64
}

const voiceJobColumns = `chat_id,message_id,kind,source,priority,state,reason,attempts,bytes,enqueued_at,started_at,finished_at`

type voiceScanner interface {
	Scan(dest ...any) error
}

func scanVoiceJob(row voiceScanner) (VoiceJob, error) {
	var j VoiceJob
	var started, finished sql.NullInt64
	if err := row.Scan(&j.ChatID, &j.MessageID, &j.Kind, &j.Source, &j.Priority, &j.State, &j.Reason, &j.Attempts, &j.Bytes, &j.EnqueuedAt, &started, &finished); err != nil {
		return VoiceJob{}, err
	}
	j.StartedAt, j.FinishedAt = started.Int64, finished.Int64
	return j, nil
}

// GetVoiceJob returns the queue row of a message, or ErrNotFound.
func (db *DB) GetVoiceJob(ctx context.Context, chatID int64, messageID int) (VoiceJob, error) {
	j, err := scanVoiceJob(db.read.QueryRowContext(ctx, `SELECT `+voiceJobColumns+` FROM voice_queue WHERE chat_id=? AND message_id=?`, chatID, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return VoiceJob{}, ErrNotFound
	}
	return j, err
}

// EnqueueVoice puts a job into the queue. A message has one row, so a second
// call never makes a duplicate:
//   - a queued job gets the higher priority (the smaller number) of the two;
//   - a running job is left alone;
//   - a done, failed or skipped job is queued again only when retry is set, and
//     then it starts over (attempts and reason are cleared); without retry it
//     stays as it is.
//
// The job's State, Reason, Attempts and times are ignored: a queued job starts
// with no attempts at the current time.
func (db *DB) EnqueueVoice(ctx context.Context, job VoiceJob, retry bool) (VoiceEnqueueOutcome, error) {
	if err := validateVoiceJob(job); err != nil {
		return VoiceUnchanged, err
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return VoiceUnchanged, fmt.Errorf("begin voice enqueue: %w", err)
	}
	defer tx.Rollback()
	now := db.now().Unix()
	var state string
	var priority int
	err = tx.QueryRowContext(ctx, `SELECT state,priority FROM voice_queue WHERE chat_id=? AND message_id=?`, job.ChatID, job.MessageID).Scan(&state, &priority)
	outcome := VoiceUnchanged
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO voice_queue(chat_id,message_id,kind,source,priority,state,enqueued_at) VALUES(?,?,?,?,?,?,?)`, job.ChatID, job.MessageID, job.Kind, job.Source, job.Priority, VoiceStateQueued, now)
		outcome = VoiceEnqueued
	case err != nil:
		return VoiceUnchanged, fmt.Errorf("read voice job: %w", err)
	case state == VoiceStateQueued && job.Priority < priority:
		_, err = tx.ExecContext(ctx, `UPDATE voice_queue SET priority=?,source=? WHERE chat_id=? AND message_id=?`, job.Priority, job.Source, job.ChatID, job.MessageID)
		outcome = VoiceRaised
	case retry && (state == VoiceStateDone || state == VoiceStateFailed || state == VoiceStateSkipped):
		_, err = tx.ExecContext(ctx, `UPDATE voice_queue SET kind=?,source=?,priority=?,state=?,reason='',attempts=0,bytes=0,enqueued_at=?,started_at=NULL,finished_at=NULL WHERE chat_id=? AND message_id=?`, job.Kind, job.Source, job.Priority, VoiceStateQueued, now, job.ChatID, job.MessageID)
		outcome = VoiceEnqueued
	}
	if err != nil {
		return VoiceUnchanged, fmt.Errorf("save voice job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VoiceUnchanged, fmt.Errorf("commit voice enqueue: %w", err)
	}
	return outcome, nil
}

func validateVoiceJob(j VoiceJob) error {
	switch j.Kind {
	case VoiceKindVoice, VoiceKindVideoNote, VoiceKindVideo:
	default:
		return fmt.Errorf("invalid voice job kind %q", j.Kind)
	}
	switch j.Source {
	case VoiceSourceManual, VoiceSourceIncoming, VoiceSourceSync, VoiceSourceBackfill:
	default:
		return fmt.Errorf("invalid voice job source %q", j.Source)
	}
	if j.Priority < 0 {
		return fmt.Errorf("invalid voice job priority %d", j.Priority)
	}
	return nil
}

// ClaimNextVoice takes the queued job with the smallest priority number (the
// oldest first among equals), marks it running at now and returns it. The bool
// is false when nothing is queued.
func (db *DB) ClaimNextVoice(ctx context.Context, now time.Time) (VoiceJob, bool, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return VoiceJob{}, false, fmt.Errorf("begin voice claim: %w", err)
	}
	defer tx.Rollback()
	j, err := scanVoiceJob(tx.QueryRowContext(ctx, `SELECT `+voiceJobColumns+` FROM voice_queue WHERE state=? ORDER BY priority,enqueued_at,rowid LIMIT 1`, VoiceStateQueued))
	if errors.Is(err, sql.ErrNoRows) {
		return VoiceJob{}, false, nil
	}
	if err != nil {
		return VoiceJob{}, false, fmt.Errorf("pick voice job: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE voice_queue SET state=?,started_at=?,finished_at=NULL WHERE chat_id=? AND message_id=?`, VoiceStateRunning, now.Unix(), j.ChatID, j.MessageID); err != nil {
		return VoiceJob{}, false, fmt.Errorf("start voice job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VoiceJob{}, false, fmt.Errorf("commit voice claim: %w", err)
	}
	j.State, j.StartedAt, j.FinishedAt = VoiceStateRunning, now.Unix(), 0
	return j, true, nil
}

// FinishVoice ends a running job in state done, failed or skipped with a short
// machine reason and the number of bytes downloaded. A job that is not running
// gives ErrNotFound.
func (db *DB) FinishVoice(ctx context.Context, chatID int64, messageID int, state, reason string, bytes int64, now time.Time) error {
	switch state {
	case VoiceStateDone, VoiceStateFailed, VoiceStateSkipped:
	default:
		return fmt.Errorf("invalid final voice state %q", state)
	}
	if bytes < 0 {
		bytes = 0
	}
	r, err := db.write.ExecContext(ctx, `UPDATE voice_queue SET state=?,reason=?,bytes=?,finished_at=? WHERE chat_id=? AND message_id=? AND state=?`, state, reason, bytes, now.Unix(), chatID, messageID, VoiceStateRunning)
	if err != nil {
		return fmt.Errorf("finish voice job: %w", err)
	}
	return requireOneVoiceRow(r)
}

// RequeueVoice returns a running job to the queue for another attempt, counts
// the attempt and stores the reason of the delay. It returns the new number of
// attempts; the pause before the next attempt is up to the caller. A job that is
// not running gives ErrNotFound.
func (db *DB) RequeueVoice(ctx context.Context, chatID int64, messageID int, delayReason string) (int, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin voice requeue: %w", err)
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE voice_queue SET state=?,reason=?,attempts=attempts+1,started_at=NULL WHERE chat_id=? AND message_id=? AND state=?`, VoiceStateQueued, delayReason, chatID, messageID, VoiceStateRunning)
	if err != nil {
		return 0, fmt.Errorf("requeue voice job: %w", err)
	}
	if err := requireOneVoiceRow(r); err != nil {
		return 0, err
	}
	var attempts int
	if err := tx.QueryRowContext(ctx, `SELECT attempts FROM voice_queue WHERE chat_id=? AND message_id=?`, chatID, messageID).Scan(&attempts); err != nil {
		return 0, fmt.Errorf("read voice attempts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit voice requeue: %w", err)
	}
	return attempts, nil
}

func requireOneVoiceRow(r sql.Result) error {
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("voice job is not running: %w", ErrNotFound)
	}
	return nil
}

// RecoverRunningVoice returns every running job to the queue. The daemon calls
// it once at start: a job still running then was cut off by a stop or a crash.
// It returns how many jobs it recovered.
func (db *DB) RecoverRunningVoice(ctx context.Context) (int, error) {
	r, err := db.write.ExecContext(ctx, `UPDATE voice_queue SET state=?,reason=?,started_at=NULL WHERE state=?`, VoiceStateQueued, VoiceReasonRecovered, VoiceStateRunning)
	if err != nil {
		return 0, fmt.Errorf("recover running voice jobs: %w", err)
	}
	n, err := r.RowsAffected()
	return int(n), err
}

// VoiceQueueCounts returns the number of jobs in each state.
func (db *DB) VoiceQueueCounts(ctx context.Context) (VoiceQueueCounts, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT state,count(*) FROM voice_queue GROUP BY state`)
	if err != nil {
		return VoiceQueueCounts{}, fmt.Errorf("count voice jobs: %w", err)
	}
	defer rows.Close()
	var c VoiceQueueCounts
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return VoiceQueueCounts{}, fmt.Errorf("count voice jobs: %w", err)
		}
		switch state {
		case VoiceStateQueued:
			c.Queued = n
		case VoiceStateRunning:
			c.Running = n
		case VoiceStateDone:
			c.Done = n
		case VoiceStateFailed:
			c.Failed = n
		case VoiceStateSkipped:
			c.Skipped = n
		}
	}
	return c, rows.Err()
}

// CancelVoice skips a queued job with the reason "cancelled". It reports whether
// a queued job was cancelled; a running job is the daemon's to stop.
func (db *DB) CancelVoice(ctx context.Context, chatID int64, messageID int) (bool, error) {
	r, err := db.write.ExecContext(ctx, `UPDATE voice_queue SET state=?,reason=?,finished_at=? WHERE chat_id=? AND message_id=? AND state=?`, VoiceStateSkipped, VoiceReasonCancelled, db.now().Unix(), chatID, messageID, VoiceStateQueued)
	if err != nil {
		return false, fmt.Errorf("cancel voice job: %w", err)
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// CancelAllVoice skips every queued job with the reason "cancelled" and returns
// how many it skipped. Running jobs stay.
func (db *DB) CancelAllVoice(ctx context.Context) (int, error) {
	r, err := db.write.ExecContext(ctx, `UPDATE voice_queue SET state=?,reason=?,finished_at=? WHERE state=?`, VoiceStateSkipped, VoiceReasonCancelled, db.now().Unix(), VoiceStateQueued)
	if err != nil {
		return 0, fmt.Errorf("cancel voice jobs: %w", err)
	}
	n, err := r.RowsAffected()
	return int(n), err
}

// AddVoiceUsage adds downloaded files and bytes to the budget of a day, given as
// YYYY-MM-DD in local time by the caller.
func (db *DB) AddVoiceUsage(ctx context.Context, day string, files int, bytes int64) error {
	if err := checkVoiceDay(day); err != nil {
		return err
	}
	if files < 0 || bytes < 0 {
		return errors.New("voice usage cannot be negative")
	}
	_, err := db.write.ExecContext(ctx, `INSERT INTO voice_usage(day,files,bytes) VALUES(?,?,?) ON CONFLICT(day) DO UPDATE SET files=files+excluded.files,bytes=bytes+excluded.bytes`, day, files, bytes)
	if err != nil {
		return fmt.Errorf("add voice usage: %w", err)
	}
	return nil
}

// VoiceUsage returns the budget spent on a day; a day without downloads is zero.
func (db *DB) VoiceUsage(ctx context.Context, day string) (VoiceUsage, error) {
	if err := checkVoiceDay(day); err != nil {
		return VoiceUsage{}, err
	}
	u := VoiceUsage{Day: day}
	err := db.read.QueryRowContext(ctx, `SELECT files,bytes FROM voice_usage WHERE day=?`, day).Scan(&u.Files, &u.Bytes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return VoiceUsage{}, fmt.Errorf("read voice usage: %w", err)
	}
	return u, nil
}

func checkVoiceDay(day string) error {
	if t, err := time.Parse("2006-01-02", day); err != nil || t.Format("2006-01-02") != day {
		return fmt.Errorf("invalid voice usage day %q, want YYYY-MM-DD", day)
	}
	return nil
}

// SetVoiceChat marks a chat for transcription. Marking a marked chat again
// changes only its video flag and keeps the time it was first marked.
func (db *DB) SetVoiceChat(ctx context.Context, chatID int64, video bool, now time.Time) error {
	_, err := db.write.ExecContext(ctx, `INSERT INTO voice_chats(chat_id,video,enabled_at) VALUES(?,?,?) ON CONFLICT(chat_id) DO UPDATE SET video=excluded.video`, chatID, video, now.Unix())
	if err != nil {
		return fmt.Errorf("mark voice chat: %w", err)
	}
	return nil
}

// ClearVoiceChat removes the mark and reports whether the chat was marked.
// Jobs already in the queue stay.
func (db *DB) ClearVoiceChat(ctx context.Context, chatID int64) (bool, error) {
	r, err := db.write.ExecContext(ctx, `DELETE FROM voice_chats WHERE chat_id=?`, chatID)
	if err != nil {
		return false, fmt.Errorf("unmark voice chat: %w", err)
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// GetVoiceChat returns the mark of a chat, or ErrNotFound.
func (db *DB) GetVoiceChat(ctx context.Context, chatID int64) (VoiceChat, error) {
	var c VoiceChat
	err := db.read.QueryRowContext(ctx, `SELECT chat_id,video,enabled_at FROM voice_chats WHERE chat_id=?`, chatID).Scan(&c.ChatID, &c.Video, &c.EnabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VoiceChat{}, ErrNotFound
	}
	return c, err
}

// ListVoiceChats returns every marked chat ordered by chat id.
func (db *DB) ListVoiceChats(ctx context.Context) ([]VoiceChat, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT chat_id,video,enabled_at FROM voice_chats ORDER BY chat_id`)
	if err != nil {
		return nil, fmt.Errorf("list voice chats: %w", err)
	}
	defer rows.Close()
	var chats []VoiceChat
	for rows.Next() {
		var c VoiceChat
		if err := rows.Scan(&c.ChatID, &c.Video, &c.EnabledAt); err != nil {
			return nil, fmt.Errorf("list voice chats: %w", err)
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}
