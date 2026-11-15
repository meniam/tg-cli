package rpc

import (
	"errors"
	"testing"
)

func TestBatchHelpers(t *testing.T) {
	for _, n := range []int{0, 1, 100, 101, 1000} {
		chunks := Chunks(make([]int, n), 100)
		if len(chunks) != (n+99)/100 {
			t.Fatalf("%d: %d chunks", n, len(chunks))
		}
		for _, c := range chunks {
			if len(c) > 100 {
				t.Fatalf("chunk length %d", len(c))
			}
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
