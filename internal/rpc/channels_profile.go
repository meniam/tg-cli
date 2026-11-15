package rpc

import (
	"strconv"
	"time"
)

const MethodMessageReaders = "message.readers"

type MessageReader struct {
	UserID   int64  `json:"user_id"`
	Name     string `json:"name,omitempty" untrusted:"true"`
	Username string `json:"username,omitempty" untrusted:"true"`
	Date     string `json:"date"`
}
type MessageReadersResult struct {
	ChatID  int64           `json:"chat_id"`
	ID      int             `json:"id"`
	Readers []MessageReader `json:"readers"`
}

const MethodAccountProfile = "account.profile"

type Birthday struct {
	Day   int `json:"day"`
	Month int `json:"month"`
	Year  int `json:"year,omitempty"`
}
type EmojiStatus struct {
	DocumentID  int64  `json:"document_id"`
	Until       string `json:"until,omitempty"`
	Collectible bool   `json:"collectible,omitempty"`
}
type AccountProfile struct {
	ID                int64        `json:"id"`
	FirstName         string       `json:"first_name" untrusted:"true"`
	LastName          string       `json:"last_name" untrusted:"true"`
	Username          string       `json:"username" untrusted:"true"`
	Bio               string       `json:"bio" untrusted:"true"`
	Premium           bool         `json:"premium"`
	Birthday          *Birthday    `json:"birthday,omitempty"`
	PersonalChannelID int64        `json:"personal_channel_id,omitempty"`
	EmojiStatus       *EmojiStatus `json:"emoji_status,omitempty"`
}

const MethodAccountEmojiStatuses = "account.emoji_statuses"

type EmojiStatusesParams struct {
	Recent bool `json:"recent,omitempty"`
}
type EmojiStatusOption struct {
	DocumentID  int64  `json:"document_id"`
	Until       string `json:"until,omitempty"`
	Collectible bool   `json:"collectible,omitempty"`
	Title       string `json:"title,omitempty" untrusted:"true"`
	Slug        string `json:"slug,omitempty" untrusted:"true"`
}
type EmojiStatusesResult struct {
	Statuses []EmojiStatusOption `json:"statuses"`
}

const (
	MethodChatApprove = "chat.approve"
	MethodChatDecline = "chat.decline"
)

