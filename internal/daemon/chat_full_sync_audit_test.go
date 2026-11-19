package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// audit04FullClient is the Telegram client of the daemon for the chat.full
// handlers: the common fake plus the full-data read the job leases it for.
type audit04FullClient struct {
	*fakeTelegram
	full *fakeChatFullTelegram
}

func (c *audit04FullClient) ChatFull(ctx context.Context, chatID int64) (rpc.ChatInfo, error) {
	return c.full.ChatFull(ctx, chatID)
}

func (c *audit04FullClient) FloodWaitRemaining() time.Duration {
	return c.full.FloodWaitRemaining()
}

// audit04FullDaemon builds a daemon with the real manager wiring (newChatFullSyncManager
// with a daemon, not the closures of chatFullFixture) on three groups and channels.
func audit04FullDaemon(t *testing.T) (d *daemon, client *audit04FullClient, clock *fakeChatFullClock, accessPath string) {
	t.Helper()
	d, tg, db, accessPath := accessTestDaemon(t)
	client = &audit04FullClient{fakeTelegram: tg, full: &fakeChatFullTelegram{}}
	d.opts.Telegram = client
	d.admission.unblock(client)
	if err := db.UpsertChats(context.Background(), []store.Chat{
		{ID: 1, Type: "channel", Title: "First"},
		{ID: 2, Type: "supergroup", Title: "Second"},
		{ID: 3, Type: "group", Title: "Third"},
	}); err != nil {
		t.Fatal(err)
	}
	clock = &fakeChatFullClock{}
	d.full = newChatFullSyncManager(context.Background(), d, clock)
	t.Cleanup(d.full.close)
	return d, client, clock, accessPath
}

