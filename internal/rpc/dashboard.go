package rpc

// MethodDashboard returns everything `tg status` prints in one answer. It only
// reads the daemon state and the local database, it makes no Telegram request,
// and, like MethodStatus, it works after the login is lost. No chat title or text
// is in the answer, only chat ids, so it carries no untrusted Telegram text.
const MethodDashboard = "dashboard"

// DashboardParams are the parameters of MethodDashboard. They carry no field
// today; the type keeps the shape of the call when a section filter appears.
type DashboardParams struct{}

// Dashboard is the result of MethodDashboard.
type Dashboard struct {
	Profile string `json:"profile"`
	PID     int    `json:"pid"`
	// StartedAt is the Unix time in seconds the daemon process started.
	StartedAt  int64               `json:"started_at"`
	Account    DashboardAccount    `json:"account"`
	Connection DashboardConnection `json:"connection"`
	// Access has the same counters as the access block of the daemon status; it
	// is always present, with every counter zero when no list is set.
	Access     AccessStatus         `json:"access"`
	Database   DashboardDatabase    `json:"database"`
	History    DashboardHistory     `json:"history"`
	Background BackgroundSyncStatus `json:"background"`
	// SemanticSearch is the state of the semantic indexer, the same value as the
	// semantic_search block of the daemon status; it is always present.
	SemanticSearch SemanticIndexStatus `json:"semantic_search"`
	Jobs           DashboardJobs       `json:"jobs"`
	Throttle       DashboardThrottle   `json:"throttle"`
	Events         DashboardEvents     `json:"events"`
}

// DashboardAccount is the account the daemon serves. User is null and
// Authorized false when nobody is logged in or the login was lost.
type DashboardAccount struct {
	User       *User `json:"user"`
	Authorized bool  `json:"authorized"`
}

// DashboardConnection is the primary Telegram connection; every field is empty
// or zero when the client is not available.
type DashboardConnection struct {
	Connection      string `json:"connection"`
	Transport       string `json:"transport"`
	ActiveTransport string `json:"active_transport,omitempty"`
	DC              int    `json:"dc,omitempty"`
	Reconnects      int    `json:"reconnects"`
}

// DashboardDatabase describes the profile database. Chats and Messages count
// what the access lists show.
type DashboardDatabase struct {
	// SizeBytes is the size of the store.db file, without the write-ahead log.
	SizeBytes     int64 `json:"size_bytes"`
	SchemaVersion int   `json:"schema_version"`
	Chats         int64 `json:"chats"`
	Messages      int64 `json:"messages"`
}

// DashboardGroup counts the chats of one group and those loaded to the first message.
type DashboardGroup struct {
	Complete int `json:"complete"`
	Total    int `json:"total"`
}

// DashboardHistory is the history completeness. The three groups are the ones
// the scheduled load picks from, and Complete and Total are their sums.
type DashboardHistory struct {
	Complete int            `json:"complete"`
	Total    int            `json:"total"`
	People   DashboardGroup `json:"people"`
	Folders  DashboardGroup `json:"folders"`
	Others   DashboardGroup `json:"others"`
}

// DashboardSyncJob is one active `chat sync` job.
type DashboardSyncJob struct {
	ChatID int64  `json:"chat_id"`
	State  string `json:"state"`
	// Source is HistorySyncManual or HistorySyncBackground.
	Source string `json:"source"`
	Pages  int    `json:"pages"`
	Loaded int    `json:"loaded"`
}

// DashboardFullJob is the state of `chat full`. Done and Total count chats and
// are zero when no job ran in this daemon run.
type DashboardFullJob struct {
	State string `json:"state"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// DashboardJobs are the jobs of the daemon. Sync lists the queued, running and
// waiting jobs, ordered by chat id; it is empty, not null, when none is active.
type DashboardJobs struct {
	Sync []DashboardSyncJob `json:"sync"`
	Full DashboardFullJob   `json:"full"`
}

// DashboardThrottle is the state of the account throttle.
type DashboardThrottle struct {
	// FloodWaitSeconds is the rest of the account-wide FLOOD_WAIT pause, rounded
	// up; zero when there is no pause.
	FloodWaitSeconds int `json:"flood_wait_seconds"`
	// Queued is the number of requests that wait in the throttle queue.
	Queued int `json:"queued"`
}

// DashboardEvents counts the stored events of the stream.
type DashboardEvents struct {
	LastHour int64 `json:"last_hour"`
	Total    int64 `json:"total"`
}
