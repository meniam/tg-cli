package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// VoiceChatRule is what the database knows about one chat for the voice
// sources: whether it is a personal chat and whether the owner marked it. A chat
// the database does not know is neither.
type VoiceChatRule struct {
	// Private is true for a chat of type "user" (not a bot, group or channel).
	Private bool
	// Marked is true when voice_chats holds the chat.
	Marked bool
	// Video is the video flag of the mark; it is false for an unmarked chat.
	Video bool
}

// VoiceChatRule reads the rule data of a chat. It does not check the access
// lists: the caller does.
func (db *DB) VoiceChatRule(ctx context.Context, chatID int64) (VoiceChatRule, error) {
	var chatType sql.NullString
	var video sql.NullBool
	err := db.read.QueryRowContext(ctx, `SELECT
		(SELECT type FROM chats WHERE id=?1),
		(SELECT video FROM voice_chats WHERE chat_id=?1)`, chatID).Scan(&chatType, &video)
	if err != nil {
		return VoiceChatRule{}, fmt.Errorf("read voice chat rule: %w", err)
	}
	return VoiceChatRule{Private: chatType.String == "user", Marked: video.Valid, Video: video.Bool}, nil
}

// VoiceCandidate is a saved message that could be queued for transcription.
type VoiceCandidate struct {
	ChatID    int64
	MessageID int
	// Kind is the media type of the message: voice, video_note or video.
	Kind string
}

// VoiceCandidateQuery selects saved messages that have no finished
// transcription and no row in the voice queue. Messages that are deleted or
// sent by the owner are never returned.
type VoiceCandidateQuery struct {
	// Filter hides the chats that access.json closes.
	Filter ChatFilter
	// ChatID limits the query to one chat; zero means every chat the other
	// conditions allow.
	ChatID int64
	// Private limits the query to personal chats (type "user").
	Private bool
	// Kinds are the media types to return; at least one is required.
	Kinds []string
	// After is the last candidate of the previous page; nil starts from the
	// beginning. Candidates come ordered by chat id, then message id.
	After *VoiceCandidate
	// Limit is the page size, 1 to MaxExportPageSize.
	Limit int
}

// VoiceCandidates returns one page of the candidates of q. A page shorter than
// the limit is the last one. A transcription that Telegram has not finished yet
// (pending) does not count as a text.
func (db *DB) VoiceCandidates(ctx context.Context, q VoiceCandidateQuery) ([]VoiceCandidate, error) {
	if q.Limit < 1 || q.Limit > MaxExportPageSize {
		return nil, fmt.Errorf("voice candidate limit must be between 1 and %d", MaxExportPageSize)
	}
	if len(q.Kinds) == 0 {
		return nil, errors.New("voice candidates need at least one kind")
	}
	var query strings.Builder
	var args []any
	query.WriteString(`SELECT m.chat_id,m.id,m.media_type FROM messages m`)
	if q.Private {
		query.WriteString(` JOIN chats c ON c.id=m.chat_id AND c.type='user'`)
	}
	query.WriteString(` LEFT JOIN transcriptions t ON t.chat_id=m.chat_id AND t.message_id=m.id`)
	query.WriteString(` LEFT JOIN voice_queue v ON v.chat_id=m.chat_id AND v.message_id=m.id`)
	query.WriteString(` WHERE m.media_type IN (` + placeholders(len(q.Kinds)) + `)`)
	for _, kind := range q.Kinds {
		switch kind {
		case VoiceKindVoice, VoiceKindVideoNote, VoiceKindVideo:
		default:
			return nil, fmt.Errorf("invalid voice candidate kind %q", kind)
		}
		args = append(args, kind)
	}
	query.WriteString(` AND m.deleted_at IS NULL AND m.out=0 AND (t.chat_id IS NULL OR t.pending=1) AND v.chat_id IS NULL`)
	if q.ChatID != 0 {
		query.WriteString(` AND m.chat_id=?`)
		args = append(args, q.ChatID)
	}
	cond, condArgs := q.Filter.condition("m.chat_id", false)
	query.WriteString(cond)
	args = append(args, condArgs...)
	if q.After != nil {
		query.WriteString(` AND (m.chat_id>? OR (m.chat_id=? AND m.id>?))`)
		args = append(args, q.After.ChatID, q.After.ChatID, q.After.MessageID)
	}
	query.WriteString(` ORDER BY m.chat_id,m.id LIMIT ?`)
	args = append(args, q.Limit)
	rows, err := db.read.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("read voice candidates: %w", err)
	}
	defer rows.Close()
	var out []VoiceCandidate
	for rows.Next() {
		var c VoiceCandidate
		if err := rows.Scan(&c.ChatID, &c.MessageID, &c.Kind); err != nil {
			return nil, fmt.Errorf("scan voice candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// VoicePrivateChats counts the personal chats that have saved messages and that
// the filter leaves open: the chats a personal backfill looks at.
func (db *DB) VoicePrivateChats(ctx context.Context, filter ChatFilter) (int, error) {
	cond, args := filter.condition("c.id", false)
	var n int
	err := db.read.QueryRowContext(ctx, `SELECT count(*) FROM chats c WHERE c.type='user' AND EXISTS (SELECT 1 FROM messages m WHERE m.chat_id=c.id)`+cond, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count personal chats: %w", err)
	}
	return n, nil
}
