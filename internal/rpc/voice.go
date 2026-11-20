package rpc

// The voice queue is a background job of the Daemon. A single worker takes the
// queued voice messages, video notes and videos, downloads each file into a
// private temporary directory, converts it with ffmpeg and turns it into text
// with a local speech engine. No method here asks Telegram to transcribe: the
// only Telegram traffic is the download of the file inside the configured
// limits.

const (
	// MethodVoiceStatus reports the state of the queue and of the engine.
	MethodVoiceStatus = "voice.status"
	// MethodVoiceCancel stops the running job and skips the queued ones, or
	// skips one queued job.
	MethodVoiceCancel = "voice.cancel"
	// MethodVoiceRetry queues a finished or failed job of one message again.
	MethodVoiceRetry = "voice.retry"
	// MethodVoiceBackfill queues the saved voice messages and video notes of
	// the personal chats.
	MethodVoiceBackfill = "voice.backfill"
)

// States of the voice worker in VoiceStatus.State.
const (
	// VoiceStateDisabled: [voice_transcription] enabled is off and no manual
	// job has started the worker.
	VoiceStateDisabled = "disabled"
	// VoiceStateIdle: the worker waits for a job.
	VoiceStateIdle = "idle"
	// VoiceStateWorking: a job is in progress.
	VoiceStateWorking = "working"
	// VoiceStatePaused: the queue waits; VoiceStatus.Paused says why.
	VoiceStatePaused = "paused"
)

// Reasons the queue is paused, and the machine words a job ends with.
const (
	// VoicePausedEngineMissing: the speech engine or ffmpeg is not installed.
	VoicePausedEngineMissing = "engine_missing"
	// VoicePausedBudget: the budget of the local day is spent.
	VoicePausedBudget = "budget"
	// VoicePausedOffline: Telegram is not connected or not logged in, so no
	// file can be downloaded; the queue resumes by itself.
	VoicePausedOffline = "offline"
)

// VoiceStatusParams carries no field: the worker is one per daemon run.
type VoiceStatusParams struct{}

// VoiceQueueCounts is the number of jobs in each state.
type VoiceQueueCounts struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// VoiceCurrentJob names the job in progress. It carries no text and no path.
type VoiceCurrentJob struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	StartedAt int64  `json:"started_at"`
}

// VoiceStatus is the state of the voice queue. Every string is a machine word,
// never message text, a file name or a path. Missing lists the parts of the
// engine that are not installed: ffmpeg, runtime, model, vad. Installed is true
// when none is missing. Paused is set while State is "paused". LastFailure is
// the reason of the latest failed or skipped job since the daemon started.
// FilesToday and BytesToday are the downloads of the local day, the budget
// DailyFiles and DailyBytes limit them. PausedUntil and StartedAt are Unix
// seconds.
type VoiceStatus struct {
	Enabled     bool             `json:"enabled"`
	Installed   bool             `json:"installed"`
	Missing     []string         `json:"missing,omitempty"`
	State       string           `json:"state"`
	Paused      string           `json:"paused,omitempty"`
	PausedUntil *int64           `json:"paused_until,omitempty"`
	Queue       VoiceQueueCounts `json:"queue"`
	Current     *VoiceCurrentJob `json:"current,omitempty"`
	DoneToday   int              `json:"done_today"`
	FilesToday  int              `json:"files_today"`
	BytesToday  int64            `json:"bytes_today"`
	DailyFiles  int              `json:"daily_files"`
	DailyBytes  int64            `json:"daily_bytes"`
	LastFailure string           `json:"last_failure,omitempty"`
}

// VoiceCancelParams selects what to cancel. Without Chat and ID everything is
// cancelled: the running job and all queued ones. With both, only that
// message's job is cancelled.
type VoiceCancelParams struct {
	Chat string `json:"chat,omitempty"`
	ID   int    `json:"id,omitempty"`
}

// VoiceCancelResult says how many queued jobs were skipped and whether the
// running job was stopped.
type VoiceCancelResult struct {
	Skipped int  `json:"skipped"`
	Stopped bool `json:"stopped"`
}

