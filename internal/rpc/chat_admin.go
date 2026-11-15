package rpc

import (
	"time"
	"unicode/utf8"
)

const (
	MethodChatJoin        = "chat.join"
	MethodChatLeave       = "chat.leave"
	MethodChatCreate      = "chat.create"
	MethodChatEdit        = "chat.edit"
	MethodChatKick        = "chat.kick"
	MethodChatBan         = "chat.ban"
	MethodChatUnban       = "chat.unban"
	MethodChatRestrict    = "chat.restrict"
	MethodChatPermissions = "chat.permissions"
	MethodChatSlowMode    = "chat.slow_mode"
)

type ChatJoinParams struct {
	Chat   string `json:"chat"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type ChatJoinResult struct {
	ChatID        int64  `json:"chat_id"`
	Type          string `json:"type,omitempty"`
	AlreadyMember bool   `json:"already_member,omitempty"`
	Requested     bool   `json:"requested,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	Action        string `json:"action,omitempty"`
	Count         int    `json:"count,omitempty"`
}
type ChatLeaveParams struct {
	Chat   string `json:"chat"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type ChatLeaveResult struct {
	ChatID int64  `json:"chat_id"`
	Left   bool   `json:"left"`
	DryRun bool   `json:"dry_run,omitempty"`
	Action string `json:"action,omitempty"`
	Count  int    `json:"count,omitempty"`
}
type ChatCreateParams struct {
	Title  string `json:"title"`
	Type   string `json:"type"`
	About  string `json:"about,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type ChatCreateResult struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title,omitempty"`
	Closed      bool   `json:"closed,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Action      string `json:"action,omitempty"`
	Count       int    `json:"count,omitempty"`
	TitleLength *int   `json:"title_length,omitempty"`
	AboutLength *int   `json:"about_length,omitempty"`
}
type ChatEditParams struct {
	Chat   string  `json:"chat"`
	Title  *string `json:"title,omitempty"`
	About  *string `json:"about,omitempty"`
	DryRun bool    `json:"dry_run,omitempty"`
}
type ChatEditResult struct {
	ChatID       int64  `json:"chat_id"`
	TitleChanged bool   `json:"title_changed"`
	AboutChanged bool   `json:"about_changed"`
	DryRun       bool   `json:"dry_run,omitempty"`
	Action       string `json:"action,omitempty"`
	Count        int    `json:"count,omitempty"`
	TitleLength  *int   `json:"title_length,omitempty"`
	AboutLength  *int   `json:"about_length,omitempty"`
}
type ChatMemberActionParams struct {
	Chat   string `json:"chat"`
	User   string `json:"user"`
	Until  string `json:"until,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type ChatRestrictParams struct {
	Chat   string   `json:"chat"`
	User   string   `json:"user"`
	Deny   []string `json:"deny,omitempty"`
	Clear  bool     `json:"clear,omitempty"`
	Until  string   `json:"until,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}
type ChatPermissionsParams struct {
	Chat   string   `json:"chat"`
	Deny   []string `json:"deny,omitempty"`
	Clear  bool     `json:"clear,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}
type ChatSlowModeParams struct {
	Chat    string `json:"chat"`
	Seconds int    `json:"seconds"`
	DryRun  bool   `json:"dry_run,omitempty"`
}
type ChatAdminResult struct {
	ChatID  int64    `json:"chat_id"`
	UserID  int64    `json:"user_id,omitempty"`
	Kicked  bool     `json:"kicked,omitempty"`
	Banned  *bool    `json:"banned,omitempty"`
	Denied  []string `json:"denied,omitempty"`
	Until   string   `json:"until,omitempty"`
	Seconds *int     `json:"seconds,omitempty"`
	DryRun  bool     `json:"dry_run,omitempty"`
	Action  string   `json:"action,omitempty"`
	Count   int      `json:"count,omitempty"`
}

// ChatOwnAdminRights reports freshly fetched permissions for dry-run checks.
type ChatOwnAdminRights struct {
	Creator     bool `json:"creator"`
	ChangeInfo  bool `json:"change_info"`
	BanUsers    bool `json:"ban_users"`
	InviteUsers bool `json:"invite_users"`
}

var ChatAdminRights = []string{"send-plain", "send-media", "send-photos", "send-videos", "send-roundvideos", "send-audios", "send-voices", "send-docs", "send-stickers", "send-gifs", "send-games", "send-inline", "send-polls", "embed-links", "send-reactions", "change-info", "invite-users", "pin-messages", "manage-topics"}

func ValidateRights(deny []string, clear bool) error {
	if clear == (len(deny) > 0) {
		return NewError(CodeInvalidParams, "specify either deny or clear")
	}
	seen := make(map[string]bool, len(deny))
	for _, name := range deny {
		found := false
		for _, allowed := range ChatAdminRights {
			if name == allowed {
				found = true
				break
			}
		}
		if !found || seen[name] {
			return NewError(CodeInvalidParams, "unknown or duplicate denied right: "+name)
		}
		seen[name] = true
	}
	return nil
}
func ValidateUntil(value string, now time.Time) error {
	if value == "" {
		return nil
	}
	until, err := time.Parse(time.RFC3339, value)
	if err != nil || until.Before(now.Add(30*time.Second)) || until.After(now.Add(366*24*time.Hour)) || until.Unix() > 2147483647 {
		return NewError(CodeInvalidParams, "until must be RFC 3339 between 30 seconds and 366 days from now")
	}
	return nil
}
func ValidateSlowModeSeconds(seconds int) error {
	switch seconds {
	case 0, 10, 30, 60, 300, 900, 3600:
		return nil
	}
	return NewError(CodeInvalidParams, "invalid slow mode seconds")
}
func ValidateChatTitle(title string) error {
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) < 1 || utf8.RuneCountInString(title) > 128 {
		return NewError(CodeInvalidParams, "title must be 1 to 128 characters")
	}
	return nil
}
func ValidateChatAbout(about string) error {
	if !utf8.ValidString(about) || utf8.RuneCountInString(about) > 255 {
		return NewError(CodeInvalidParams, "about must be at most 255 characters")
	}
	return nil
}
func ErrAdminDisabled() *Error {
	return &Error{Code: CodeWriteDisabled, Message: "admin access is disabled: add the chat to the admin list in the access file", Data: map[string]any{"list": "admin"}}
}
