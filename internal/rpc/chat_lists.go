package rpc

import "encoding/json"

// MembersParams selects a page of chat participants.
type MembersParams struct {
	Chat   string `json:"chat"`
	Search string `json:"search,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Filter string `json:"filter,omitempty"`
}

type Member struct {
	ID       int64  `json:"id"`
	Name     string `json:"name" untrusted:"true"`
	Username string `json:"username,omitempty" untrusted:"true"`
	IsBot    bool   `json:"is_bot"`
	Role     string `json:"role"`
	Joined   string `json:"joined,omitempty"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}

type MembersResult struct {
	Members    []Member `json:"members"`
	NextOffset int      `json:"next_offset,omitempty"`
}

type TopicsParams struct {
	Chat   string `json:"chat"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type Topic struct {
	ID         int    `json:"id"`
	Title      string `json:"title" untrusted:"true"`
	Closed     bool   `json:"closed"`
	Pinned     bool   `json:"pinned"`
	Hidden     bool   `json:"hidden"`
	Unread     int    `json:"unread"`
	TopMessage int    `json:"top_message"`
	Created    string `json:"created"`
}

type TopicsResult struct {
	Topics     []Topic `json:"topics"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type CommonChatsParams struct {
	User   string `json:"user"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type CommonChat struct {
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw      json.RawMessage `json:"-"`
	ID       int64           `json:"id"`
	Type     string          `json:"type"`
	Title    string          `json:"title" untrusted:"true"`
	Username string          `json:"username,omitempty" untrusted:"true"`
}

type CommonChatsResult struct {
	Chats      []CommonChat `json:"chats"`
	NextCursor string       `json:"next_cursor,omitempty"`
}