// VoiceRetryParams names the message to queue again.
type VoiceRetryParams struct {
	Chat string `json:"chat"`
	ID   int    `json:"id"`
}

// VoiceRetryResult reports the job after the call: Queued is false when the
// job is queued or running already and nothing changed.
type VoiceRetryResult struct {
	ChatID int64  `json:"chat_id"`
	ID     int    `json:"id"`
	Kind   string `json:"kind"`
	Queued bool   `json:"queued"`
	State  string `json:"state"`
}

// The chat marks tell the Daemon which chats it transcribes besides the personal
// ones. They live in the local database; none of these methods calls Telegram.
const (
	// MethodChatVoiceOn marks a chat for transcription of its voice messages and
	// video notes (and of videos too with Video), optionally queueing the ones
	// already saved.
	MethodChatVoiceOn = "chat.voice.on"
	// MethodChatVoiceOff removes the mark of a chat.
	MethodChatVoiceOff = "chat.voice.off"
	// MethodChatVoiceList lists the marked chats.
	MethodChatVoiceList = "chat.voice.list"
)

// ChatVoiceOnParams names the chat to mark. Video adds videos to the mark (a
// second call with another Video value changes it). Backfill queues the voice
// messages and video notes already saved for the chat that have no text yet, and
// the videos too when Video is set.
type ChatVoiceOnParams struct {
	Chat     string `json:"chat"`
	Video    bool   `json:"video,omitempty"`
	Backfill bool   `json:"backfill,omitempty"`
}

// ChatVoiceOnResult reports the mark. Enabled is the state of
// [voice_transcription] enabled: while it is off the mark does nothing yet.
// Candidates and Queued count the backfill (zero without it); EnabledAt is Unix
// seconds of the first mark.
type ChatVoiceOnResult struct {
	ChatID     int64 `json:"chat_id"`
	Video      bool  `json:"video"`
	EnabledAt  int64 `json:"enabled_at"`
	Enabled    bool  `json:"enabled"`
	Candidates int   `json:"candidates"`
	Queued     int   `json:"queued"`
}

// ChatVoiceOffParams names the chat whose mark is removed.
type ChatVoiceOffParams struct {
	Chat string `json:"chat"`
}

// ChatVoiceOffResult says whether the chat had a mark. Jobs already in the queue
// stay.
type ChatVoiceOffResult struct {
	ChatID  int64 `json:"chat_id"`
	Removed bool  `json:"removed"`
}

// ChatVoiceListParams carries no field.
type ChatVoiceListParams struct{}

// ChatVoiceEntry is one marked chat. Title and Username are names from Telegram
// (untrusted data); they are empty when the database does not know the chat.
type ChatVoiceEntry struct {
	ChatID    int64  `json:"chat_id"`
	Title     string `json:"title,omitempty" untrusted:"true"`
	Username  string `json:"username,omitempty" untrusted:"true"`
	Video     bool   `json:"video"`
	EnabledAt int64  `json:"enabled_at"`
}

// ChatVoiceListResult lists the marked chats that access.json leaves open, by
// chat id. Enabled is the state of [voice_transcription] enabled.
type ChatVoiceListResult struct {
	Enabled bool             `json:"enabled"`
	Chats   []ChatVoiceEntry `json:"chats"`
}

// VoiceBackfillParams selects a dry run, which counts and queues nothing.
type VoiceBackfillParams struct {
	DryRun bool `json:"dry_run,omitempty"`
}

// VoiceBackfillResult counts the saved voice messages and video notes of the
// personal chats that have no finished transcription (Candidates) and how many
// of them this call queued (Queued, always 0 for a dry run). Enabled is the
// state of [voice_transcription] enabled.
type VoiceBackfillResult struct {
	Enabled    bool `json:"enabled"`
	DryRun     bool `json:"dry_run"`
	Chats      int  `json:"chats"`
	Candidates int  `json:"candidates"`
	Queued     int  `json:"queued"`
}
