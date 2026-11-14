package rpc

const MethodChatTyping = "chat.typing"

type TypingParams struct {
	Chat   string `json:"chat"`
	Action string `json:"action"`
	Topic  int    `json:"topic,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type TypingResult struct {
	ChatID int64  `json:"chat_id"`
	Action string `json:"action"`
	DryRun bool   `json:"dry_run,omitempty"`
}

func ValidateTypingParams(p TypingParams) error {
	if p.Topic < 0 {
		return NewError(CodeInvalidParams, "topic id must not be negative")
	}
	switch p.Action {
	case "typing", "upload_photo", "upload_video", "upload_document", "record_voice", "upload_voice", "record_video", "choose_sticker", "cancel":
		return nil
	default:
		return NewError(CodeInvalidParams, "invalid typing action")
	}
}
