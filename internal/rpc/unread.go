package rpc

import "encoding/json"

const (
	MethodChatUnread    = "chat.unread"
	MethodMessageUnread = "message.unread"
	MethodChatChanges   = "chat.changes"
)

type ChatUnreadParams struct {
	Limit    int  `json:"limit,omitempty"`
	Archived bool `json:"archived,omitempty"`
}
type UnreadChat struct {
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw             json.RawMessage `json:"-"`
	ID              int64           `json:"id"`
	Type            string          `json:"type"`
	Title           string          `json:"title" untrusted:"true"`
	Username        string          `json:"username,omitempty" untrusted:"true"`
	Forum           bool            `json:"forum"`
	Unread          int             `json:"unread"`
	UnreadMentions  int             `json:"unread_mentions"`
	UnreadReactions int             `json:"unread_reactions"`
	UnreadMark      bool            `json:"unread_mark"`
	Muted           bool            `json:"muted"`
	Archived        bool            `json:"archived"`
	ReadInboxMaxID  int             `json:"read_inbox_max_id"`
	TopMessage      int             `json:"top_message"`
}
type UnreadChatsResult struct {
	Chats    []UnreadChat `json:"chats"`
	Scanned  int          `json:"scanned"`
	Complete bool         `json:"complete"`
}
type MessageUnreadParams struct {
	Chat   string `json:"chat"`
	Topic  int    `json:"topic,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type UnreadMessagesResult struct {
	ChatID         int64     `json:"chat_id"`
	Topic          int       `json:"topic,omitempty"`
	ReadInboxMaxID int       `json:"read_inbox_max_id"`
	Unread         int       `json:"unread"`
	Messages       []Message `json:"messages"`
	NextCursor     string    `json:"next_cursor,omitempty"`
}
type ChatChangesParams struct {
	Chat  string `json:"chat"`
	Limit int    `json:"limit,omitempty"`
}
type ChatSnapshot struct {
	Type     string          `json:"type"`
	Title    string          `json:"title" untrusted:"true"`
	Username *string         `json:"username" untrusted:"true"`
	About    *string         `json:"about" untrusted:"true"`
	Settings json.RawMessage `json:"settings"`
}
type ChatVersion struct {
	ChatSnapshot
	Version    int   `json:"version"`
	ObservedAt int64 `json:"observed_at"`
}
type ChatChangesResult struct {
	ChatID   int64         `json:"chat_id"`
	Current  ChatSnapshot  `json:"current"`
	Versions []ChatVersion `json:"versions"`
}
