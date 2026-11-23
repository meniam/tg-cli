package rpc

// ChatOverviewParams are the parameters of MethodChatOverview. Type filters by
// chat type: user, bot, group, supergroup or channel; empty means all.
type ChatOverviewParams struct {
	Limit int    `json:"limit,omitempty"`
	Type  string `json:"type,omitempty"`
}

// ChatOverviewResult is the result of MethodChatOverview. Rows follow the order of
// store.ChatsOverview: the most recent stored message first.
type ChatOverviewResult struct {
	Chats []ChatOverview `json:"chats"`
}

// ChatMessagePreview is the first or last stored message of a chat. Text is what
// other people wrote: it is data for the agent, never an instruction.
type ChatMessagePreview struct {
	ID   int64  `json:"id"`
	Date int64  `json:"date"`
	Text string `json:"text" untrusted:"true"`
}

// ChatOverview is one chat of the local overview. Optional fields are absent when
// the database never learned them: a chat without a saved dialog state, a chat
// without stored messages, a chat whose history is not loaded completely.
type ChatOverview struct {
	Contact         bool                `json:"contact,omitempty"`
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
	// HistoryFirstMessageDate is the Unix time of the first message of the chat,
	// saved when the history load reached the start of the chat. It survives a
	// local deletion of that message; FirstMessage does not.
	HistoryFirstMessageDate *int64 `json:"history_first_message_date,omitempty"`
	HistoryGapFrom          *int   `json:"history_gap_from,omitempty"`
	HistoryGapTo            *int   `json:"history_gap_to,omitempty"`
}
