package rpc

const (
	MethodChatDraftGet   = "chat.draft_get"
	MethodChatDraftList  = "chat.draft_list"
	MethodChatDraftSet   = "chat.draft_set"
	MethodChatDraftClear = "chat.draft_clear"
)

type DraftParams struct {
	Chat    string `json:"chat,omitempty"`
	Text    string `json:"text,omitempty"`
	ReplyTo int    `json:"reply_to,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}
type Draft struct {
	ChatID  int64  `json:"chat_id"`
	Text    string `json:"text" untrusted:"true"`
	Date    string `json:"date,omitempty"`
	ReplyTo int    `json:"reply_to,omitempty"`
	Empty   bool   `json:"empty"`
}
type DraftList struct {
	Drafts []Draft `json:"drafts"`
}
type DraftWriteResult struct {
	ChatID     int64 `json:"chat_id"`
	TextLength int   `json:"text_length,omitempty"`
	Cleared    bool  `json:"cleared,omitempty"`
	Replaces   bool  `json:"replaces,omitempty"`
	DryRun     bool  `json:"dry_run,omitempty"`
}
