package rpc

import (
	"time"
)

// ScheduleTime validates the requested delivery time against the current clock.
func ScheduleTime(at, in string, now time.Time) (time.Time, error) {
	if at != "" && in != "" {
		return time.Time{}, NewError(CodeInvalidParams, "--at and --in are mutually exclusive")
	}
	if at == "" && in == "" {
		return time.Time{}, nil
	}
	var t time.Time
	var err error
	if in != "" {
		var d time.Duration
		d, err = time.ParseDuration(in)
		if err == nil {
			t = now.Add(d)
		}
	} else {
		t, err = time.Parse(time.RFC3339, at)
	}
	if err != nil {
		return time.Time{}, NewError(CodeInvalidParams, "invalid schedule time")
	}
	if t.Before(now.Add(10 * time.Second)) {
		return time.Time{}, NewError(CodeInvalidParams, "schedule time must be at least 10 seconds in the future")
	}
	if t.Nanosecond() != 0 {
		t = t.Truncate(time.Second).Add(time.Second)
	}
	if t.Unix() > 2147483647 {
		return time.Time{}, NewError(CodeInvalidParams, "schedule time is out of range")
	}
	return t, nil
}

const (
	MethodMessageScheduledList    = "message.scheduled_list"
	MethodMessageScheduledDelete  = "message.scheduled_delete"
	MethodMessageScheduledSendNow = "message.scheduled_send_now"
)

type ScheduledParams struct {
	Chat   string `json:"chat"`
	IDs    []int  `json:"ids,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type ScheduledResult struct {
	Batch
	ChatID   int64     `json:"chat_id"`
	Messages []Message `json:"messages"`
	Deleted  []int     `json:"deleted,omitempty"`
	DryRun   bool      `json:"dry_run,omitempty"`
}
