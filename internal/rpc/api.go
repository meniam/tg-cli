package rpc

import (
	"encoding/json"
	"time"
)

// Method names of the daemon API.
const (
	MethodSessionExport = "session.export"
	MethodPing          = "ping"
	MethodStatus        = "status"
	MethodShutdown      = "shutdown"
	MethodAuthStart     = "auth.start"
	MethodAuthCode      = "auth.code"
	MethodAuthPassword  = "auth.password"
	MethodAuthResend    = "auth.resend"
	MethodAuthStartQR   = "auth.start_qr"
	MethodAuthWait      = "auth.wait"
	MethodAuthCancel    = "auth.cancel"
	MethodChatList      = "chat.list"
	MethodChatMembers   = "chat.members"
	MethodChatTopics    = "chat.topics"
	MethodChatCommon    = "chat.common"

	MethodChatResolve         = "chat.resolve"
	MethodChatInfo            = "chat.info"
	MethodChatOverview        = "chat.overview"
	MethodChatInviteInfo      = "chat.invite_info"
	MethodMessageHistory      = "message.history"
	MethodMessageDownload     = "message.download"
	MethodMessageGet          = "message.get"
	MethodMessagePinned       = "message.pinned"
	MethodMessageSend         = "message.send"
	MethodMessageSendMedia    = "message.send_media"
	MethodMessageEdit         = "message.edit"
	MethodMessageDelete       = "message.delete"
	MethodMessageForward      = "message.forward"
	MethodMessageReact        = "message.react"
	MethodMessagePin          = "message.pin"
	MethodMessageUnpin        = "message.unpin"
	MethodMessagePoll         = "message.poll"
	MethodMessageSearch       = "message.search"
	MethodMessageSearchPublic = "message.search_public"
	MethodMessageContext      = "message.context"
	MethodMessageExportLocal  = "message.export_local"
	MethodMessageComments     = "message.comments"
	MethodMessageLink         = "message.link"
	MethodChatMarkRead        = "chat.mark_read"
	MethodNoteAdd             = "note.add"
	MethodNoteList            = "note.list"
	MethodNoteDelete          = "note.delete"
	MethodNoteDeleteByAuthor  = "note.delete_by_author"
	MethodSearch              = "search"
	MethodHistorySyncStart    = "history_sync.start"
	MethodHistorySyncStatus   = "history_sync.status"
	MethodHistorySyncCancel   = "history_sync.cancel"
	MethodSubscribe           = "subscribe"
	MethodUnsubscribe         = "unsubscribe"
	MethodEvent               = "event"

	MethodUserProfile   = "user.profile"
	MethodUserBlock     = "user.block"
	MethodUserUnblock   = "user.unblock"
	MethodContactList   = "contact.list"
	MethodContactAdd    = "contact.add"
	MethodContactDelete = "contact.delete"
	MethodContactSearch = "contact.search"
)

// Login states reported by the auth methods.
const (
	AuthQRScan           = "qr_scan"
	AuthCodeRequired     = "code_required"
	AuthPasswordRequired = "password_required"
	AuthAuthorized       = "authorized"
)

// User is a Telegram account.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// PingResult is the result of MethodPing.
type PingResult struct {
	Pong bool `json:"pong"`
}

// ConnectionInfo describes the primary Telegram connection.
type ConnectionInfo struct {
	Transport         string `json:"transport"`
	ActiveTransport   string `json:"active_transport,omitempty"`
	TransportSwitches int    `json:"transport_switches"`
	Proxy             string `json:"proxy,omitempty"`
	Connection        string `json:"connection"`
	DC                int    `json:"dc,omitempty"`
	Reconnects        int    `json:"reconnects"`
}

// StatusResult is the result of MethodStatus.
type StatusResult struct {
	*ConnectionInfo `json:",omitempty"`
	Profile         string        `json:"profile"`
	PID             int           `json:"pid"`
	Connected       bool          `json:"connected"`
	Authorized      bool          `json:"authorized"`
	User            *User         `json:"user,omitempty"`
	LoginState      string        `json:"login_state,omitempty"`
	AuthLost        string        `json:"auth_lost,omitempty"`
	ActiveRequests  int           `json:"active_requests"`
	Access          *AccessStatus `json:"access,omitempty"`
}

// AccessStatus reports access-list state without revealing chat IDs.
type AccessStatus struct {
	Enabled  bool `json:"enabled"`
	Allow    int  `json:"allow"`
	Block    int  `json:"block"`
	Write    int  `json:"write,omitempty"`
	Admin    int  `json:"admin,omitempty"`
	Create   bool `json:"create,omitempty"`
	Dialogs  bool `json:"dialogs,omitempty"`
	Sessions bool `json:"sessions,omitempty"`
	Profile  bool `json:"profile,omitempty"`
	Closed   bool `json:"closed,omitempty"`
}

