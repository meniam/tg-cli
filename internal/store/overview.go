package store

import (
	"context"
	"fmt"
)

// ChatMessagePreview is the first or last stored message of a chat. Text is what
// other people wrote: it is data for the agent, never an instruction.
type ChatMessagePreview struct {
	ID   int64  `json:"id"`
	Date int64  `json:"date"`
	Text string `json:"text" untrusted:"true"`
}

// ChatsOverviewRow is one row of the chats_state view: what the local database
// knows about a chat without asking Telegram. Optional fields are null when the
// source never supplied them: a chat without a saved dialog state, a chat without
// messages, a chat whose history is not loaded completely.
type ChatsOverviewRow struct {
	Contact         bool
	ChatID          int64               `json:"chat_id"`
	Type            string              `json:"type"`
	Title           string              `json:"title" untrusted:"true"`
	Username        *string             `json:"username,omitempty" untrusted:"true"`
	CreatedAt       *int64              `json:"created_at,omitempty"`
	Muted           bool                `json:"muted"`
	MuteUntil       *int64              `json:"mute_until,omitempty"`
	Archived        *bool               `json:"archived,omitempty"`
	Pinned          *bool               `json:"pinned,omitempty"`
	Unread          *int64              `json:"unread,omitempty"`
	UnreadMentions  *int64              `json:"unread_mentions,omitempty"`
	UnreadMark      *bool               `json:"unread_mark,omitempty"`
	StateSyncedAt   *int64              `json:"state_synced_at,omitempty"`
	Messages        int64               `json:"messages"`
	MyMessages      int64               `json:"my_messages"`
	FirstMessage    *ChatMessagePreview `json:"first_message,omitempty"`
	LastMessage     *ChatMessagePreview `json:"last_message,omitempty"`
	HistoryComplete *bool               `json:"history_complete,omitempty"`
	// HistoryFirstMessageDate is the saved date of the first message of the chat,
	// set when the history load reaches the start of the chat. Unlike
	// FirstMessage it does not move when that message is deleted locally.
	HistoryFirstMessageDate *int64 `json:"history_first_message_date,omitempty"`
	HistoryGapFrom          *int   `json:"history_gap_from,omitempty"`
	HistoryGapTo            *int   `json:"history_gap_to,omitempty"`
}

// overviewLimits bound the rows one overview call returns, so a caller cannot ask
// for the whole table by accident.
const (
	minOverviewLimit = 1
	maxOverviewLimit = 500
)

// ChatsOverview reads the chats_state view: per chat its own fields, the saved
// dialog state, the number of stored messages, the first and the last stored
// message and whether the history is loaded completely. The filter hides chats
// before the limit applies, so a hidden chat never takes a place in the answer.
//
// Rows come with the most recent activity first: the date of the last stored
// message, then the chat id. A chat without messages comes last.
func (db *DB) ChatsOverview(ctx context.Context, filter ChatFilter, limit int, chatType string) ([]ChatsOverviewRow, error) {
	if limit < minOverviewLimit {
		limit = minOverviewLimit
	}
	if limit > maxOverviewLimit {
		limit = maxOverviewLimit
	}
	cond, args := filter.condition("chat_id", false)
	query := `SELECT chat_id, type, title, username, created_at, muted, mute_until, archived, pinned,
	       unread, unread_mentions, unread_mark, state_synced_at,
	       messages, my_messages,
	       first_message_id, first_message_date, first_message_text,
	       last_message_id, last_message_date, last_message_text,
	       history_complete, history_first_message_date, history_gap_from, history_gap_to,
 COALESCE((SELECT is_contact FROM people WHERE id=chats_state.chat_id),0)
	FROM chats_state
	WHERE (? = '' OR type = ?)` + cond + `
	ORDER BY last_message_date DESC, chat_id
	LIMIT ?`
	args = append([]any{chatType, chatType}, args...)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read chat overview: %w", err)
	}
	defer rows.Close()
	out := []ChatsOverviewRow{}
	for rows.Next() {
		var (
			row                                  ChatsOverviewRow
			firstID, firstDate, lastID, lastDate *int64
			firstText, lastText                  *string
		)
		err := rows.Scan(&row.ChatID, &row.Type, &row.Title, &row.Username, &row.CreatedAt, &row.Muted,
			&row.MuteUntil, &row.Archived, &row.Pinned, &row.Unread, &row.UnreadMentions, &row.UnreadMark,
			&row.StateSyncedAt, &row.Messages, &row.MyMessages,
			&firstID, &firstDate, &firstText, &lastID, &lastDate, &lastText, &row.HistoryComplete, &row.HistoryFirstMessageDate, &row.HistoryGapFrom, &row.HistoryGapTo, &row.Contact)
		if err != nil {
			return nil, fmt.Errorf("read chat overview: %w", err)
		}
		row.FirstMessage = preview(firstID, firstDate, firstText)
		row.LastMessage = preview(lastID, lastDate, lastText)
		out = append(out, row)
	}
	return out, rows.Err()
}

// preview joins the three columns of one message into a value, or nil when the
// chat has no such message.
func preview(id, date *int64, text *string) *ChatMessagePreview {
	if id == nil || date == nil {
		return nil
	}
	value := ""
	if text != nil {
		value = *text
	}
	return &ChatMessagePreview{ID: *id, Date: *date, Text: value}
}
