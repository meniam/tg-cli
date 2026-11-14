package rpc

// SendParams sends plain text to a chat. The caller validates authorization.
type SendParams struct {
	Chat    string `json:"chat"`
	Text    string `json:"text"`
	ReplyTo int    `json:"reply_to,omitempty"`
	Topic   int    `json:"topic,omitempty"`
	Silent  bool   `json:"silent,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

type EditParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	Text   string `json:"text"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type DeleteParams struct {
	Chat   string `json:"chat"`
	IDs    []int  `json:"ids"`
	Revoke bool   `json:"revoke"`
	ForMe  bool   `json:"for_me,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type ForwardParams struct {
	From   string `json:"from"`
	To     string `json:"to"`
	IDs    []int  `json:"ids"`
	Silent bool   `json:"silent,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type ReactParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	Emoji  string `json:"emoji"`
	Remove bool   `json:"remove,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type PinParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	Silent bool   `json:"silent,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type UnpinParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type PollParams struct {
	Chat        string   `json:"chat"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Multiple    bool     `json:"multiple,omitempty"`
	Public      bool     `json:"public,omitempty"`
	Quiz        bool     `json:"quiz,omitempty"`
	Correct     int      `json:"correct,omitempty"`
	Explanation string   `json:"explanation,omitempty"`
	ReplyTo     int      `json:"reply_to,omitempty"`
	Topic       int      `json:"topic,omitempty"`
	Silent      bool     `json:"silent,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

type SendResult struct {
	ChatID     int64  `json:"chat_id"`
	ID         int    `json:"id,omitempty"`
	Date       string `json:"date,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
	Action     string `json:"action,omitempty"`
	Count      int    `json:"count,omitempty"`
	TextLength int    `json:"text_length,omitempty"`
}

type ReactResult struct {
	ChatID  int64  `json:"chat_id"`
	ID      int    `json:"id,omitempty"`
	Emoji   string `json:"emoji,omitempty"`
	Removed bool   `json:"removed"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Action  string `json:"action,omitempty"`
	Count   int    `json:"count,omitempty"`
}

type PinResult struct {
	ChatID int64  `json:"chat_id"`
	ID     int    `json:"id,omitempty"`
	Pinned bool   `json:"pinned"`
	DryRun bool   `json:"dry_run,omitempty"`
	Action string `json:"action,omitempty"`
	Count  int    `json:"count,omitempty"`
}

type DeleteResult struct {
	ChatID  int64  `json:"chat_id"`
	Deleted []int  `json:"deleted"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Action  string `json:"action,omitempty"`
	Count   int    `json:"count,omitempty"`
}

type ForwardedMessage struct {
	ID   int    `json:"id"`
	Date string `json:"date"`
}

type ForwardResult struct {
	ChatID   int64              `json:"chat_id"`
	Messages []ForwardedMessage `json:"messages"`
	DryRun   bool               `json:"dry_run,omitempty"`
	Action   string             `json:"action,omitempty"`
	Count    int                `json:"count,omitempty"`
}

// ErrWriteDisabled tells the caller how the owner can enable this chat.
func ErrWriteDisabled() *Error {
	return NewError(CodeWriteDisabled, "writing to this chat is disabled: add the chat to the write list in access.json")
}
