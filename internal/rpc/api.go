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

	MethodChatResolve          = "chat.resolve"
	MethodChatInfo             = "chat.info"
	MethodChatOverview         = "chat.overview"
	MethodChatInviteInfo       = "chat.invite_info"
	MethodMessageHistory       = "message.history"
	MethodMessageDownload      = "message.download"
	MethodMessageGet           = "message.get"
	MethodMessagePinned        = "message.pinned"
	MethodMessageSend          = "message.send"
	MethodMessageSendMedia     = "message.send_media"
	MethodMessageEdit          = "message.edit"
	MethodMessageDelete        = "message.delete"
	MethodMessageForward       = "message.forward"
	MethodMessageReact         = "message.react"
	MethodMessagePin           = "message.pin"
	MethodMessageUnpin         = "message.unpin"
	MethodMessagePoll          = "message.poll"
	MethodMessageSearch        = "message.search"
	MethodMessageSearchPublic  = "message.search_public"
	MethodMessageContext       = "message.context"
	MethodMessageExportLocal   = "message.export_local"
	MethodMessageComments      = "message.comments"
	MethodMessageLink          = "message.link"
	MethodChatMarkRead         = "chat.mark_read"
	MethodNoteAdd              = "note.add"
	MethodNoteList             = "note.list"
	MethodNoteDelete           = "note.delete"
	MethodNoteDeleteByAuthor   = "note.delete_by_author"
	MethodSearch               = "search"
	MethodSearchSemantic       = "search.semantic"
	MethodHistorySyncStart     = "history_sync.start"
	MethodHistorySyncStatus    = "history_sync.status"
	MethodHistorySyncCancel    = "history_sync.cancel"
	MethodHistorySyncQueue     = "history_sync.queue"
	MethodHistorySyncCancelAll = "history_sync.cancel_all"
	MethodSubscribe            = "subscribe"
	MethodUnsubscribe          = "unsubscribe"
	MethodEvent                = "event"

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
	// BackgroundSync is always present, also when the scheduler is off.
	BackgroundSync BackgroundSyncStatus `json:"background_sync"`
	// SemanticSearch is always present; state is "disabled" while the feature is off.
	SemanticSearch SemanticIndexStatus `json:"semantic_search"`
	// Voice is the local transcription queue; it is always present, with state
	// "disabled" while [voice_transcription] enabled is off and no manual job ran.
	// It holds machine words and counters, never message text or a path.
	Voice VoiceStatus `json:"voice"`
}

// States of the scheduled history load in BackgroundSyncStatus.State.
const (
	BackgroundSyncDisabled        = "disabled"         // the scheduler is off
	BackgroundSyncWaitingWindow   = "waiting_window"   // outside the daily window
	BackgroundSyncWaitingInterval = "waiting_interval" // inside the window, waiting for the next run
	BackgroundSyncRunning         = "running"          // a chat is being loaded
)

// Results of the last run of the scheduled history load in
// BackgroundSyncStatus.LastResult.
const (
	BackgroundSyncDone      = "done"      // the chat was loaded to its first message
	BackgroundSyncCancelled = "cancelled" // the window ended or a manual load took over
	BackgroundSyncFailed    = "failed"    // the load ended with an error
	BackgroundSyncSkipped   = "skipped"   // nothing was loaded: no chat, no login or a busy manager
)

// BackgroundSyncStatus is the state of the scheduled history load.
type BackgroundSyncStatus struct {
	Enabled bool                 `json:"enabled"`
	Window  BackgroundSyncWindow `json:"window"`
	// State is one of the BackgroundSync* state constants.
	State string `json:"state"`
	// NextRunAt is a Unix time in seconds; null when disabled or running.
	NextRunAt *int64 `json:"next_run_at"`
	// LastRunAt is the Unix time in seconds the last run ended; null before the first run.
	LastRunAt *int64 `json:"last_run_at"`
	// LastResult is one of the BackgroundSync* result constants, empty before the first run.
	LastResult string `json:"last_result"`
	// LastChatID is the chat of the last run that picked one; null until a run has picked a chat.
	// A run that ended as skipped without a chat leaves it unchanged.
	LastChatID *int64 `json:"last_chat_id"`
	// CurrentChatID is the chat being loaded; null when nothing is loading.
	CurrentChatID *int64 `json:"current_chat_id"`
}

// BackgroundSyncWindow is the daily window of the scheduled load, local clock "HH:MM".
type BackgroundSyncWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
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
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Type   string `json:"type,omitempty"`
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
	// LastMessageDate is the Unix time of the last message of the dialog, zero for
	// a dialog without one. Pinned dialogs come first in the list whatever their date.
	LastMessageDate int64 `json:"last_message_date,omitempty"`
	Pinned          bool  `json:"-"`
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

// DialogOffset is the internal Telegram position sealed by the daemon. Skip
// counts dialogs without a chronological position after the last offset.
type DialogOffset struct {
	Date, MessageID, PeerID, AccessHash, Skip int64
}

