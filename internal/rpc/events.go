package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

const (
	EventMessageNew    = "message.new"
	EventMessageEdit   = "message.edit"
	EventMessageDelete = "message.delete"
	EventReaction      = "reaction"
	EventRead          = "read"
	EventTyping        = "typing"
	EventStatus        = "status"
	EventGap           = "gap"
	EventLagged        = "lagged"
)

// Event is a journal or live event. ID is a string cursor. Durable cursors
// start with the database event ID; live-only events may reuse the last cursor.
type Event struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	ChatID *int64          `json:"chat_id,omitempty"`
	Time   string          `json:"time"`
	Data   json.RawMessage `json:"data,omitempty" untrusted:"true"`
	// DedupKey is used by the Daemon's durable event store and never sent to clients.
	DedupKey string `json:"-"`
}

// Message event data is the existing Message type.
type MessageDeleteData struct {
	ChatID *int64 `json:"chat_id,omitempty"`
	IDs    []int  `json:"ids"`
}

type ReactionCount struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
}

type ReactionData struct {
	ChatID    int64           `json:"chat_id"`
	MessageID int             `json:"message_id"`
	Reactions []ReactionCount `json:"reactions"`
}

type ReadData struct {
	ChatID int64 `json:"chat_id"`
	MaxID  int   `json:"max_id"`
	Outbox bool  `json:"outbox"`
}

type TypingData struct {
	ChatID int64  `json:"chat_id"`
	UserID int64  `json:"user_id"`
	Action string `json:"action"`
}

type StatusData struct {
	UserID int64 `json:"user_id"`
	Online bool  `json:"online"`
}

type GapData struct {
	ChatID *int64 `json:"chat_id,omitempty"`
	Reason string `json:"reason"`
}

// SubscribeParams selects chats, topic, types and the last seen event cursor.
type SubscribeParams struct {
	Chats []string `json:"chats,omitempty"`
	Topic int      `json:"topic,omitempty"`
	Types []string `json:"types,omitempty"`
	After string   `json:"after,omitempty"`
}

type SubscribeResult struct {
	SubscriptionID string `json:"subscription_id"`
	Cursor         string `json:"cursor"`
}

type UnsubscribeParams struct {
	SubscriptionID string `json:"subscription_id"`
}

type UnsubscribeResult struct {
	Unsubscribed bool `json:"unsubscribed"`
}

// EventFilter constrains live events after replay. Empty lists accept all.
type EventFilter struct {
	Types   []string
	ChatIDs []int64
	Topic   int
}

// ReplayPlan contains durable history and the journal high-water cursor.
// Filter applies to subsequent live events.
type ReplayPlan struct {
	Events []Event
	Cursor string
	Filter EventFilter
	resume string
}

// ReplayFunc runs after live registration. Buffered events with IDs already
// included in the returned history are skipped by the subscription sender.
type ReplayFunc func(context.Context, SubscribeParams) (ReplayPlan, error)

// CursorID reads the leading durable ID of <id>.<pts>.<qts>.<date>. The state
// components are opaque here and will be filled by the updates integration.
func CursorID(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 4 {
		return 0, errors.New("invalid event cursor")
	}
	var id int64
	for i, part := range parts {
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil || value < 0 {
			return 0, errors.New("invalid event cursor")
		}
		if i == 0 {
			id = value
		}
	}
	return id, nil
}
