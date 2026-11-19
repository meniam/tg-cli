package tgclient

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"tg-cli/internal/netconf"
)

type differenceStub struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (*differenceStub) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	return &tg.UpdatesState{Pts: 1, Seq: 1, Date: 1}, nil
}
func (a *differenceStub) UpdatesGetDifference(ctx context.Context, _ *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	if a.calls.Add(1) == 1 {
		return &tg.UpdatesDifferenceEmpty{Date: 1, Seq: 1}, nil
	}
	select {
	case a.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.release:
		return &tg.UpdatesDifferenceEmpty{Date: 1, Seq: 1}, nil
	}
}
func (*differenceStub) UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	panic("unexpected channel difference")
}
func TestCatchupCoalescesUntilManagerFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{connection: newConnection(netconf.Settings{})}
	api := &differenceStub{entered: make(chan struct{}, 5), release: make(chan struct{})}
	c.updates = updates.New(updates.Config{Handler: catchupHandler{client: c, next: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil })}, BeforeRun: updates.BeforeRunBuffer})
	started := make(chan struct{})
	managerDone := make(chan error, 1)
	go func() {
		managerDone <- c.updates.Run(ctx, api, 1, updates.AuthOptions{OnStart: func(context.Context) {
			c.connection.mu.Lock()
			c.connection.started = true
			c.connection.mu.Unlock()
			close(started)
		}})
	}()
	<-started
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); c.runCatchup(ctx) }()
	event := func(state telegram.ConnectionState) {
		c.connection.update(telegram.ConnectionStateEvent{State: state, DC: 2})
	}
	event(telegram.ConnectionStateReady)
	event(telegram.ConnectionStateDisconnected)
	event(telegram.ConnectionStateConnecting)
	event(telegram.ConnectionStateReady)
	select {
	case <-api.entered:
	case <-time.After(audit08Safety):
		t.Fatal("difference did not start")
	}
	for i := 0; i < 3; i++ {
		event(telegram.ConnectionStateDisconnected)
		event(telegram.ConnectionStateConnecting)
		event(telegram.ConnectionStateReady)
	}
	if got := api.calls.Load(); got != 2 {
		t.Fatalf("difference calls while blocked: %d", got)
	}
	// Events are applied synchronously, and catchupPending stays set until the
	// worker finishes, so no extra catch-up marker may be queued behind the
	// running one.
	if n := len(c.connection.catchup); n != 0 {
		t.Fatalf("%d extra catch-up markers queued while difference is blocked", n)
	}
	close(api.release)
	audit08WaitFor(t, "catch-up barrier not processed", func() bool { return !audit08CatchupPending(c) })
	if n := len(c.connection.catchup); n != 0 {
		t.Fatalf("%d catch-up markers left queued after the barrier", n)
	}
	if got := api.calls.Load(); got != 2 {
		t.Fatalf("duplicate difference: %d", got)
	}
	event(telegram.ConnectionStateDisconnected)
	event(telegram.ConnectionStateConnecting)
	event(telegram.ConnectionStateReady)
	select {
	case <-api.entered:
	case <-time.After(audit08Safety):
		t.Fatal("later reconnect did not trigger difference")
	}
	if got := api.calls.Load(); got != 3 {
		t.Fatalf("later difference count: %d", got)
	}
	cancel()
	<-workerDone
	<-managerDone
}

func TestCatchupWaitsForBufferedStartupReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{connection: newConnection(netconf.Settings{})}
	api := &differenceStub{entered: make(chan struct{}, 5), release: make(chan struct{})}
	close(api.release)
	c.updates = updates.New(updates.Config{Handler: catchupHandler{client: c, next: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil })}, BeforeRun: updates.BeforeRunBuffer})
	update := func() tg.UpdatesClass {
		return &tg.UpdateShort{Update: &tg.UpdateUserStatus{UserID: 123, Status: &tg.UserStatusEmpty{}}}
	}
	if err := c.updates.Handle(ctx, update()); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	resume := make(chan struct{})
	managerDone := make(chan error, 1)
	go func() {
		managerDone <- c.updates.Run(ctx, api, 1, updates.AuthOptions{OnStart: func(context.Context) {
			c.connection.mu.Lock()
			c.connection.started = true
			c.connection.mu.Unlock()
			close(started)
			<-resume
		}})
	}()
	<-started
	for i := 0; i < 100; i++ {
		if err := c.updates.Handle(ctx, update()); err != nil {
			t.Fatal(err)
		}
	}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); c.runCatchup(ctx) }()
	for _, state := range []telegram.ConnectionState{telegram.ConnectionStateReady, telegram.ConnectionStateDisconnected, telegram.ConnectionStateConnecting, telegram.ConnectionStateReady} {
		c.connection.update(telegram.ConnectionStateEvent{State: state, DC: 2})
	}
	audit08WaitFor(t, "probe was not queued", func() bool {
		c.connection.mu.Lock()
		defer c.connection.mu.Unlock()
		return len(c.connection.barriers) > 0
	})
	close(resume)
	select {
	case <-api.entered:
	case <-time.After(audit08Safety):
		t.Fatal("catch-up did not recover after startup replay")
	}
	audit08WaitFor(t, "catch-up did not complete", func() bool { return !audit08CatchupPending(c) })
	cancel()
	<-workerDone
	<-managerDone
}