type JoinRequestActionParams struct {
	Chat   string `json:"chat"`
	User   string `json:"user"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type JoinRequestActionResult struct {
	ChatID   int64 `json:"chat_id"`
	UserID   int64 `json:"user_id"`
	Approved bool  `json:"approved"`
	DryRun   bool  `json:"dry_run,omitempty"`
}

const MethodChatSet = "chat.set"

type ChatSetParams struct {
	Chat              string  `json:"chat"`
	Signatures        *string `json:"signatures,omitempty"`
	SignatureProfiles *string `json:"signature_profiles,omitempty"`
	Antispam          *string `json:"antispam,omitempty"`
	Forum             *string `json:"forum,omitempty"`
	Prehistory        *string `json:"prehistory,omitempty"`
	JoinRequests      *string `json:"join_requests,omitempty"`
	JoinToSend        *string `json:"join_to_send,omitempty"`
	DryRun            bool    `json:"dry_run,omitempty"`
}
type SettingChange struct {
	Flag    string `json:"flag"`
	Value   string `json:"value"`
	Changed bool   `json:"changed"`
}
type SettingError struct {
	Flag    string `json:"flag"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ChatSetResult struct {
	ChatID  int64           `json:"chat_id"`
	Changes []SettingChange `json:"changes"`
	Partial bool            `json:"partial,omitempty"`
	Errors  []SettingError  `json:"errors,omitempty"`
	DryRun  bool            `json:"dry_run,omitempty"`
}

func (r ChatSetResult) IsPartial() bool { return r.Partial }
func (p ChatSetParams) Changes() []SettingChange {
	out := []SettingChange{}
	for _, item := range []struct {
		name  string
		value *string
	}{{"signatures", p.Signatures}, {"signature-profiles", p.SignatureProfiles}, {"antispam", p.Antispam}, {"forum", p.Forum}, {"prehistory", p.Prehistory}, {"join-requests", p.JoinRequests}, {"join-to-send", p.JoinToSend}} {
		if item.value != nil {
			out = append(out, SettingChange{Flag: item.name, Value: *item.value})
		}
	}
	return out
}
func (p ChatSetParams) Validate() error {
	changes := p.Changes()
	if len(changes) == 0 {
		return NewError(CodeInvalidParams, "at least one setting is required")
	}
	for _, c := range changes {
		if c.Flag == "prehistory" {
			if c.Value != "visible" && c.Value != "hidden" {
				return NewError(CodeInvalidParams, "prehistory must be visible or hidden")
			}
		} else if c.Value != "on" && c.Value != "off" {
			return NewError(CodeInvalidParams, c.Flag+" must be on or off")
		}
	}
	return nil
}

const MethodAccountSet = "account.set"

type AccountSetParams struct {
	Birthday         *string `json:"birthday,omitempty"`
	PersonalChannel  *string `json:"personal_channel,omitempty"`
	EmojiStatus      *string `json:"emoji_status,omitempty"`
	EmojiStatusUntil string  `json:"emoji_status_until,omitempty"`
	DryRun           bool    `json:"dry_run,omitempty"`
}
type AccountSetResult struct {
	Changes []SettingChange `json:"changes"`
	Partial bool            `json:"partial,omitempty"`
	Errors  []SettingError  `json:"errors,omitempty"`
	DryRun  bool            `json:"dry_run,omitempty"`
}

func (r AccountSetResult) IsPartial() bool { return r.Partial }
func (p AccountSetParams) Validate(now time.Time) error {
	if p.Birthday == nil && p.PersonalChannel == nil && p.EmojiStatus == nil {
		return NewError(CodeInvalidParams, "at least one setting is required")
	}
	if p.Birthday != nil && *p.Birthday != "none" {
		if _, err := ParseBirthday(*p.Birthday, now); err != nil {
			return err
		}
	}
	if p.PersonalChannel != nil && *p.PersonalChannel == "" {
		return NewError(CodeInvalidParams, "personal channel is required")
	}
	if p.EmojiStatus != nil && *p.EmojiStatus != "none" {
		id, err := strconv.ParseInt(*p.EmojiStatus, 10, 64)
		if err != nil || id <= 0 {
			return NewError(CodeInvalidParams, "emoji status must be a positive document id or none")
		}
	}
	if p.EmojiStatusUntil != "" {
		if p.EmojiStatus == nil || *p.EmojiStatus == "none" {
			return NewError(CodeInvalidParams, "emoji status until requires a status")
		}
		until, err := time.Parse(time.RFC3339, p.EmojiStatusUntil)
		if err != nil || !until.After(now) || until.Unix() > 2147483647 {
			return NewError(CodeInvalidParams, "emoji status until must be a future RFC 3339 time")
		}
	}
	return nil
}
func ParseBirthday(value string, now time.Time) (Birthday, error) {
	invalid := NewError(CodeInvalidParams, "birthday must be YYYY-MM-DD, MM-DD or none")
	var parsed time.Time
	var err error
	year := 0
	switch len(value) {
	case 5:
		parsed, err = time.Parse("2006-01-02", "2000-"+value)
	case 10:
		parsed, err = time.Parse("2006-01-02", value)
		if err == nil {
			year = parsed.Year()
			if year < 1900 || year > now.Year() {
				return Birthday{}, invalid
			}
		}
	default:
		return Birthday{}, invalid
	}
	if err != nil {
		return Birthday{}, invalid
	}
	return Birthday{Day: parsed.Day(), Month: int(parsed.Month()), Year: year}, nil
}

const MethodChatRequests = "chat.requests"

type JoinRequestsParams struct {
	Chat   string `json:"chat"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Search string `json:"search,omitempty"`
}
type JoinRequest struct {
	IsBot      bool   `json:"-"`
	AccessHash int64  `json:"-"`
	UserID     int64  `json:"user_id"`
	Name       string `json:"name" untrusted:"true"`
	Username   string `json:"username,omitempty" untrusted:"true"`
	Date       string `json:"date"`
	About      string `json:"about,omitempty" untrusted:"true"`
}
type JoinRequestsResult struct {
	Requests   []JoinRequest `json:"requests"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

const (
	MethodAccountPrivacyGet = "account.privacy_get"
	MethodAccountPrivacySet = "account.privacy_set"
)

var PrivacyKeys = []string{"last-seen", "phone-number", "added-by-phone", "profile-photo", "forwards", "calls", "p2p-calls", "invites", "voice-messages", "bio", "birthday", "gifts-auto-save", "no-paid-messages", "saved-music"}

type PrivacyGetParams struct {
	Key string `json:"key"`
}
type PrivacySetParams struct {
	Key               string   `json:"key"`
	Default           string   `json:"default"`
	AllowUsers        []string `json:"allow_users,omitempty"`
	DenyUsers         []string `json:"deny_users,omitempty"`
	AllowChats        []string `json:"allow_chats,omitempty"`
	DenyChats         []string `json:"deny_chats,omitempty"`
	AllowPremium      bool     `json:"allow_premium,omitempty"`
	AllowBots         bool     `json:"allow_bots,omitempty"`
	DenyBots          bool     `json:"deny_bots,omitempty"`
	AllowCloseFriends bool     `json:"allow_close_friends,omitempty"`
	ReplaceHidden     bool     `json:"replace_hidden,omitempty"`
	Yes               bool     `json:"yes,omitempty"`
	DryRun            bool     `json:"dry_run,omitempty"`
}
type PrivacyRules struct {
	Key               string  `json:"key"`
	Default           string  `json:"default"`
	AllowUsers        []int64 `json:"allow_users"`
	DenyUsers         []int64 `json:"deny_users"`
	AllowChats        []int64 `json:"allow_chats"`
	DenyChats         []int64 `json:"deny_chats"`
	AllowPremium      bool    `json:"allow_premium"`
	AllowBots         bool    `json:"allow_bots"`
	DenyBots          bool    `json:"deny_bots"`
	AllowCloseFriends bool    `json:"allow_close_friends"`
	Hidden            int     `json:"hidden,omitempty"`
	DryRun            bool    `json:"dry_run,omitempty"`
}

func ValidPrivacyKey(key string) bool {
	for _, v := range PrivacyKeys {
		if v == key {
			return true
		}
	}
	return false
}
func (p PrivacySetParams) Validate() error {
	if !ValidPrivacyKey(p.Key) {
		return NewError(CodeInvalidParams, "unknown privacy key")
	}
	if p.Default != "everybody" && p.Default != "contacts" && p.Default != "nobody" {
		return NewError(CodeInvalidParams, "default must be everybody, contacts or nobody")
	}
	if p.AllowBots && p.DenyBots {
		return NewError(CodeInvalidParams, "allow-bots and deny-bots conflict")
	}
	if !p.Yes && !p.DryRun {
		return NewError(CodeInvalidParams, "add --yes to confirm or --dry-run to preview")
	}
	return nil
}
