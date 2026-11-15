package rpc

import (
	"net/url"
	"strconv"
	"strings"
)

const (
	MethodMessageButtons     = "message.buttons"
	MethodMessagePress       = "message.press"
	MethodMessageInlineQuery = "message.inline_query"
	MethodMessageSendInline  = "message.send_inline"
	MethodMessageEditMedia   = "message.edit_media"
	MethodMessageSendSticker = "message.send_sticker"
	MethodStickerSearch      = "sticker.search"
	MethodStickerSet         = "sticker.set"
	MethodStickerInstalled   = "sticker.installed"
	MethodChatAutoDelete     = "chat.auto_delete"
)

type ButtonsParams struct {
	Chat string `json:"chat"`
	ID   int    `json:"id"`
}
type Button struct {
	Index     int    `json:"index"`
	Row       int    `json:"row"`
	Column    int    `json:"column"`
	Text      string `json:"text" untrusted:"true"`
	Type      string `json:"type"`
	URL       string `json:"url,omitempty" untrusted:"true"`
	CopyText  string `json:"copy_text,omitempty" untrusted:"true"`
	UserID    int64  `json:"user_id,omitempty"`
	Pressable bool   `json:"pressable"`
}
type ButtonsResult struct {
	ChatID    int64    `json:"chat_id"`
	MessageID int      `json:"message_id"`
	Keyboard  string   `json:"keyboard"`
	Buttons   []Button `json:"buttons"`
}
type PressParams struct {
	Chat   string  `json:"chat"`
	ID     int     `json:"id"`
	Button *int    `json:"button,omitempty"`
	Text   *string `json:"text,omitempty"`
	DryRun bool    `json:"dry_run,omitempty"`
}
type PressButton struct {
	Index int    `json:"index"`
	Text  string `json:"text" untrusted:"true"`
	Type  string `json:"type"`
}
type PressResult struct {
	ChatID    int64       `json:"chat_id"`
	MessageID int         `json:"message_id"`
	Button    PressButton `json:"button"`
	Answered  bool        `json:"answered"`
	Alert     bool        `json:"alert,omitempty"`
	Message   string      `json:"message,omitempty" untrusted:"true"`
	URL       string      `json:"url,omitempty" untrusted:"true"`
	DryRun    bool        `json:"dry_run,omitempty"`
}
type InlineQueryParams struct {
	Chat   string `json:"chat"`
	Bot    string `json:"bot"`
	Query  string `json:"query"`
	Offset string `json:"offset,omitempty"`
}
type InlineResult struct {
	ID          string `json:"id" untrusted:"true"`
	Type        string `json:"type" untrusted:"true"`
	Title       string `json:"title,omitempty" untrusted:"true"`
	Description string `json:"description,omitempty" untrusted:"true"`
	URL         string `json:"url,omitempty" untrusted:"true"`
	HasMedia    bool   `json:"has_media"`
}
type InlineQueryResult struct {
	ChatID     int64          `json:"chat_id"`
	BotID      int64          `json:"bot_id"`
	QueryID    string         `json:"query_id"`
	NextOffset string         `json:"next_offset,omitempty" untrusted:"true"`
	Gallery    bool           `json:"gallery"`
	Results    []InlineResult `json:"results"`
	CacheTime  int            `json:"-"`
}
type SendInlineParams struct {
	Chat           string `json:"chat"`
	QueryID        string `json:"query_id"`
	Result         string `json:"result"`
	ReplyTo        int    `json:"reply_to,omitempty"`
	Topic          int    `json:"topic,omitempty"`
	Silent         bool   `json:"silent,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	AccountID      int64  `json:"-"`
	DryRun         bool   `json:"dry_run,omitempty"`
}
type EditMediaParams struct {
	Chat     string  `json:"chat"`
	ID       int     `json:"id"`
	As       string  `json:"as"`
	File     string  `json:"file"`
	Caption  *string `json:"caption,omitempty"`
	Duration int     `json:"duration,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	DryRun   bool    `json:"dry_run,omitempty"`
}
type EditMediaResult struct {
	ChatID        int64  `json:"chat_id"`
	ID            int    `json:"id"`
	Date          string `json:"date,omitempty"`
	Action        string `json:"action"`
	MediaType     string `json:"media_type"`
	Size          int64  `json:"size"`
	CaptionLength *int   `json:"caption_length,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
}
type StickerSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}
type StickerSetParams struct {
	Name string `json:"name"`
}
type StickerSetInfo struct {
	ShortName string `json:"short_name" untrusted:"true"`
	Title     string `json:"title" untrusted:"true"`
	Count     int    `json:"count"`
	Type      string `json:"type"`
	Official  bool   `json:"official"`
	Animated  bool   `json:"animated"`
	Video     bool   `json:"video"`
}
type Sticker struct {
	Index   int    `json:"index"`
	Emoji   string `json:"emoji" untrusted:"true"`
	Type    string `json:"type"`
	Premium bool   `json:"premium"`
}
type StickerSetResult struct {
	Set      StickerSetInfo `json:"set"`
	Stickers []Sticker      `json:"stickers"`
}
type StickerSetsResult struct {
	Sets []StickerSetSummary `json:"sets"`
}
type StickerSetSummary struct {
	ShortName string `json:"short_name" untrusted:"true"`
	Title     string `json:"title" untrusted:"true"`
	Count     int    `json:"count"`
	Type      string `json:"type"`
	Official  bool   `json:"official"`
}
type SendStickerParams struct {
	Chat           string  `json:"chat"`
	Set            string  `json:"set"`
	Index          *int    `json:"index,omitempty"`
	Emoji          *string `json:"emoji,omitempty"`
	ReplyTo        int     `json:"reply_to,omitempty"`
	Topic          int     `json:"topic,omitempty"`
	Silent         bool    `json:"silent,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
	AccountID      int64   `json:"-"`
	DryRun         bool    `json:"dry_run,omitempty"`
}
type AutoDeleteParams struct {
	Chat   string `json:"chat"`
	Period string `json:"period"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type AutoDeleteResult struct {
	ChatID    int64 `json:"chat_id"`
	TTLPeriod int   `json:"ttl_period"`
	DryRun    bool  `json:"dry_run,omitempty"`
}

func ParseAutoDeletePeriod(s string) (int, error) {
	switch s {
	case "off":
		return 0, nil
	case "1d":
		return 86400, nil
	case "1w":
		return 604800, nil
	case "1m":
		return 2592000, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 86400 || n > 365*86400 {
		return 0, NewError(CodeInvalidParams, "period must be off, 1d, 1w, 1m or seconds from 86400 to 31536000")
	}
	return n, nil
}
func ParseSelfDestruct(s string) (int, error) {
	if s == "view-once" {
		return 0x7fffffff, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > 60 {
		return 0, NewError(CodeInvalidParams, "self-destruct must be view-once or 1 to 60 seconds")
	}
	return n, nil
}
func StickerShortName(s string) (string, error) {
	if strings.Contains(s, "/") {
		if strings.HasPrefix(strings.ToLower(s), "t.me/") {
			s = "https://" + s
		}
		u, e := url.Parse(s)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "t.me") || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/addstickers/") {
			return "", NewError(CodeInvalidParams, "invalid sticker set name")
		}
		s = strings.TrimPrefix(u.Path, "/addstickers/")
	}
	if s == "" {
		return "", NewError(CodeInvalidParams, "invalid sticker set name")
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "", NewError(CodeInvalidParams, "invalid sticker set name")
		}
	}
	return s, nil
}

type SendStickerResult struct {
	SendResult
	Sticker *Sticker `json:"sticker,omitempty"`
}
