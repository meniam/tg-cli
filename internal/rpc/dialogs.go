package rpc

import "regexp"

const MethodAccountTerminate = "account.terminate"

type TerminateParams struct {
	ID     string `json:"id"`
	Yes    bool   `json:"yes,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type TerminateResult struct {
	ID          string `json:"id"`
	Terminated  bool   `json:"terminated,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Action      string `json:"action,omitempty"`
	DeviceModel string `json:"device_model,omitempty" untrusted:"true"`
	Platform    string `json:"platform,omitempty" untrusted:"true"`
	AppName     string `json:"app_name,omitempty" untrusted:"true"`
	DateActive  int    `json:"date_active,omitempty"`
}

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func ValidateTerminateParams(p TerminateParams) error {
	if !sessionIDPattern.MatchString(p.ID) {
		return NewError(CodeInvalidParams, "session id must contain 16 lowercase hexadecimal characters")
	}
	if !p.Yes && !p.DryRun {
		return NewError(CodeInvalidParams, "--yes is required")
	}
	return nil
}

const (
	MethodChatArchive     = "chat.archive"
	MethodChatUnarchive   = "chat.unarchive"
	MethodChatPinDialog   = "chat.pin_dialog"
	MethodChatUnpinDialog = "chat.unpin_dialog"
)

type DialogParams struct {
	Chats  []string `json:"chats"`
	DryRun bool     `json:"dry_run,omitempty"`
}
type DialogItem struct {
	ChatID   int64 `json:"chat_id"`
	Archived bool  `json:"archived"`
}
type DialogArchiveResult struct {
	Chats  []DialogItem `json:"chats"`
	DryRun bool         `json:"dry_run,omitempty"`
}
type DialogPinResult struct {
	ChatID int64 `json:"chat_id"`
	Pinned bool  `json:"pinned"`
	DryRun bool  `json:"dry_run,omitempty"`
}

const (
	MethodChatMute   = "chat.mute"
	MethodChatUnmute = "chat.unmute"
	MethodChatNotify = "chat.notify"
)

type NotifyParams struct {
	Chat      string `json:"chat"`
	MuteUntil *int   `json:"mute_until,omitempty"`
	Silent    *bool  `json:"silent,omitempty"`
	Previews  *bool  `json:"previews,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
}
type NotifyResult struct {
	ChatID       int64 `json:"chat_id"`
	MuteUntil    *int  `json:"mute_until"`
	Silent       *bool `json:"silent"`
	ShowPreviews *bool `json:"show_previews"`
	DryRun       bool  `json:"dry_run,omitempty"`
}

const MethodFolderInviteInfo = "folder.invite_info"

type FolderInviteParams struct {
	Link string `json:"link"`
}
type FolderInviteChat struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title" untrusted:"true"`
	Members int    `json:"members,omitempty"`
	Joined  bool   `json:"joined"`
}
type FolderInviteResult struct {
	Title    string             `json:"title" untrusted:"true"`
	Emoticon string             `json:"emoticon,omitempty"`
	Already  bool               `json:"already"`
	FolderID *int               `json:"folder_id,omitempty"`
	Chats    []FolderInviteChat `json:"chats"`
}

const MethodFolderOrder = "folder.order"

type FolderOrderParams struct {
	IDs    []int `json:"ids"`
	DryRun bool  `json:"dry_run,omitempty"`
}
type FolderOrderResult struct {
	IDs    []int `json:"ids"`
	DryRun bool  `json:"dry_run,omitempty"`
}

const (
	MethodSavedTags      = "saved.tags"
	MethodSavedTagRename = "saved.tag_rename"
)

type SavedTagsParams struct {
	Peer string `json:"peer,omitempty"`
}
type SavedTag struct {
	Emoji         string `json:"emoji,omitempty"`
	CustomEmojiID int64  `json:"custom_emoji_id,omitempty"`
	Title         string `json:"title" untrusted:"true"`
	Count         int    `json:"count"`
}
type SavedTagsResult struct {
	Tags []SavedTag `json:"tags"`
}
type SavedTagRenameParams struct {
	Emoji  string  `json:"emoji"`
	Title  *string `json:"title,omitempty"`
	Clear  bool    `json:"clear,omitempty"`
	DryRun bool    `json:"dry_run,omitempty"`
}
type SavedTagRenameResult struct {
	Emoji   string  `json:"emoji"`
	Title   *string `json:"title,omitempty" untrusted:"true"`
	Cleared bool    `json:"cleared,omitempty"`
	DryRun  bool    `json:"dry_run,omitempty"`
}

const (
	MethodSavedHistory = "saved.history"
	MethodSavedSearch  = "saved.search"
)

type SavedMessagesParams struct {
	Peer   string `json:"peer,omitempty"`
	Query  string `json:"query,omitempty"`
	Tag    string `json:"tag,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

const MethodSavedList = "saved.list"

type SavedListParams struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type SavedDialog struct {
	PeerID     int64  `json:"peer_id"`
	Type       string `json:"type"`
	Title      string `json:"title" untrusted:"true"`
	TopMessage int    `json:"top_message"`
	Date       string `json:"date"`
	Pinned     bool   `json:"pinned"`
	Cursor     string `json:"-"`
}
type SavedListResult struct {
	Dialogs      []SavedDialog `json:"dialogs"`
	NextCursor   string        `json:"next_cursor,omitempty"`
	CursorPeerID int64         `json:"-"`
}

const (
	MethodFolderCreate = "folder.create"
	MethodFolderEdit   = "folder.edit"
	MethodFolderDelete = "folder.delete"
)

type FolderWriteParams struct {
	ID        int      `json:"id,omitempty"`
	Title     *string  `json:"title,omitempty"`
	Emoticon  *string  `json:"emoticon,omitempty"`
	Color     *int     `json:"color,omitempty"`
	Rules     *string  `json:"rules,omitempty"`
	Include   []string `json:"include,omitempty"`
	Add       []string `json:"add,omitempty"`
	Remove    []string `json:"remove,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
	Unexclude []string `json:"unexclude,omitempty"`
	Pin       []string `json:"pin,omitempty"`
	Unpin     []string `json:"unpin,omitempty"`
	Yes       bool     `json:"yes,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
}
type FolderWriteResult struct {
	ID          int  `json:"id"`
	TitleLength int  `json:"title_length"`
	Include     int  `json:"include"`
	Exclude     int  `json:"exclude"`
	Pinned      int  `json:"pinned"`
	Hidden      int  `json:"hidden,omitempty"`
	Deleted     bool `json:"deleted,omitempty"`
	DryRun      bool `json:"dry_run,omitempty"`
}
type FolderDeleteResult struct {
	ID      int  `json:"id"`
	Deleted bool `json:"deleted"`
	DryRun  bool `json:"dry_run,omitempty"`
}
