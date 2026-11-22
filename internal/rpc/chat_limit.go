package rpc

// The chat message limit caps how many messages the local database keeps for one
// chat. The methods only touch the local database; none of them calls Telegram.
const (
	// MethodChatLimitSet stores the limit of a chat.
	MethodChatLimitSet = "chat.limit.set"
	// MethodChatLimitOff removes the limit of a chat.
	MethodChatLimitOff = "chat.limit.off"
	// MethodChatLimitList lists the chats that have a limit.
	MethodChatLimitList = "chat.limit.list"
)

// ChatLimitSetParams names the chat and its limit. Confirm must be true when the
// chat already holds more messages than the limit and the cleanup would delete
// saved messages at once.
type ChatLimitSetParams struct {
	Chat     string `json:"chat"`
	Messages int    `json:"messages"`
	Confirm  bool   `json:"confirm,omitempty"`
}

// ChatLimitSetResult reports the limit. Saved is how many message rows the chat
// holds now and Keep is how many a cleanup leaves. WillDelete is how many
// messages the next cleanup removes: zero while Saved does not exceed Messages.
type ChatLimitSetResult struct {
	ChatID     int64 `json:"chat_id"`
	Messages   int   `json:"messages"`
	Keep       int   `json:"keep"`
	Saved      int   `json:"saved"`
	WillDelete int   `json:"will_delete"`
}

// ChatLimitOffParams names the chat whose limit is removed.
type ChatLimitOffParams struct {
	Chat string `json:"chat"`
}

// ChatLimitOffResult says whether the chat had a limit. Deleted messages do not
// come back.
type ChatLimitOffResult struct {
	ChatID  int64 `json:"chat_id"`
	Removed bool  `json:"removed"`
}

// ChatLimitListParams carries no field.
type ChatLimitListParams struct{}

// ChatLimitEntry is one chat with a limit. Title and Username are names from
// Telegram (untrusted data); they are empty when the database does not know the
// chat.
type ChatLimitEntry struct {
	ChatID   int64  `json:"chat_id"`
	Title    string `json:"title,omitempty" untrusted:"true"`
	Username string `json:"username,omitempty" untrusted:"true"`
	Messages int    `json:"messages"`
	Saved    int    `json:"saved"`
}

// ChatLimitListResult lists the limited chats that access.json leaves open, by
// chat id.
type ChatLimitListResult struct {
	Chats []ChatLimitEntry `json:"chats"`
}