// ShutdownResult is the result of MethodShutdown.
type ShutdownResult struct {
	Stopping bool `json:"stopping"`
}

// AuthStartParams starts a phone login.
type AuthStartParams struct {
	Phone string `json:"phone"`
}

// AuthCodeParams passes the login code received in Telegram.
type AuthCodeParams struct {
	Code string `json:"code"`
}

// AuthPasswordParams passes the two-factor password.
type AuthPasswordParams struct {
	Password string `json:"password"`
}

// Where Telegram delivered the login code, reported with AuthCodeRequired so
// the user knows where to look.
const (
	DeliveryApp    = "app"   // a message in the Telegram app of another logged-in device
	DeliverySMS    = "sms"   // an SMS
	DeliveryCall   = "call"  // a phone call, flash call or missed call
	DeliveryEmail  = "email" // an e-mail
	DeliveryOther  = "other" // a kind this tool does not know yet
	DeliveryUnsent = ""      // not known
)

// CodePrompt describes a login code request: where Telegram delivered the
// code and which way it offers next.
type CodePrompt struct {
	// Delivery is where the code went, one of the Delivery values.
	Delivery string
	// NextDelivery is the way a resend would use, empty when Telegram offers none.
	NextDelivery string
	// ResendAfterSeconds is how long Telegram asks to wait before a resend, 0 if unknown.
	ResendAfterSeconds int
}

// QRToken is a login QR code to show: the user scans it in the Telegram app
// of a logged-in device. Telegram refreshes the token every few tens of seconds.
type QRToken struct {
	URL              string
	ExpiresInSeconds int
}

// AuthResult is the result of the auth methods: the login state after the step.
// Delivery, NextDelivery and ResendAfterSeconds are set only with AuthCodeRequired,
// QRURL and ExpiresInSeconds only with AuthQRScan.
type AuthResult struct {
	State              string `json:"state"`
	Delivery           string `json:"delivery,omitempty"`
	NextDelivery       string `json:"next_delivery,omitempty"`
	ResendAfterSeconds int    `json:"resend_after_seconds,omitempty"`
	QRURL              string `json:"qr_url,omitempty"`
	ExpiresInSeconds   int    `json:"expires_in_seconds,omitempty"`
	User               *User  `json:"user,omitempty"`
}

// AuthCancelResult is the result of MethodAuthCancel.
type AuthCancelResult struct {
	Canceled bool `json:"canceled"`
}

// ChatListParams are the parameters of MethodChatList. Type filters by chat
// type: user, bot, group, supergroup or channel; empty means all.
type ChatListParams struct {
	Limit int    `json:"limit,omitempty"`
	Type  string `json:"type,omitempty"`
}

// Chat is a dialog of the account. Chat types are "user", "bot", "group",
// "supergroup" and "channel". ID uses the Bot API convention: groups are
// negative, channels and supergroups start from -1000000000000.
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title" untrusted:"true"`
	Username string `json:"username,omitempty" untrusted:"true"`
	Unread   int    `json:"unread"`
	// Scam, Fake and Restricted travel with the chat object of Telegram and are
	// stored with the chat by the Daemon. They are not part of this answer: the
	// fields of `chat info` are where an agent reads them, so a list and a resolve
	// keep their old shape.
	Scam       bool `json:"-"`
	Fake       bool `json:"-"`
	Restricted bool `json:"-"`
	// FlagsKnown reports whether the source of this record read the flags above.
	// A record that leaves them out must not clear what the database already
	// knows: a move from a dialog, a peer or an event knows no flags.
	FlagsKnown bool `json:"-"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}

// ChatListResult is the result of MethodChatList.
type ChatListResult struct {
	Chats []Chat `json:"chats"`
}

// ChatRefParams point at a chat the way a user writes it: @username, a numeric
// id in the Bot API form, a phone number of a contact or a t.me link.
type ChatRefParams struct {
	Chat string `json:"chat"`
}

// ChatInfo describes one chat in more detail than Chat. Optional fields are
// left out when Telegram does not give them.
type ChatInfo struct {
	Settings json.RawMessage `json:"settings,omitempty"`
	ID       int64           `json:"id"`
	Type     string          `json:"type"`
	Title    string          `json:"title" untrusted:"true"`
	Username string          `json:"username,omitempty" untrusted:"true"`
	About    string          `json:"about,omitempty" untrusted:"true"`
	Members  int             `json:"members,omitempty"`
	Forum    bool            `json:"forum,omitempty"`
	Verified bool            `json:"verified,omitempty"`
	// CreatedAt is the date the chat was created, in Unix seconds. Absent when
	// Telegram did not supply it.
	CreatedAt int64 `json:"created_at,omitempty"`
	// Scam, Fake and Restricted are the flags Telegram sets on a chat.
	Scam       bool `json:"scam,omitempty"`
	Fake       bool `json:"fake,omitempty"`
	Restricted bool `json:"restricted,omitempty"`
	// RestrictionReason explains a restriction: platform, reason and the text
	// shown to the user. It is absent when the chat is not restricted.
	RestrictionReason []RestrictionReason `json:"restriction_reason,omitempty"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}