// audit04WriteAccess replaces access.json and moves its modification time, so the
// loader cannot mistake the new file for the cached one.
func audit04WriteAccess(t *testing.T, path, data string, age time.Duration) {
	t.Helper()
	writeAccess(t, path, data)
	at := time.Now().Add(age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// audit04Call runs a chat.full handler and returns the job it answers with.
func audit04Call(t *testing.T, handler func(context.Context, json.RawMessage) (any, error), raw json.RawMessage) rpc.ChatFullJob {
	t.Helper()
	got, err := handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	job, ok := got.(rpc.ChatFullJob)
	if !ok {
		t.Fatalf("handler result is %T, want rpc.ChatFullJob", got)
	}
	return job
}

func audit04Code(t *testing.T, err error) int {
	t.Helper()
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want an *rpc.Error, got %v", err)
	}
	return rpcErr.Code
}

// TestAudit04_ChatFullHandlersWithoutManager covers the answers of a daemon that
// has no manager: status and cancel report idle, start says the job is unavailable.
func TestAudit04_ChatFullHandlersWithoutManager(t *testing.T) {
	d, _, _, _ := accessTestDaemon(t)
	ctx := context.Background()
	if job := audit04Call(t, d.chatFullStatus, nil); job.State != rpc.ChatFullIdle {
		t.Errorf("status = %+v, want idle", job)
	}
	if job := audit04Call(t, d.chatFullCancel, nil); job.State != rpc.ChatFullIdle {
		t.Errorf("cancel = %+v, want idle", job)
	}
	_, err := d.chatFullStart(ctx, nil)
	if code := audit04Code(t, err); code != rpc.CodeInternal {
		t.Errorf("start code = %d, want %d", code, rpc.CodeInternal)
	}
}

// TestAudit04_ChatFullHandlersRunTheJob covers chat.full.start, chat.full.status
// and chat.full.cancel on a daemon with the real manager: the login check, the
// parameter decoding, the job that reads the stored chats through the leased
// client and a change of access.json between two requests.
func TestAudit04_ChatFullHandlersRunTheJob(t *testing.T) {
	d, client, _, accessPath := audit04FullDaemon(t)
	ctx := context.Background()

	// Not logged in: the job does not start.
	client.fakeTelegram.mu.Lock()
	client.fakeTelegram.user = nil
	client.fakeTelegram.mu.Unlock()
	_, err := d.chatFullStart(ctx, params(t, rpc.ChatFullStartParams{}))
	if code := audit04Code(t, err); code != rpc.CodeNotAuthorized {
		t.Fatalf("start without a login: code %d, want %d", code, rpc.CodeNotAuthorized)
	}
	if got := audit04Call(t, d.chatFullStatus, nil); got.State != rpc.ChatFullIdle {
		t.Fatalf("a refused start left a job: %+v", got)
	}
	client.fakeTelegram.mu.Lock()
	client.fakeTelegram.user = &rpc.User{ID: 42}
	client.fakeTelegram.mu.Unlock()

	// A chat of the wrong type is a decoding error before any work.
	_, err = d.chatFullStart(ctx, []byte(`{"chat":1}`))
	if code := audit04Code(t, err); code != rpc.CodeInvalidParams {
		t.Fatalf("start with a numeric chat: code %d, want %d", code, rpc.CodeInvalidParams)
	}

	// The first job reads the three chats through the leased client.
	started := audit04Call(t, d.chatFullStart, params(t, rpc.ChatFullStartParams{}))
	if started.Total != 3 {
		t.Fatalf("start = %+v, want three chats", started)
	}
	awaitState(t, d.full, rpc.ChatFullDone)
	status := audit04Call(t, d.chatFullStatus, nil)
	if status.State != rpc.ChatFullDone || status.Done != 3 || status.Failed != 0 {
		t.Fatalf("status = %+v", status)
	}
	if got := len(client.full.requests()); got != 3 {
		t.Fatalf("requests = %v, want one per chat", client.full.requests())
	}

	// access.json closes chat 2 between two requests: the refresh skips it, and a
	// start for the closed chat is refused before Telegram is asked.
	audit04WriteAccess(t, accessPath, `{"block":[2]}`, time.Hour)
	again := audit04Call(t, d.chatFullStart, params(t, rpc.ChatFullStartParams{Refresh: true}))
	if again.Total != 2 {
		t.Fatalf("refresh = %+v, want the two open chats", again)
	}
	awaitState(t, d.full, rpc.ChatFullDone)
	counts := map[int64]int{}
	for _, id := range client.full.requests() {
		counts[id]++
	}
	if counts[1] != 2 || counts[3] != 2 || counts[2] != 1 {
		t.Fatalf("requests per chat = %v, want chat 2 read only before it was closed", counts)
	}
	_, err = d.chatFullStart(ctx, params(t, rpc.ChatFullStartParams{Chat: "2"}))
	if code := audit04Code(t, err); code != rpc.ErrUnknownChatID().Code {
		t.Fatalf("start for a closed chat: %v", err)
	}
	if got := len(client.full.requests()); got != 5 {
		t.Errorf("a refused start asked Telegram: %v", client.full.requests())
	}
}

// TestAudit04_ChatFullCancelHandler covers chat.full.cancel on a job that waits in
// its pause: the answer is the cancelled job, and the worker reads nothing more.
func TestAudit04_ChatFullCancelHandler(t *testing.T) {
	d, client, clock, _ := audit04FullDaemon(t)
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	defer closeRelease()
	entered := make(chan struct{}, 1)
	clock.onSleep = func(time.Duration) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}

	audit04Call(t, d.chatFullStart, params(t, rpc.ChatFullStartParams{Refresh: true}))
	// The worker is in the pause after the first chat once the hook reports it.
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never reached its first pause")
	}
	cancelled := audit04Call(t, d.chatFullCancel, nil)
	if cancelled.State != rpc.ChatFullCancelled {
		t.Fatalf("cancel = %+v, want cancelled", cancelled)
	}
	closeRelease()
	d.full.close()
	if got := len(client.full.requests()); got != 1 {
		t.Errorf("requests after the cancel = %v", client.full.requests())
	}
	if status := audit04Call(t, d.chatFullStatus, nil); status.State != rpc.ChatFullCancelled {
		t.Errorf("status after the cancel = %+v", status)
	}
}

