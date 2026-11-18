package rpc

import "encoding/json"

const MethodMessageTranscribe = "message.transcribe"

// TranscribeParams identifies a message and the maximum wait in milliseconds.
type TranscribeParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	WaitMS int64  `json:"wait_ms"`
}

// Transcription is a Telegram transcription or an update to it.
type Transcription struct {
	ChatID          int64  `json:"chat_id"`
	ID              int    `json:"id"`
	TranscriptionID int64  `json:"transcription_id"`
	Text            string `json:"text" untrusted:"true"`
	Pending         bool   `json:"pending"`
	Cached          bool   `json:"cached"`
	TrialRemains    *int   `json:"trial_remains,omitempty"`
	TrialResetsAt   string `json:"trial_resets_at,omitempty"`
	// Raw carries the Telegram object as received; never encoded in responses.
	Raw json.RawMessage `json:"-"`
}
