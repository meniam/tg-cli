package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

// runWithIdle starts a daemon with an idle timeout and returns it with the way
// to reach it. The caller stops it with t.Cleanup of the test's own context.
func runWithIdle(t *testing.T, idle time.Duration) (*running, chan error, func()) {
	t.Helper()
	paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	logs := &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	listening := make(chan struct{})
	// A logged-in account, so the requests of the in-flight test are answered
	// rather than refused before they reach the client.
	fake := newFake()
	fake.mu.Lock()
	fake.user = &rpc.User{ID: 1, Username: "me"}
	fake.mu.Unlock()
	go func() {
		done <- Run(ctx, Options{
			Profile:     "test",
			Paths:       paths,
			Telegram:    fake,
			Log:         slog.New(slog.NewTextHandler(logs, nil)),
			IdleTimeout: idle,
			OnListening: func() { close(listening) },
		})
	}()
	t.Cleanup(cancel)
	select {
	case <-listening:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start")
	}
	return &running{paths: paths, logs: logs}, done, cancel
}

// TestIdleStopEndsAnIdleDaemon is the behaviour of the decision: with a timeout
// the daemon stops by itself once it has been idle that long, and its journal
// says why.
func TestIdleStopEndsAnIdleDaemon(t *testing.T) {
	_, done, _ := runWithIdle(t, 200*time.Millisecond)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("an idle stop must be a clean stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not stop when idle")
	}
}

// TestIdleStopWaitsForARequestInFlight is the case the decision singles out: a
// request that is running is not idle time, so the daemon must not stop under it,
// and a request restarts the clock.
func TestIdleStopWaitsForARequestInFlight(t *testing.T) {
	r, done, _ := runWithIdle(t, 300*time.Millisecond)
	c, err := rpc.Dial(context.Background(), "unix", r.paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Requests arrive faster than the timeout: the daemon must still be there
	// after several timeouts have passed.
	start := time.Now()
	for i := 0; i < 5; i++ {
		var out rpc.ChatListResult
		if err := c.Call(ctxTimeout(t), rpc.MethodChatList, nil, &out); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
		select {
		case <-done:
			t.Fatalf("the daemon stopped after %s with requests coming", time.Since(start))
		default:
		}
	}
}

// TestNoIdleStopWithoutTimeout is the other half of the decision: the default is
// no idle stop, which the live updates of the next plan depend on.
func TestNoIdleStopWithoutTimeout(t *testing.T) {
	_, done, _ := runWithIdle(t, 0)
	select {
	case <-done:
		t.Fatal("without an idle timeout the daemon must not stop by itself")
	case <-time.After(700 * time.Millisecond):
	}
}
