// Package exitcode defines stable process exit codes.
//
// An agent decides what happened from the code alone, so existing values never
// change meaning. New codes are only appended.
package exitcode

const (
	// OK means the command succeeded.
	OK = 0
	// Failure is a generic error with no more specific code.
	Failure = 1
	// Usage means wrong arguments, flags or environment.
	Usage = 2
	// DaemonNotRunning means the daemon socket does not answer.
	DaemonNotRunning = 3
	// NotAuthorized means the profile has no logged-in Telegram account.
	NotAuthorized = 4
	// Telegram means Telegram rejected the request.
	Telegram = 5
	// FloodWait means Telegram asked to wait before the next request.
	FloodWait = 6
	// NotFound means the chat, user or message does not exist or is not visible.
	NotFound = 7
)

var names = map[int]string{
	OK:               "ok",
	Failure:          "failure",
	Usage:            "usage",
	DaemonNotRunning: "daemon_not_running",
	NotAuthorized:    "not_authorized",
	Telegram:         "telegram_error",
	FloodWait:        "flood_wait",
	NotFound:         "not_found",
}

// Name returns the stable machine name of a code, or "unknown".
func Name(code int) string {
	if n, ok := names[code]; ok {
		return n
	}
	return "unknown"
}

// All returns every defined code.
func All() []int {
	codes := make([]int, 0, len(names))
	for c := OK; c <= NotFound; c++ {
		if _, ok := names[c]; ok {
			codes = append(codes, c)
		}
	}
	return codes
}
