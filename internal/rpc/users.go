package rpc

import (
	"encoding/json"
	"strings"
	"tg-cli/internal/chatref"
)

type UserRefParams struct {
	User string `json:"user"`
}
type BlockParams struct {
	User   string `json:"user"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type BlockResult struct {
	UserID  int64 `json:"user_id"`
	Blocked bool  `json:"blocked"`
	DryRun  bool  `json:"dry_run,omitempty"`
}
type UserProfile struct {
	ID               int64    `json:"id"`
	Type             string   `json:"type"`
	FirstName        string   `json:"first_name" untrusted:"true"`
	LastName         string   `json:"last_name" untrusted:"true"`
	Username         string   `json:"username" untrusted:"true"`
	Usernames        []string `json:"usernames" untrusted:"true"`
	Phone            string   `json:"phone,omitempty"`
	Bio              string   `json:"bio" untrusted:"true"`
	IsContact        bool     `json:"is_contact"`
	MutualContact    bool     `json:"mutual_contact"`
	Blocked          bool     `json:"blocked"`
	Verified         bool     `json:"verified"`
	Premium          bool     `json:"premium"`
	Scam             bool     `json:"scam"`
	Fake             bool     `json:"fake"`
	Deleted          bool     `json:"deleted"`
	Status           string   `json:"status"`
	LastSeen         string   `json:"last_seen,omitempty"`
	CommonChatsCount int      `json:"common_chats_count"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}
type ContactListParams struct {
	Limit  int  `json:"limit,omitempty"`
	Offset int  `json:"offset,omitempty"`
	Local  bool `json:"local,omitempty"`
}
type Contact struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name" untrusted:"true"`
	LastName  string `json:"last_name" untrusted:"true"`
	Username  string `json:"username" untrusted:"true"`
	Phone     string `json:"phone,omitempty"`
	Mutual    *bool  `json:"mutual,omitempty"`
	Deleted   bool   `json:"deleted"`
	IsBot     bool   `json:"-"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}
type ContactListResult struct {
	Contacts   []Contact `json:"contacts"`
	NextOffset int       `json:"next_offset,omitempty"`
}
type ContactAddParams struct {
	User      string `json:"user"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Phone     string `json:"phone,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
}
type ContactAddResult struct {
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw       json.RawMessage `json:"-"`
	UserID    int64           `json:"user_id"`
	FirstName string          `json:"first_name" untrusted:"true"`
	LastName  string          `json:"last_name" untrusted:"true"`
	IsContact bool            `json:"is_contact"`
	Mutual    bool            `json:"mutual"`
	DryRun    bool            `json:"dry_run,omitempty"`
}
type ContactDeleteParams struct {
	User   string `json:"user"`
	Yes    bool   `json:"yes,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type ContactDeleteResult struct {
	UserID  int64 `json:"user_id"`
	Deleted bool  `json:"deleted"`
	DryRun  bool  `json:"dry_run,omitempty"`
}
type ContactSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Local bool   `json:"local,omitempty"`
}
type ContactHit struct {
	Contact
	Source string `json:"source"`
}
type ContactSearchResult struct {
	Contacts []ContactHit `json:"contacts"`
}

func ValidateUserRef(value string, allowPhone bool) error {
	ref, err := chatref.Parse(value)
	if err != nil || ref.Kind == chatref.Invite || (ref.Kind == chatref.ID && ref.ID <= 0) || (!allowPhone && ref.Kind == chatref.Phone) {
		return NewError(CodeInvalidParams, "a valid user reference is required")
	}
	return nil
}
func ValidateContactListParams(p ContactListParams) error {
	if p.Limit < 0 || p.Limit > 1000 || p.Offset < 0 {
		return NewError(CodeInvalidParams, "invalid contact page")
	}
	return nil
}
func ValidateContactSearchParams(p ContactSearchParams) error {
	if strings.TrimSpace(p.Query) == "" || p.Limit < 0 || p.Limit > 100 {
		return NewError(CodeInvalidParams, "invalid contact search")
	}
	return nil
}
