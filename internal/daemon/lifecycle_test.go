package daemon

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type lifecycleTelegram struct {
	*fakeTelegram
	entered         chan string
	release         chan struct{}
	historyCanceled chan struct{}
	pageStarted     chan struct{}
	pageRelease     chan struct{}
	pageCanceled    chan struct{}
	muCalls         sync.Mutex
	sends           int
}

func (f *lifecycleTelegram) Send(ctx context.Context, _ chatref.Ref, p rpc.SendParams) (rpc.SendResult, error) {
	f.muCalls.Lock()
	f.sends++
	f.muCalls.Unlock()
	f.entered <- p.IdempotencyKey
	if p.IdempotencyKey == "held" {
		select {
		case <-f.release:
		case <-ctx.Done():
			return rpc.SendResult{}, ctx.Err()
		}
	}
	return rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z"}, nil
}
func (f *lifecycleTelegram) History(ctx context.Context, _ chatref.Ref, _ rpc.HistoryQuery) (rpc.MessagesResult, error) {
	f.entered <- "history"
	<-ctx.Done()
	close(f.historyCanceled)
	return rpc.MessagesResult{}, ctx.Err()
}
func (f *lifecycleTelegram) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *lifecycleTelegram) HistoryPage(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
	close(f.pageStarted)
	select {
	case <-f.pageRelease:
		return rpc.HistoryPage{}, nil
	case <-ctx.Done():
		close(f.pageCanceled)
		return rpc.HistoryPage{}, ctx.Err()
	}
}
func (f *lifecycleTelegram) SendMedia(ctx context.Context, _ chatref.Ref, p rpc.SendMediaParams, _ []rpc.MediaFile) (rpc.SendMediaResult, error) {
	f.entered <- p.IdempotencyKey
	select {
	case <-f.release:
		return rpc.SendMediaResult{Messages: []rpc.MessageRef{{ID: 12, Date: "2026-11-14T10:00:00Z"}}}, nil
	case <-ctx.Done():
		return rpc.SendMediaResult{}, ctx.Err()
	}
}
func lifecyclePipe(t *testing.T, d *daemon) *rpc.Client {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() { d.srv.ServeConn(b); close(done) }()
	c := rpc.NewClient(a)
	t.Cleanup(func() { _ = c.Close(); <-done })
	return c
}
func waitLifecycle(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach Telegram")
		return ""
	}
}
func TestLifecycleKeyedRequests(t *testing.T) {
	d, base, _, accessPath := accessTestDaemon(t)
	writeAccess(t, accessPath, `{"write":[42]}`)
	f := &lifecycleTelegram{fakeTelegram: base, entered: make(chan string, 4), release: make(chan struct{}), historyCanceled: make(chan struct{})}
	d.opts.Telegram = f
	d.register()
	a, b, c, replayClient := lifecyclePipe(t, d), lifecyclePipe(t, d), lifecyclePipe(t, d), lifecyclePipe(t, d)
	p := rpc.SendParams{Chat: "42", Text: "hello", IdempotencyKey: "held"}
	first := make(chan error, 1)
	go func() { first <- a.Call(context.Background(), rpc.MethodMessageSend, p, nil) }()
	if got := waitLifecycle(t, f.entered); got != "held" {
		t.Fatal(got)
	}
	other := p
	other.IdempotencyKey = "other"
	var otherResult rpc.SendResult
	if err := rpcCall(t, b, rpc.MethodMessageSend, other, &otherResult); err != nil {
		t.Fatal(err)
	}
	if got := waitLifecycle(t, f.entered); got != "other" {
		t.Fatal(got)
	}
	canceled := make(chan error, 1)
	go func() { canceled <- c.Call(context.Background(), rpc.MethodMessageSend, p, nil) }()
	type result struct {
		value rpc.SendResult
		err   error
	}
	replayed := make(chan result, 1)
	go func() {
		var value rpc.SendResult
		err := replayClient.Call(context.Background(), rpc.MethodMessageSend, p, &value)
		replayed <- result{value, err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.idempotencyLocks.mu.Lock()
		e := d.idempotencyLocks.entries[idempotencyKey{42, "held"}]
		refs := 0
		if e != nil {
			refs = e.refs
		}
		d.idempotencyLocks.mu.Unlock()
		if refs == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("same-key waiter not registered")
		}
		time.Sleep(time.Millisecond)
	}
	_ = c.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("client did not close")
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		d.idempotencyLocks.mu.Lock()
		e := d.idempotencyLocks.entries[idempotencyKey{42, "held"}]
		refs := 0
		if e != nil {
			refs = e.refs
		}
		d.idempotencyLocks.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled waiter retained its key")
		}
		time.Sleep(time.Millisecond)
	}
	close(f.release)
	if err := audit05Recv(t, first, "first send result"); err != nil {
		t.Fatal(err)
	}
	replay := audit05Recv(t, replayed, "replayed send")
	if replay.err != nil || !replay.value.Replayed {
		t.Fatalf("replay=%+v err=%v", replay.value, replay.err)
	}
	f.muCalls.Lock()
	calls := f.sends
	f.muCalls.Unlock()
	if calls != 2 {
		t.Fatalf("Telegram sends=%d", calls)
	}
}
func TestLifecycleHistoryCancelKeepsTelegram(t *testing.T) {
	d, base, _, _ := accessTestDaemon(t)
	f := &lifecycleTelegram{fakeTelegram: base, entered: make(chan string, 1), release: make(chan struct{}), historyCanceled: make(chan struct{}), pageStarted: make(chan struct{}), pageRelease: make(chan struct{}), pageCanceled: make(chan struct{})}
	d.opts.Telegram = f
	d.login = newLoginManager(context.Background(), f)
	d.register()
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.sync = newHistorySyncManager(runCtx, d, nil)
	defer d.sync.close()
	d.srv.SetEventSource(d.hub, func(context.Context, rpc.SubscribeParams) (rpc.ReplayPlan, error) { return rpc.ReplayPlan{}, nil })
	runDone := make(chan error, 1)
	go func() { runDone <- f.Run(runCtx) }()
	a, b, stream := lifecyclePipe(t, d), lifecyclePipe(t, d), lifecyclePipe(t, d)
	started, event := make(chan struct{}), make(chan rpc.Event, 1)
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	go func() {
		_, _ = stream.SubscribeWithStart(streamCtx, rpc.SubscribeParams{}, func(rpc.SubscribeResult) { close(started) }, func(e rpc.Event) error { event <- e; return nil })
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription not started")
	}
	_, err := d.sync.start(42, chatref.Ref{Kind: chatref.ID, ID: 42}, store.SyncProgress{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.pageStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("sync not started")
	}
	done := make(chan error, 1)
	go func() {
		done <- a.Call(context.Background(), rpc.MethodMessageHistory, rpc.HistoryParams{Chat: "42"}, nil)
	}()
	if got := waitLifecycle(t, f.entered); got != "history" {
		t.Fatal(got)
	}
	_ = a.Close()
	select {
	case <-f.historyCanceled:
	case <-time.After(time.Second):
		t.Fatal("history not canceled")
	}
	select {
	case <-runDone:
		t.Fatal("Telegram lifetime canceled")
	default:
	}
	select {
	case <-f.pageCanceled:
		t.Fatal("sync canceled")
	default:
	}
	d.hub.Publish(rpc.Event{ID: "1.0.0.0", Type: rpc.EventMessageNew, Time: "2026-11-14T00:00:00Z"})
	select {
	case <-event:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription stopped")
	}
	close(f.pageRelease)
	var status rpc.StatusResult
	if err := rpcCall(t, b, rpc.MethodStatus, nil, &status); err != nil || !status.Connected || status.ActiveRequests != 0 {
		t.Fatalf("status %+v err=%v", status, err)
	}
	var chats rpc.ChatListResult
	if err := rpcCall(t, b, rpc.MethodChatList, rpc.ChatListParams{}, &chats); err != nil {
		t.Fatal(err)
	}
	audit05Recv(t, done, "history call")
}

func TestLifecycleMediaDoesNotBlockOtherKey(t *testing.T) {
	c := newMediaCase(t)
	f := &lifecycleTelegram{fakeTelegram: c.tg.fakeTelegram, entered: make(chan string, 2), release: make(chan struct{}), historyCanceled: make(chan struct{})}
	c.d.opts.Telegram = f
	c.d.register()
	a, b := lifecyclePipe(t, c.d), lifecyclePipe(t, c.d)
	c.p.IdempotencyKey = "media"
	done := make(chan error, 1)
	go func() { done <- a.Call(context.Background(), rpc.MethodMessageSendMedia, c.p, nil) }()
	if got := waitLifecycle(t, f.entered); got != "media" {
		t.Fatal(got)
	}
	var sent rpc.SendResult
	err := rpcCall(t, b, rpc.MethodMessageSend, rpc.SendParams{Chat: "42", Text: "hello", IdempotencyKey: "text"}, &sent)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitLifecycle(t, f.entered); got != "text" {
		t.Fatal(got)
	}
	close(f.release)
	if err := audit05Recv(t, done, "media call"); err != nil {
		t.Fatal(err)
	}
}
