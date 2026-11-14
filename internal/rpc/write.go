package rpc

import "os"

// SendParams sends text to a chat. The caller validates authorization.
type SendParams struct {
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	AccountID      int64    `json:"-"`
	Chat           string   `json:"chat"`
	Text           string   `json:"text"`
	ReplyTo        int      `json:"reply_to,omitempty"`
	Topic          int      `json:"topic,omitempty"`
	Silent         bool     `json:"silent,omitempty"`
	DryRun         bool     `json:"dry_run,omitempty"`
	Parse          string   `json:"parse,omitempty"`
	Entities       []Entity `json:"entities,omitempty"`
}

// SendMediaParams carries only the request description, never file contents.
type SendMediaParams struct {
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	AccountID      int64    `json:"-"`
	Chat           string   `json:"chat"`
	As             string   `json:"as"`
	Files          []string `json:"files"`
	Caption        string   `json:"caption,omitempty"`
	ReplyTo        int      `json:"reply_to,omitempty"`
	Topic          int      `json:"topic,omitempty"`
	Silent         bool     `json:"silent,omitempty"`
	DryRun         bool     `json:"dry_run,omitempty"`
	Duration       int      `json:"duration,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
}

// MediaFile is a checked, already-open file owned by the caller.
type MediaFile struct {
	Name     string   `json:"-"`
	Size     int64    `json:"-"`
	File     *os.File `json:"-"`
	Duration int      `json:"-"`
	Width    int      `json:"-"`
	Height   int      `json:"-"`
	MIME     string   `json:"-"`
}

type MessageRef = ForwardedMessage

type SendMediaResult struct {
	ReplayAvailable *bool        `json:"replay_available,omitempty"`
	Replayed        bool         `json:"replayed,omitempty"`
	ChatID          int64        `json:"chat_id"`
	Messages        []MessageRef `json:"messages"`
	DryRun          bool         `json:"dry_run,omitempty"`
	Action          string       `json:"action,omitempty"`
	Count           int          `json:"count,omitempty"`
	Types           []string     `json:"types,omitempty"`
	Sizes           []int64      `json:"sizes,omitempty"`
	CaptionLength   int          `json:"caption_length,omitempty"`
}

type EditParams struct {
	Chat     string   `json:"chat"`
	ID       int      `json:"id"`
	Text     string   `json:"text"`
	DryRun   bool     `json:"dry_run,omitempty"`
	Parse    string   `json:"parse,omitempty"`
	Entities []Entity `json:"entities,omitempty"`
}

// Entity describes a Telegram message entity. Offset and Length use UTF-16 units.
type Entity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	URL           string `json:"url,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	Language      string `json:"language,omitempty"`
	CustomEmojiID int64  `json:"custom_emoji_id,omitempty"`
	Collapsed     bool   `json:"collapsed,omitempty"`
	DateUnix      int    `json:"date_unix,omitempty"`
	Format        string `json:"format,omitempty"`
}

type DeleteParams struct {
	Chat   string `json:"chat"`
	IDs    []int  `json:"ids"`
	Revoke bool   `json:"revoke"`
	ForMe  bool   `json:"for_me,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type ForwardParams struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	AccountID      int64  `json:"-"`
	From           string `json:"from"`
	To             string `json:"to"`
	IDs            []int  `json:"ids"`
	Silent         bool   `json:"silent,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
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
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	AccountID      int64    `json:"-"`
	Chat           string   `json:"chat"`
	Question       string   `json:"question"`
	Options        []string `json:"options"`
	Multiple       bool     `json:"multiple,omitempty"`
	Public         bool     `json:"public,omitempty"`
	Quiz           bool     `json:"quiz,omitempty"`
	Correct        int      `json:"correct,omitempty"`
	Explanation    string   `json:"explanation,omitempty"`
	ReplyTo        int      `json:"reply_to,omitempty"`
	Topic          int      `json:"topic,omitempty"`
	Silent         bool     `json:"silent,omitempty"`
	DryRun         bool     `json:"dry_run,omitempty"`
}

type SendResult struct {
	ReplayAvailable *bool  `json:"replay_available,omitempty"`
	Replayed        bool   `json:"replayed,omitempty"`
	ChatID          int64  `json:"chat_id"`
	ID              int    `json:"id,omitempty"`
	Date            string `json:"date,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
	Action          string `json:"action,omitempty"`
	Count           int    `json:"count,omitempty"`
	TextLength      int    `json:"text_length,omitempty"`
	Mode            string `json:"mode,omitempty"`
	PlainLength     *int   `json:"plain_length,omitempty"`
	EntityCount     *int   `json:"entities,omitempty"`
	RichMode        *bool  `json:"rich,omitempty"`
}

// TextCheck is a local formatting preview without a Telegram write.
type TextCheck struct {
	Mode        string
	PlainLength int
	Entities    int
	Rich        bool
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
	ReplayAvailable *bool              `json:"replay_available,omitempty"`
	Replayed        bool               `json:"replayed,omitempty"`
	ChatID          int64              `json:"chat_id"`
	Messages        []ForwardedMessage `json:"messages"`
	DryRun          bool               `json:"dry_run,omitempty"`
	Action          string             `json:"action,omitempty"`
	Count           int                `json:"count,omitempty"`
}

// ErrWriteDisabled tells the caller how the owner can enable this chat.
func ErrWriteDisabled() *Error {
	return NewError(CodeWriteDisabled, "writing to this chat is disabled: add the chat to the write list in access.json")
}