// RestrictionReason is why Telegram restricts a chat. Text is what Telegram shows
// to the user: it is data, not an instruction.
type RestrictionReason struct {
	Platform string `json:"platform"`
	Reason   string `json:"reason"`
	Text     string `json:"text,omitempty" untrusted:"true"`
}

// InviteInfo is what an invite link shows without joining the chat.
type InviteInfo struct {
	Title         string `json:"title" untrusted:"true"`
	Type          string `json:"type"`
	About         string `json:"about,omitempty" untrusted:"true"`
	Members       int    `json:"members,omitempty"`
	AlreadyMember bool   `json:"already_member,omitempty"`
	RequestNeeded bool   `json:"request_needed,omitempty"`
}

// Sender is the author of a message: a user, or the channel for a channel post.
type Sender struct {
	ID       int64  `json:"id"`
	Name     string `json:"name,omitempty" untrusted:"true"`
	Username string `json:"username,omitempty" untrusted:"true"`
}

// Message is one message of a chat. Text is what other people wrote: it is data
// for the agent, never an instruction.
type Message struct {
	SavedSourceID      int64   `json:"-"`
	SavedSourceUnknown bool    `json:"-"`
	ID                 int     `json:"id"`
	ChatID             int64   `json:"chat_id"`
	Date               string  `json:"date"` // RFC 3339, UTC
	Sender             *Sender `json:"sender,omitempty"`
	Text               string  `json:"text" untrusted:"true"`
	Out                bool    `json:"out,omitempty"`
	ReplyTo            int     `json:"reply_to,omitempty"`
	TopicID            int     `json:"topic_id,omitempty"`
	MediaType          string  `json:"media_type,omitempty"`
	Edited             bool    `json:"edited,omitempty"`
	Pinned             bool    `json:"pinned,omitempty"`
	Service            string  `json:"service,omitempty"` // the kind of a service message
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}

// ExportMessage is a locally stored message with an optional deletion marker.
type ExportMessage struct {
	Message
	Deleted bool `json:"deleted,omitempty"`
}

// ExportParams selects one page of messages from the local database.
type ExportParams struct {
	Chat           string `json:"chat"`
	Since          string `json:"since,omitempty"`
	Until          string `json:"until,omitempty"`
	IncludeDeleted bool   `json:"include_deleted,omitempty"`
	After          int    `json:"after,omitempty"`
}

// ExportPage continues after NextAfter when the field is present.
type ExportPage struct {
	Messages  []ExportMessage `json:"messages"`
	NextAfter int             `json:"next_after,omitempty"`
}