// TestAudit04_ChatFullSyncChatClosedDuringTheJob covers a chat that the owner
// closes while the job runs: before its request the chat is dropped from the total
// and never requested, after its request the answer is not saved.
func TestAudit04_ChatFullSyncChatClosedDuringTheJob(t *testing.T) {
	tg := &fakeChatFullTelegram{}
	m, db, _ := chatFullFixture(t, "", tg)
	defer m.close()
	var mu sync.Mutex
	asked := map[int64]int{}
	m.allowed = func(id int64) bool {
		mu.Lock()
		defer mu.Unlock()
		asked[id]++
		switch id {
		case 2:
			return false // closed before the request
		case 3:
			return asked[id] == 1 // open for the request, closed for the answer
		}
		return true
	}
	if _, err := m.start(context.Background(), "", true); err != nil {
		t.Fatalf("start: %v", err)
	}
	final := awaitState(t, m, rpc.ChatFullDone)
	if final.Total != 2 || final.Done != 1 || final.Failed != 0 {
		t.Fatalf("final job = %+v, want total 2 (chat 2 dropped), done 1 (chat 3 not saved)", final)
	}
	requests := tg.requests()
	if len(requests) != 2 || requests[0] != 1 || requests[1] != 3 {
		t.Fatalf("requests = %v, want chats 1 and 3", requests)
	}
	for id, wantSaved := range map[int64]bool{1: true, 2: false, 3: false} {
		chat, err := db.GetChat(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if (chat.FullSyncedAt != nil) != wantSaved {
			t.Errorf("chat %d: saved=%v, want %v", id, chat.FullSyncedAt != nil, wantSaved)
		}
	}
}

// TestAudit04_ChatFullSyncFailures covers the error paths of the worker: no
// Telegram client, a save that fails and a retry after FLOOD_WAIT that fails.
func TestAudit04_ChatFullSyncFailures(t *testing.T) {
	t.Run("no client", func(t *testing.T) {
		tg := &fakeChatFullTelegram{}
		m, db, _ := chatFullFixture(t, "", tg)
		defer m.close()
		m.telegram = func(ctx context.Context) (context.Context, chatFullTelegram, func(), error) {
			return ctx, nil, nil, errors.New("no client")
		}
		if _, err := m.start(context.Background(), "", true); err != nil {
			t.Fatalf("start: %v", err)
		}
		final := awaitState(t, m, rpc.ChatFullDone, rpc.ChatFullFailed)
		if final.Failed != 3 || final.Done != 0 || final.Error != "no client" {
			t.Fatalf("final job = %+v", final)
		}
		if requests := tg.requests(); len(requests) != 0 {
			t.Errorf("requests = %v", requests)
		}
		chat, err := db.GetChat(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if chat.FullError == nil || *chat.FullError != "no client" {
			t.Errorf("the failure is not in the database: %+v", chat)
		}
	})
	t.Run("save fails", func(t *testing.T) {
		tg := &fakeChatFullTelegram{}
		m, _, _ := chatFullFixture(t, "", tg)
		defer m.close()
		m.save = func(context.Context, int64, store.ChatFull) error { return errors.New("disk full") }
		if _, err := m.start(context.Background(), "", true); err != nil {
			t.Fatalf("start: %v", err)
		}
		final := awaitState(t, m, rpc.ChatFullDone, rpc.ChatFullFailed)
		if final.Failed != 3 || final.Done != 0 || final.Error != "disk full" {
			t.Fatalf("final job = %+v, want every chat failed with the save error", final)
		}
	})
	t.Run("retry after flood fails", func(t *testing.T) {
		tg := &fakeChatFullTelegram{
			flooding: map[int64]bool{1: true},
			thenFail: map[int64]string{1: "CHANNEL_PRIVATE"},
		}
		m, db, clock := chatFullFixture(t, "", tg)
		defer m.close()
		if _, err := m.start(context.Background(), "", true); err != nil {
			t.Fatalf("start: %v", err)
		}
		final := awaitState(t, m, rpc.ChatFullDone, rpc.ChatFullFailed)
		if final.Failed != 1 || final.Done != 2 || final.Error != "CHANNEL_PRIVATE" {
			t.Fatalf("final job = %+v", final)
		}
		var waited bool
		for _, sleep := range clock.slept() {
			waited = waited || sleep == 3*time.Second
		}
		if !waited {
			t.Errorf("the job did not wait for the flood delay: %v", clock.slept())
		}
		chat, err := db.GetChat(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if chat.FullError == nil || *chat.FullError != "CHANNEL_PRIVATE" || chat.FullSyncedAt != nil {
			t.Errorf("the chat whose retry failed: %+v", chat)
		}
	})
}

// TestAudit04_ChatFullSyncStartEdges covers a start while the daemon stops and a
// start with nothing to read.
func TestAudit04_ChatFullSyncStartEdges(t *testing.T) {
	t.Run("daemon is stopping", func(t *testing.T) {
		tg := &fakeChatFullTelegram{}
		m, _, _ := chatFullFixture(t, "", tg)
		m.close()
		_, err := m.start(context.Background(), "", true)
		if code := audit04Code(t, err); code != rpc.CodeBusy {
			t.Fatalf("start after close: %v", err)
		}
		if requests := tg.requests(); len(requests) != 0 {
			t.Errorf("requests = %v", requests)
		}
	})
	t.Run("nothing to read", func(t *testing.T) {
		tg := &fakeChatFullTelegram{}
		m, _, clock := chatFullFixture(t, `{"block":[1,2,3]}`, tg)
		defer m.close()
		job, err := m.start(context.Background(), "", true)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if job.State != rpc.ChatFullDone || job.Total != 0 {
			t.Fatalf("job = %+v, want done at once with no chats", job)
		}
		if requests := tg.requests(); len(requests) != 0 || len(clock.slept()) != 0 {
			t.Errorf("a job without chats worked: requests=%v pauses=%v", requests, clock.slept())
		}
	})
}
