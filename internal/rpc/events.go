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
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	ChatID *int64          `json:"chat_id,omitempty"`
	Time   string          `json:"time"`
	Data   json.RawMessage `json:"data,omitempty" untrusted:"true"`
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
}

// ReplayPlan contains durable history and a cursor for that history. Filter
// applies to subsequent live events. The cursor must name the
// highest durable event in Events, or the requested cursor when Events is empty.
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
	head, _, _ := strings.Cut(cursor, ".")
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil || id < 0 {
		return 0, errors.New("invalid event cursor")
	}
	return id, nil
}