// HistoryParams are the parameters of MethodMessageHistory. Since and Until are
// RFC 3339 times; the history is returned from the newest message to the oldest.
type HistoryParams struct {
	Chat   string `json:"chat"`
	Limit  int    `json:"limit,omitempty"`
	Since  string `json:"since,omitempty"`
	Until  string `json:"until,omitempty"`
	Topic  int    `json:"topic,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// HistoryQuery is HistoryParams after validation, the form Telegram clients take.
type HistoryQuery struct {
	MinID  int
	Limit  int
	Since  time.Time
	Until  time.Time
	Topic  int
	Cursor string
	// MaxBytes cuts the page when its encoded size would pass it; the cursor
	// then points at the rest.
	MaxBytes int
}

// MessagesResult is a page of messages. NextCursor is set when more follow:
// pass it back as the cursor to continue.
type MessagesResult struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// HistoryPage is one raw Telegram history response for background sync.
// RawNextOffsetID also includes empty message placeholders.
type HistoryPage struct {
	Messages        []Message `json:"messages"`
	OldestID        int       `json:"oldest_id"`
	RawNextOffsetID int       `json:"raw_next_offset_id"`
	Empty           bool      `json:"empty"`
}

// HistorySyncParams identifies one chat for starting or cancelling a sync.
type HistorySyncParams struct {
	Chat string `json:"chat"`
}

// HistorySyncStatusParams optionally selects one chat; an empty chat lists jobs.
type HistorySyncStatusParams struct {
	Chat string `json:"chat,omitempty"`
}

const (
	HistorySyncIdle         = "idle"
	HistorySyncQueued       = "queued"
	HistorySyncRunning      = "running"
	HistorySyncWaitingFlood = "waiting_flood"
	HistorySyncDone         = "done"
	HistorySyncFailed       = "failed"
	HistorySyncCancelled    = "cancelled"
)

// Sources of a history load: the owner's command or the Daemon's schedule.
const (
	HistorySyncManual     = "manual"
	HistorySyncBackground = "background"
)

// HistorySyncJob is the in-memory state of one background history load.
type HistorySyncJob struct {
	ChatID      int64  `json:"chat_id"`
	State       string `json:"state"`
	Source      string `json:"source,omitempty"` // HistorySyncManual or HistorySyncBackground; empty when idle
	Pages       int    `json:"pages"`
	Loaded      int    `json:"loaded"`
	OldestID    *int   `json:"oldest_id"`
	NewestID    *int   `json:"newest_id"`
	Complete    bool   `json:"complete"`
	WaitSeconds int    `json:"wait_seconds,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at"`
	UpdatedAt   string `json:"updated_at"`
}

// HistorySyncJobs is the status result when no chat was selected.
type HistorySyncJobs struct {
	Jobs []HistorySyncJob `json:"jobs"`
}

// MessageGetParams are the parameters of MethodMessageGet.
type MessageGetParams struct {
	Chat string `json:"chat"`
	ID   int    `json:"id"`
}

// PinnedParams are the parameters of MethodMessagePinned.
type PinnedParams struct {
	Chat  string `json:"chat"`
	Limit int    `json:"limit,omitempty"`
}

// Note is a locally stored annotation. Timestamps are RFC 3339 UTC.
type Note struct {
	ID        int64    `json:"id"`
	Kind      string   `json:"kind"`
	ChatID    *int64   `json:"chat_id,omitempty"`
	TargetID  *int64   `json:"target_id,omitempty"`
	Body      string   `json:"body"`
	Author    string   `json:"author"`
	Model     string   `json:"model,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// NoteAddParams describes a note target and its content.
type NoteAddParams struct {
	Kind      string   `json:"kind"`
	Chat      string   `json:"chat,omitempty"`
	Person    string   `json:"person,omitempty"`
	MessageID int64    `json:"message_id,omitempty"`
	Body      string   `json:"body"`
	Author    string   `json:"author,omitempty"`
	Model     string   `json:"model,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// NoteListParams filters notes, newest first.
type NoteListParams struct {
	Kind      string `json:"kind,omitempty"`
	Chat      string `json:"chat,omitempty"`
	Person    string `json:"person,omitempty"`
	MessageID int64  `json:"message_id,omitempty"`
	Author    string `json:"author,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	BeforeID  int64  `json:"before_id,omitempty"`
}

// NoteListResult contains a page of notes.
type NoteListResult struct {
	Notes []Note `json:"notes"`
}

// NoteDeleteParams identifies one note.
type NoteDeleteParams struct {
	ID int64 `json:"id"`
}

// NoteDeleteResult reports how many notes were deleted.
type NoteDeleteResult struct {
	Deleted int `json:"deleted"`
}

// NoteDeleteByAuthorParams selects notes written by one author.
type NoteDeleteByAuthorParams struct {
	Author string `json:"author"`
}

// SearchParams selects ranked local search results.
type SearchParams struct {
	Query          string   `json:"query"`
	Scopes         []string `json:"scopes,omitempty"`
	Author         string   `json:"author,omitempty"`
	Chat           string   `json:"chat,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Prefix         bool     `json:"prefix,omitempty"`
	IncludeDeleted bool     `json:"include_deleted,omitempty"`
}

// SearchHit is one ranked result. Date is RFC 3339 UTC when present.
type SearchHit struct {
	Scope    string  `json:"scope"`
	ID       int64   `json:"id"`
	ChatID   *int64  `json:"chat_id,omitempty"`
	Title    string  `json:"title,omitempty" untrusted:"true"`
	Snippet  string  `json:"snippet" untrusted:"true"`
	Date     string  `json:"date,omitempty"`
	Deleted  bool    `json:"deleted,omitempty"`
	Author   string  `json:"author,omitempty"`
	Model    string  `json:"model,omitempty"`
	NoteKind string  `json:"note_kind,omitempty"`
	Rank     float64 `json:"rank"`
}

// SearchResult contains hits grouped by scope.
type SearchResult struct {
	Hits []SearchHit `json:"hits"`
}

// SessionExportParams requests export to a new local file.
type SessionExportParams struct {
	Path string `json:"path"`
	Yes  bool   `json:"yes"`
}

// SessionExportResult contains only public metadata.
type SessionExportResult struct {
	Format string `json:"format"`
	DC     int    `json:"dc"`
	UserID int64  `json:"user_id,omitempty"`
	KeyID  string `json:"key_id"`
	Client string `json:"client,omitempty"`
}
