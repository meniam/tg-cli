package tgclient

import (
	"testing"
	"time"
)

// audit08Safety bounds every wait in the catch-up tests. The tests finish on
// events; this is only a safety net that stays far above a loaded machine.
const audit08Safety = 10 * time.Second

// audit08WaitFor blocks until cond holds or the safety timeout expires. The
// connection state has no channel that fires when a barrier is processed, so
// the condition is polled on a ticker instead of sleeping in a loop.
func audit08WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(audit08Safety)
	defer deadline.Stop()
	for !cond() {
		select {
		case <-deadline.C:
			t.Fatal(what)
		case <-ticker.C:
		}
	}
}

func audit08CatchupPending(c *Client) bool {
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	return c.connection.catchupPending
}
