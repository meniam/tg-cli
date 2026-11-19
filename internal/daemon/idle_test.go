package daemon

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

// runWithIdle starts a daemon with an idle timeout and returns it with the
// channel that receives the Run result. tg is the Telegram client; nil means a
// logged-in fake. The test cleanup stops the daemon and waits for Run to return.
func runWithIdle(t *testing.T, idle time.Duration, tg Telegram) (*running, chan error) {
	t.Helper()
	paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	logs := &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	listening := make(chan struct{})
	if tg == nil {
		tg = audit05LoggedInFake()
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- Run(ctx, Options{
			Profile:     "test",
			Paths:       paths,
			Telegram:    tg,
			Log:         slog.New(slog.NewTextHandler(logs, nil)),
			IdleTimeout: idle,
			OnListening: func() { close(listening) },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(audit05Timeout):
			t.Error("daemon did not stop on cleanup")
		}
	})
	select {
	case <-listening:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start")
	}
	return &running{paths: paths, logs: logs}, done
}

// audit05LoggedInFake is a logged-in account, so requests are answered rather
// than refused before they reach the client.
func audit05LoggedInFake() *fakeTelegram {
	fake := newFake()
	fake.mu.Lock()
	fake.user = &rpc.User{ID: 1, Username: "me"}
	fake.mu.Unlock()
	return fake
}

// audit05HoldingTelegram keeps chat.list in flight until released.
type audit05HoldingTelegram struct {
	*fakeTelegram
	entered chan struct{}
	release chan struct{}
}

func (f *audit05HoldingTelegram) Chats(ctx context.Context, limit int, chatType string) ([]rpc.Chat, error) {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.fakeTelegram.Chats(ctx, limit, chatType)
}

// TestIdleStopEndsAnIdleDaemon is the behaviour of the decision: with a timeout
// the daemon stops by itself once it has been idle that long, and its journal
// says why.
func TestIdleStopEndsAnIdleDaemon(t *testing.T) {
	r, done := runWithIdle(t, 200*time.Millisecond, nil)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("an idle stop must be a clean stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not stop when idle")
	}
	if !strings.Contains(r.logs.String(), "daemon idle, stopping") {
		t.Errorf("the journal does not say why the daemon stopped: %s", r.logs.String())
	}
}

// TestIdleStopWaitsForARequestInFlight is the case the decision singles out: a
// request that is running is not idle time, so the daemon must not stop under it.
// The request outlives several timeouts; the daemon stops only after it ends.
func TestIdleStopWaitsForARequestInFlight(t *testing.T) {
	const idle = 100 * time.Millisecond
	tg := &audit05HoldingTelegram{fakeTelegram: audit05LoggedInFake(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	r, done := runWithIdle(t, idle, tg)
	c, err := rpc.Dial(context.Background(), "unix", r.paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	call := make(chan error, 1)
	go func() {
		var out rpc.ChatListResult
		call <- c.Call(context.Background(), rpc.MethodChatList, nil, &out)
	}()
	audit05Recv(t, tg.entered, "the request to reach Telegram")
	// The request is older than several timeouts and several idle checks.
	// The server drains requests on shutdown, so Run may not return while the
	// request is held; the idle decision itself is what the journal records.
	select {
	case err := <-done:
		t.Fatalf("the daemon stopped under a request in flight: %v", err)
	case <-time.After(5 * idle):
	}
	if strings.Contains(r.logs.String(), "daemon idle, stopping") {
		t.Fatalf("the daemon decided to stop under a request in flight: %s", r.logs.String())
	}
	close(tg.release)
	if err := audit05Recv(t, call, "the held request"); err != nil {
		t.Fatalf("held request: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("an idle stop must be a clean stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not stop once the request ended")
	}
}

// TestNoIdleStopWithoutTimeout is the other half of the decision: the default is
// no idle stop, which the live updates of the next plan depend on.
func TestNoIdleStopWithoutTimeout(t *testing.T) {
	_, done := runWithIdle(t, 0, nil)
	select {
	case <-done:
		t.Fatal("without an idle timeout the daemon must not stop by itself")
	case <-time.After(700 * time.Millisecond):
	}
}
