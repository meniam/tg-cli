package rpc

import "encoding/json"

const MethodMessageTranscribe = "message.transcribe"

// Limits of the wait of message.transcribe, in milliseconds: the local queue
// waits up to MaxTranscribeWaitMS, a direct Telegram request up to
// MaxTelegramTranscribeWaitMS.
const (
	MaxTranscribeWaitMS         = 600_000
	MaxTelegramTranscribeWaitMS = 90_000
)

// Sources of a transcription text.
const (
	TranscriptionSourceTelegram = "telegram"
	TranscriptionSourceLocal    = "local"
)

// TranscribeParams identifies a message and the maximum wait in milliseconds.
// Telegram asks Telegram itself (Premium) instead of the local queue.
type TranscribeParams struct {
	Chat     string `json:"chat"`
	ID       int    `json:"id"`
	WaitMS   int64  `json:"wait_ms"`
	Telegram bool   `json:"telegram,omitempty"`
}

// Transcription is a transcription or an update to it. Source is "local" for
// the local engine and "telegram" for Telegram's; Engine names the local
// engine. Queued is true when the answer came from the local queue (the job
// was queued or running, or has just finished); Cached is true when a stored
// text was returned without any work.
type Transcription struct {
	ChatID          int64  `json:"chat_id"`
	ID              int    `json:"id"`
	TranscriptionID int64  `json:"transcription_id"`
	Text            string `json:"text" untrusted:"true"`
	Pending         bool   `json:"pending"`
	Cached          bool   `json:"cached"`
	Source          string `json:"source,omitempty"`
	Engine          string `json:"engine,omitempty"`
	Queued          bool   `json:"queued,omitempty"`
	TrialRemains    *int   `json:"trial_remains,omitempty"`
	TrialResetsAt   string `json:"trial_resets_at,omitempty"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}

// MessageTranscription is the stored text of a voice message, video note or
// video, shown beside the message by the commands that read the local database.
// Text is untrusted data like the message text, and it is empty for a recording
// the engine found no speech in. Source is "local" for the local engine and
// "telegram" for Telegram's; Engine names the local engine.
type MessageTranscription struct {
	Text   string `json:"text" untrusted:"true"`
	Source string `json:"source"`
	Engine string `json:"engine,omitempty"`
}

// ContextMessage is a message of MethodMessageContext. Transcription is set only
// for a local context (Local): the finished text the database holds for the
// message, absent when it has none.
type ContextMessage struct {
	Message
	Transcription *MessageTranscription `json:"transcription,omitempty"`
}

// ContextResult is the answer of MethodMessageContext. The Daemon answers a live
// context with a MessagesResult of the same shape, without transcriptions.
type ContextResult struct {
	Messages   []ContextMessage `json:"messages"`
	NextCursor string           `json:"next_cursor,omitempty"`
}
