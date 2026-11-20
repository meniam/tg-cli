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
