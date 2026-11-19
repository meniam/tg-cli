package rpc

import (
	"errors"
	"slices"
	"testing"
)

func TestBatchHelpers(t *testing.T) {
	for _, n := range []int{0, 1, 100, 101, 1000} {
		ids := make([]int, n)
		for i := range ids {
			ids[i] = i
		}
		chunks := Chunks(ids, 100)
		if len(chunks) != (n+99)/100 {
			t.Fatalf("%d: %d chunks", n, len(chunks))
		}
		var joined []int
		for i, c := range chunks {
			if len(c) == 0 || len(c) > 100 {
				t.Fatalf("%d: chunk %d has length %d", n, i, len(c))
			}
			joined = append(joined, c...)
		}
		if !slices.Equal(joined, ids) {
			t.Fatalf("%d: the chunks lost, repeated or reordered ids", n)
		}
	}
	for _, size := range []int{0, -1} {
		if chunks := Chunks([]int{1, 2, 3}, size); chunks != nil {
			t.Errorf("size %d: %v, want no chunks", size, chunks)
		}
	}
	e := ItemErrorFrom(7, &Error{Code: CodeFloodWait, Message: "wait", Data: map[string]any{"retry_after_seconds": 13}})
	if e.ID != 7 || e.Code != "flood_wait" || e.RetryAfterSeconds != 13 {
		t.Fatalf("%+v", e)
	}
	e = ItemErrorFrom(3, errors.New("secret"))
	if e.Code != "failure" || e.Message != "internal error" {
		t.Fatalf("%+v", e)
	}
}

// TestAudit07_ChunksDoNotShareCapacity keeps an append to one chunk from
// overwriting the first ids of the next one.
func TestAudit07_ChunksDoNotShareCapacity(t *testing.T) {
	chunks := Chunks([]int{1, 2, 3, 4}, 2)
	chunks[0] = append(chunks[0], 99)
	if !slices.Equal(chunks[1], []int{3, 4}) {
		t.Fatalf("an append to the first chunk changed the second: %v", chunks[1])
	}
}

// TestAudit07_ItemErrorCodes covers every RPC code that has its own public
// name, one that does not, and both numeric types retry_after_seconds can have
// (int in process, float64 after a trip through JSON).
func TestAudit07_ItemErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want string
	}{
		{"invalid params", CodeInvalidParams, "usage"},
		{"method not found", CodeMethodNotFound, "usage"},
		{"not authorized", CodeNotAuthorized, "not_authorized"},
		{"telegram", CodeTelegram, "telegram_error"},
		{"flood wait", CodeFloodWait, "flood_wait"},
		{"not found", CodeNotFound, "not_found"},
		{"cursor expired", CodeCursorExpired, "cursor_expired"},
		{"write disabled", CodeWriteDisabled, "write_disabled"},
		{"internal", CodeInternal, "failure"},
		{"busy", CodeBusy, "failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ItemErrorFrom(5, &Error{Code: tc.code, Message: "text"})
			if got.ID != 5 || got.Code != tc.want || got.Message != "text" || got.RetryAfterSeconds != 0 {
				t.Fatalf("%+v, want code %s", got, tc.want)
			}
		})
	}
	for name, value := range map[string]any{"int": 13, "float64": float64(13)} {
		got := ItemErrorFrom(1, &Error{Code: CodeFloodWait, Message: "wait", Data: map[string]any{"retry_after_seconds": value}})
		if got.RetryAfterSeconds != 13 {
			t.Errorf("%s: retry_after_seconds = %d, want 13", name, got.RetryAfterSeconds)
		}
	}
	got := ItemErrorFrom(1, &Error{Code: CodeTelegram, Message: "rejected password=hunter2"})
	if got.Message != "rejected [redacted]" {
		t.Errorf("the message keeps a secret: %q", got.Message)
	}
	wrapped := ItemErrorFrom(2, errors.Join(errors.New("context"), &Error{Code: CodeNotFound, Message: "gone"}))
	if wrapped.Code != "not_found" {
		t.Errorf("a wrapped *Error is not unwrapped: %+v", wrapped)
	}
}
