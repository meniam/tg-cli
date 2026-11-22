package daemon

import (
	"encoding/json"
	"time"

	"tg-cli/internal/config"
	"tg-cli/internal/rpc"
)

// maxHistoryDepthMessages bounds the messages limit of a depth-limited load.
const maxHistoryDepthMessages = 100000

// historyDepth limits how far a history load goes back. The zero value is no
// limit: the load runs to the first message of the chat.
type historyDepth struct {
	// Messages stops the load once the chat holds at least this many saved messages.
	Messages int
	// Since is a Unix time: the load stops once the oldest saved message is not newer.
	Since int64
}

func (d historyDepth) active() bool { return d.Messages > 0 || d.Since > 0 }

// satisfied reports whether count saved messages, the oldest of them dated at
// oldest, already meet the limit.
func (d historyDepth) satisfied(count int, oldest int64) bool {
	switch {
	case d.Messages > 0:
		return count >= d.Messages
	case d.Since > 0:
		return count > 0 && oldest <= d.Since
	}
	return false
}

// sinceText is the RFC 3339 form of Since for a job.
func (d historyDepth) sinceText() string {
	if d.Since <= 0 {
		return ""
	}
	return time.Unix(d.Since, 0).UTC().Format(time.RFC3339)
}

// parseHistoryDepth checks the depth parameters of a start or a queue request.
// Both limits together are an error, and a Since in the future would stop every
// load at once.
func parseHistoryDepth(messages int, since string, now time.Time) (historyDepth, error) {
	if messages < 0 || messages > maxHistoryDepthMessages {
		return historyDepth{}, rpc.NewError(rpc.CodeInvalidParams, "messages must be between 1 and 100000")
	}
	if messages > 0 && since != "" {
		return historyDepth{}, rpc.NewError(rpc.CodeInvalidParams, "use either messages or since, not both")
	}
	depth := historyDepth{Messages: messages}
	if since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return historyDepth{}, rpc.NewError(rpc.CodeInvalidParams, "since must be a time in RFC 3339 form")
		}
		if t.After(now) {
			return historyDepth{}, rpc.NewError(rpc.CodeInvalidParams, "since must not be in the future")
		}
		depth.Since = t.Unix()
	}
	return depth, nil
}

// historyLimit is the pair of settings that ends a full history load: the load
// of a chat stops, and the chat counts as completely loaded, once the oldest
// saved message is not newer than Since or the chat holds Messages saved
// messages. A zero field is off. It does not apply to a depth-limited load.
type historyLimit struct {
	Messages int
	Since    int64
}

// reached reports whether count saved messages, the oldest of them dated at
// oldest, meet either limit.
func (l historyLimit) reached(count int, oldest int64) bool {
	if l.Messages > 0 && count >= l.Messages {
		return true
	}
	return l.Since > 0 && count > 0 && oldest <= l.Since
}

// historyLimit returns the limits in force. A value overridden or stored
// through the settings wins over the default.
func (d *daemon) historyLimit() historyLimit {
	limit := historyLimit{Messages: config.DefaultHistoryLimitMessages}
	if since, err := config.ParseHistoryLimitSince(config.DefaultHistoryLimitSince); err == nil {
		limit.Since = since.Unix()
	}
	if value, ok := d.liveSetting("history_limit.messages"); ok {
		var n int
		if json.Unmarshal(value, &n) == nil {
			limit.Messages = n
		}
	}
	if value, ok := d.liveSetting("history_limit.since"); ok {
		var text string
		if json.Unmarshal(value, &text) == nil {
			if since, err := config.ParseHistoryLimitSince(text); err == nil {
				limit.Since = 0
				if !since.IsZero() {
					limit.Since = since.Unix()
				}
			}
		}
	}
	return limit
}