// ChatListResult is the result of MethodChatList.
type ChatListResult struct {
	Chats      []Chat `json:"chats"`
	NextCursor string `json:"next_cursor,omitempty"`
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
	// Transcription is the finished text of a voice message, video note or video
	// the database holds for the message; absent when it has none.
	Transcription *MessageTranscription `json:"transcription,omitempty"`
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
// Messages and Since limit the depth of a start: the load stops at a page
// boundary once the chat holds at least Messages saved messages, or once the
// oldest saved message is not newer than Since (RFC 3339). They exclude each
// other; without them the load goes back to the first message.
type HistorySyncParams struct {
	Chat     string `json:"chat"`
	Messages int    `json:"messages,omitempty"`
	Since    string `json:"since,omitempty"`
}

// HistorySyncQueueParams select a batch of chats for MethodHistorySyncQueue.
type HistorySyncQueueParams struct {
	Type       string `json:"type,omitempty"`
	ActiveDays int    `json:"active_days,omitempty"`
	Unsynced   bool   `json:"unsynced,omitempty"`
	MaxChats   int    `json:"max_chats,omitempty"`
	Messages   int    `json:"messages,omitempty"`
	Since      string `json:"since,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

// Actions of one chat in a HistorySyncQueueResult.
const (
	HistorySyncActionQueued     = "queued"
	HistorySyncActionWouldQueue = "would_queue"
)

// HistorySyncQueueChat is one chat picked by the batch.
type HistorySyncQueueChat struct {
	ChatID          int64  `json:"chat_id"`
	Title           string `json:"title" untrusted:"true"`
	LastMessageDate int64  `json:"last_message_date,omitempty"`
	Action          string `json:"action"`
}

// HistorySyncQueueResult is the result of MethodHistorySyncQueue. Queued is the
// number of jobs put in the queue (zero for a dry run), SkippedLoaded counts the
// chats that match but are loaded enough already, SkippedActive the chats that
// already have a queued or running job.
type HistorySyncQueueResult struct {
	DryRun        bool                   `json:"dry_run"`
	Queued        int                    `json:"queued"`
	WouldQueue    int                    `json:"would_queue"`
	SkippedLoaded int                    `json:"skipped_loaded"`
	SkippedActive int                    `json:"skipped_active"`
	Chats         []HistorySyncQueueChat `json:"chats"`
}

// HistorySyncCancelAllResult is the result of MethodHistorySyncCancelAll.
type HistorySyncCancelAllResult struct {
	Cancelled int `json:"cancelled"`
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
// FirstMessageDate is the Unix time of the first message of the chat, null until
// the load has reached the start of the chat.
type HistorySyncJob struct {
	ChatID           int64  `json:"chat_id"`
	State            string `json:"state"`
	Source           string `json:"source,omitempty"` // HistorySyncManual or HistorySyncBackground; empty when idle
	Pages            int    `json:"pages"`
	Loaded           int    `json:"loaded"`
	OldestID         *int   `json:"oldest_id"`
	NewestID         *int   `json:"newest_id"`
	Complete         bool   `json:"complete"`
	FirstMessageDate *int64 `json:"first_message_date"`
	WaitSeconds      int    `json:"wait_seconds,omitempty"`
	ErrorCode        int    `json:"error_code,omitempty"`
	Error            string `json:"error,omitempty"`
	// Messages and Since echo the depth limit of the start; DepthReached tells that
	// the job stopped on the limit, not at the first message (Complete stays false).
	Messages     int    `json:"messages,omitempty"`
	Since        string `json:"since,omitempty"`
	DepthReached bool   `json:"depth_reached,omitempty"`
	StartedAt    string `json:"started_at"`
	UpdatedAt    string `json:"updated_at"`
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
	// Media keeps only messages with this attachment type: voice, video_note,
	// video, photo, document, gif or audio. It applies to the messages scope
	// only, so a request that names another scope with it is invalid.
	Media string `json:"media,omitempty"`
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
	// Link and LinkUnavailable belong to message hits. Link is the t.me link of
	// the message, built locally; it is null (always present for a message hit)
	// when the chat has none, and LinkUnavailable then holds private_chat or
	// basic_group, or nothing if the chat is unknown.
	Link            *string `json:"link,omitempty"`
	LinkUnavailable string  `json:"link_unavailable,omitempty"`
	// ChatTitle and ChatUsername name the chat of a message hit. ChatTitle is an
	// empty string for a chat the database does not know, ChatUsername is null
	// for a chat without a username; both are always there for messages and
	// absent for the other scopes.
	ChatTitle    string  `json:"chat_title,omitempty" untrusted:"true"`
	ChatUsername *string `json:"chat_username,omitempty" untrusted:"true"`
	// MediaType is the attachment type of a message hit (voice, video_note,
	// video, photo, document, gif, audio, sticker, poll and so on). Absent for a
	// message without an attachment and for the other scopes.
	MediaType string `json:"media_type,omitempty"`
}

// MarshalJSON writes "link": null, "chat_title" and "chat_username": null for
// a message hit that lacks them, so the fields are always there for messages,
// and leaves them out for other scopes.
func (h SearchHit) MarshalJSON() ([]byte, error) {
	type plain SearchHit
	if h.Scope != "messages" {
		return json.Marshal(plain(h))
	}
	return json.Marshal(struct {
		plain
		Link         *string `json:"link"`
		ChatTitle    string  `json:"chat_title"`
		ChatUsername *string `json:"chat_username"`
	}{plain: plain(h), Link: h.Link, ChatTitle: h.ChatTitle, ChatUsername: h.ChatUsername})
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
