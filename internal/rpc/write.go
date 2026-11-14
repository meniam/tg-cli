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
	DryRun bool   `json:"dry_run,omitempty"`
}

type ForwardParams struct {
	From   string `json:"from"`
	To     string `json:"to"`
	IDs    []int  `json:"ids"`
	Silent bool   `json:"silent,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type SendResult struct {
	ChatID int64  `json:"chat_id"`
	ID     int    `json:"id,omitempty"`
	Date   string `json:"date,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type DeleteResult struct {
	ChatID  int64 `json:"chat_id"`
	Deleted []int `json:"deleted"`
	DryRun  bool  `json:"dry_run,omitempty"`
}

type ForwardedMessage struct {
	ID   int    `json:"id"`
	Date string `json:"date"`
}

type ForwardResult struct {
	ChatID   int64              `json:"chat_id"`
	Messages []ForwardedMessage `json:"messages"`
	DryRun   bool               `json:"dry_run,omitempty"`
}

// ErrWriteDisabled tells the caller how the owner can enable this chat.
func ErrWriteDisabled() *Error {
	return NewError(CodeWriteDisabled, "writing to this chat is disabled: add the chat to the write list in access.json")
}
