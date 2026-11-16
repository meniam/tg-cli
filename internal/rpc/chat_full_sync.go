package rpc

// The full-data job reads the description, the member count and the settings of
// every allowed group and channel, one chat per Telegram request. It only reads,
// and it runs behind the shared account throttle as background traffic.

const (
	// MethodChatFullStart queues the job. An empty chat means every allowed group
	// and channel without a complete read yet.
	MethodChatFullStart = "chat_full.start"
	// MethodChatFullStatus reports the job of this daemon run.
	MethodChatFullStatus = "chat_full.status"
	// MethodChatFullCancel stops the job after the chat it is reading.
	MethodChatFullCancel = "chat_full.cancel"
)

// ChatFullStartParams are the parameters of MethodChatFullStart. Refresh reads
// every allowed group and channel again, not only the ones without a full read.
type ChatFullStartParams struct {
	Chat    string `json:"chat,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
}

// ChatFullStatusParams are the parameters of MethodChatFullStatus. They carry no
// field today: the job is one per daemon run. The type exists so that a new filter
// does not change the shape of the call.
type ChatFullStatusParams struct{}

// ChatFullCancelParams are the parameters of MethodChatFullCancel. They carry no
// field: the job is one per daemon run.
type ChatFullCancelParams struct{}

// The states of the full-data job.
const (
	ChatFullIdle         = "idle"
	ChatFullQueued       = "queued"
	ChatFullRunning      = "running"
	ChatFullWaitingFlood = "waiting_flood"
	ChatFullDone         = "done"
	ChatFullFailed       = "failed"
	ChatFullCancelled    = "cancelled"
)

// ChatFullJob is the in-memory state of the full-data job. Progress counts chats,
// not requests. StartedAt and UpdatedAt are RFC 3339 UTC.
type ChatFullJob struct {
	State         string `json:"state"`
	Total         int    `json:"total"`
	Done          int    `json:"done"`
	Failed        int    `json:"failed"`
	CurrentChatID *int64 `json:"current_chat_id,omitempty"`
	WaitSeconds   int    `json:"wait_seconds,omitempty"`
	Error         string `json:"error,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}
