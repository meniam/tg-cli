package rpc

import "strconv"

const (
	MethodFolderList      = "folder.list"
	MethodChatState       = "chat.state"
	MethodAccountSessions = "account.sessions"
	MethodAccountBlocked  = "account.blocked"
	MethodAccountNotify   = "account.notify"
)

type LocalParams struct {
	Local bool `json:"local,omitempty"`
}
type ChatStateParams struct {
	Chat  string `json:"chat,omitempty"`
	Limit int    `json:"limit,omitempty"`
	Local bool   `json:"local,omitempty"`
}
type BlockedParams struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Local  bool   `json:"local,omitempty"`
}

type FolderRules struct {
	Contacts        bool `json:"contacts"`
	NonContacts     bool `json:"non_contacts"`
	Groups          bool `json:"groups"`
	Broadcasts      bool `json:"broadcasts"`
	Bots            bool `json:"bots"`
	ExcludeMuted    bool `json:"exclude_muted"`
	ExcludeRead     bool `json:"exclude_read"`
	ExcludeArchived bool `json:"exclude_archived"`
}
type FolderPeer struct {
	ID    int64  `json:"id"`
	Title string `json:"title,omitempty" untrusted:"true"`
}
type Folder struct {
	ID       int          `json:"id"`
	Kind     string       `json:"kind"`
	Position int          `json:"position"`
	Title    string       `json:"title" untrusted:"true"`
	Emoticon *string      `json:"emoticon"`
	Color    *int         `json:"color"`
	Rules    *FolderRules `json:"rules"`
	Pinned   []FolderPeer `json:"pinned"`
	Include  []FolderPeer `json:"include"`
	Exclude  []FolderPeer `json:"exclude"`
	SyncedAt *int64       `json:"synced_at"`
}
type FoldersResult struct {
	Folders  []Folder `json:"folders"`
	SyncedAt *int64   `json:"synced_at"`
}
type ChatState struct {
	ChatID          int64  `json:"chat_id"`
	ReadInboxMaxID  int    `json:"read_inbox_max_id"`
	ReadOutboxMaxID int    `json:"read_outbox_max_id"`
	Unread          int    `json:"unread"`
	UnreadMentions  int    `json:"unread_mentions"`
	UnreadReactions int    `json:"unread_reactions"`
	UnreadMark      bool   `json:"unread_mark"`
	Pinned          bool   `json:"pinned"`
	Archived        bool   `json:"archived"`
	TopMessage      int    `json:"top_message"`
	TTLPeriod       *int   `json:"ttl_period"`
	MuteUntil       *int   `json:"mute_until"`
	Silent          *bool  `json:"silent"`
	ShowPreviews    *bool  `json:"show_previews"`
	SyncedAt        *int64 `json:"synced_at"`
}
type ChatStatesResult struct {
	States   []ChatState `json:"states"`
	SyncedAt *int64      `json:"synced_at"`
}
type Session struct {
	Hash            int64  `json:"-"`
	Current         bool   `json:"current"`
	OfficialApp     bool   `json:"official_app"`
	PasswordPending bool   `json:"password_pending"`
	Unconfirmed     bool   `json:"unconfirmed"`
	DeviceModel     string `json:"device_model" untrusted:"true"`
	Platform        string `json:"platform" untrusted:"true"`
	SystemVersion   string `json:"system_version" untrusted:"true"`
	APIID           int    `json:"api_id"`
	AppName         string `json:"app_name" untrusted:"true"`
	AppVersion      string `json:"app_version" untrusted:"true"`
	DateCreated     int    `json:"date_created"`
	DateActive      int    `json:"date_active"`
	Country         string `json:"country"`
	Region          string `json:"region"`
}
type SessionsResult struct {
	Sessions             []Session `json:"sessions"`
	AuthorizationTTLDays int       `json:"authorization_ttl_days"`
	SyncedAt             *int64    `json:"synced_at"`
}
type BlockedPeer struct {
	PeerID   int64  `json:"peer_id"`
	Type     string `json:"type"`
	Title    string `json:"title" untrusted:"true"`
	Username string `json:"username,omitempty" untrusted:"true"`
	Date     int    `json:"date"`
}
type BlockedResult struct {
	Peers      []BlockedPeer `json:"peers"`
	NextCursor string        `json:"next_cursor,omitempty"`
	SyncedAt   *int64        `json:"synced_at"`
}
type NotifyDefault struct {
	Scope        string `json:"scope"`
	MuteUntil    *int   `json:"mute_until"`
	Silent       *bool  `json:"silent"`
	ShowPreviews *bool  `json:"show_previews"`
	SyncedAt     *int64 `json:"synced_at"`
}
type NotifyDefaultsResult struct {
	Defaults []NotifyDefault `json:"defaults"`
	SyncedAt *int64          `json:"synced_at"`
}

func ValidateChatStateParams(p ChatStateParams) error {
	if p.Limit < 0 || p.Limit > 500 || (p.Chat != "" && p.Limit != 0) {
		return NewError(CodeInvalidParams, "limit must be 1 to 500 and cannot accompany a chat")
	}
	return nil
}
func BlockedOffset(p BlockedParams) (int, error) {
	if p.Limit < 0 || p.Limit > 1000 || (p.Local && p.Cursor != "") {
		return 0, NewError(CodeInvalidParams, "invalid blocked limit or cursor")
	}
	if p.Cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(p.Cursor)
	if err != nil || n <= 0 || strconv.Itoa(n) != p.Cursor {
		return 0, NewError(CodeInvalidParams, "invalid blocked cursor")
	}
	return n, nil
}
