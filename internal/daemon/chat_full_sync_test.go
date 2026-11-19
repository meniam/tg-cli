package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// fakeChatFullTelegram answers the full-data job and counts the requests. A chat
// listed in failing answers an error; a chat listed in flooding answers
// FLOOD_WAIT_3 once and succeeds on the retry, unless it is also listed in
// thenFail: then the retry answers that error.
type fakeChatFullTelegram struct {
	mu        sync.Mutex
	requested []int64
	failing   map[int64]string
	flooding  map[int64]bool
	thenFail  map[int64]string
	wait      time.Duration
}

func (f *fakeChatFullTelegram) ChatFull(_ context.Context, chatID int64) (rpc.ChatInfo, error) {
	f.mu.Lock()
	f.requested = append(f.requested, chatID)
	reason, failing := f.failing[chatID]
	flood, flooding := f.flooding[chatID]
	if flooding {
		delete(f.flooding, chatID)
		if reason, ok := f.thenFail[chatID]; ok {
			if f.failing == nil {
				f.failing = map[int64]string{}
			}
			f.failing[chatID] = reason
		}
	}
	f.mu.Unlock()
	if failing {
		return rpc.ChatInfo{}, rpc.NewError(rpc.CodeTelegram, reason)
	}
	if flood {
		f.mu.Lock()
		f.wait = 3 * time.Second
		f.mu.Unlock()
		return rpc.ChatInfo{}, rpc.NewError(rpc.CodeFloodWait, "FLOOD_WAIT: wait before the next request")
	}
	return rpc.ChatInfo{ID: chatID, Type: "channel", Title: "chat", About: "about", Members: 7, Settings: json.RawMessage(`{"slow_mode_seconds":5}`)}, nil
}

func (f *fakeChatFullTelegram) FloodWaitRemaining() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	remaining := f.wait
	f.wait = 0
	return remaining
}

func (f *fakeChatFullTelegram) requests() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.requested...)
}

// fakeChatFullClock records the pauses and never blocks.
type fakeChatFullClock struct {
	mu      sync.Mutex
	now     time.Time
	sleeps  []time.Duration
	onSleep func(time.Duration)
}

func (c *fakeChatFullClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.now.IsZero() {
		c.now = time.Unix(1700000000, 0)
	}
	c.now = c.now.Add(time.Second)
	return c.now
}

func (c *fakeChatFullClock) Sleep(ctx context.Context, d time.Duration) bool {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	hook := c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook(d)
	}
	return ctx.Err() == nil
}

func (c *fakeChatFullClock) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// chatFullFixture builds the job on a real database and the fake Telegram, without
// a Daemon and without a socket. accessJSON is the body of access.json; an empty
// value means no access lists at all.
func chatFullFixture(t *testing.T, accessJSON string, tg *fakeChatFullTelegram) (*chatFullSyncManager, *store.DB, *fakeChatFullClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.UpsertChats(context.Background(), []store.Chat{
		{ID: 1, Type: "channel", Title: "First"},
		{ID: 2, Type: "supergroup", Title: "Second"},
		{ID: 3, Type: "group", Title: "Third"},
		{ID: 4, Type: "user", Title: "Direct"},
	}); err != nil {
		t.Fatal(err)
	}
	policy := access.Policy{}
	path := filepath.Join(t.TempDir(), "access.json")
	if accessJSON != "" {
		writeAccess(t, path, accessJSON)
		loader := access.Open(path)
		if policy, err = loader.Policy(); err != nil {
			t.Fatalf("access policy: %v", err)
		}
	}
	clock := &fakeChatFullClock{}
	m := newChatFullSyncManager(nil, nil, clock)
	m.list = func(ctx context.Context, filter store.ChatFilter, refresh bool) ([]store.FullSyncChat, error) {
		return db.ChatsForFullSync(ctx, filter, refresh)
	}
	m.filter = func() store.ChatFilter {
		hide, allow, block := policy.Filter()
		return store.ChatFilter{Hide: hide, Allow: allow, Block: block}
	}
	m.save = db.SaveChatFull
	m.allowed = policy.Allowed
	m.telegram = func(ctx context.Context) (context.Context, chatFullTelegram, func(), error) {
		return ctx, tg, func() {}, nil
	}
	m.local = func(_ context.Context, value string) (int64, error) {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, rpc.NewError(rpc.CodeInvalidParams, "not a chat reference")
		}
		if _, err := db.GetChat(context.Background(), id); err != nil {
			return 0, rpc.NewError(rpc.CodeNotFound, rpc.ErrChatNotFound().Message)
		}
		return id, nil
	}
	return m, db, clock
}

// awaitState waits until the job reaches a final state.
func awaitState(t *testing.T, m *chatFullSyncManager, want ...string) rpc.ChatFullJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job := m.status()
		for _, state := range want {
			if job.State == state {
				return job
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the job did not reach %v: %+v", want, m.status())
	return rpc.ChatFullJob{}
}

// TestChatFullSyncReadsAllowedChatsOnly covers the job end to end, without a
// socket: two allowed chats, one of them failing, one chat blocked by the access
// lists and a direct chat that the job never covers. The result is total=2,
// done=1, failed=1, and the blocked chat is never requested.
func TestChatFullSyncReadsAllowedChatsOnly(t *testing.T) {
	tg := &fakeChatFullTelegram{failing: map[int64]string{2: "CHANNEL_PRIVATE"}}
	m, db, clock := chatFullFixture(t, `{"block":[3]}`, tg)
	defer m.close()

	job, err := m.start(context.Background(), "", false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Total != 2 {
		t.Fatalf("the job took %d chats, want the two allowed ones: %+v", job.Total, job)
	}
	final := awaitState(t, m, rpc.ChatFullDone, rpc.ChatFullFailed)
	if final.Done != 1 || final.Failed != 1 {
		t.Fatalf("final job = %+v", final)
	}
	if final.State != rpc.ChatFullDone {
		t.Fatalf("state = %q", final.State)
	}
	requests := tg.requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %v, want one per chat", requests)
	}
	for _, id := range requests {
		if id == 3 || id == 4 {
			t.Errorf("a chat outside the job was requested: %d", id)
		}
	}
	// The worker pauses after every chat, the last one included. The job is final
	// before that last pause is recorded, so the worker is stopped first: close
	// waits for it, and the pause that is already under way is recorded.
	m.close()
	sleeps := clock.slept()
	if len(sleeps) != 2 {
		t.Errorf("pauses = %v, want one per chat", sleeps)
	}
	for _, sleep := range sleeps {
		if sleep != chatFullPause {
			t.Errorf("pause = %v, want %v", sleep, chatFullPause)
		}
	}
	// The failure is in the database, so the next start retries the chat.
	chat, err := db.GetChat(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if chat.FullError == nil || *chat.FullError != "CHANNEL_PRIVATE" || chat.FullSyncedAt != nil {
		t.Fatalf("the failed chat: %+v", chat)
	}
	// The successful chat left the list.
	pending, err := db.ChatsForFullSync(context.Background(), store.ChatFilter{Block: []int64{3}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != 2 {
		t.Fatalf("pending = %+v", pending)
	}
}

// TestChatFullSyncCancelStopsAfterTheCurrentChat covers cancellation: a cancel
// during the pause after the first chat stops the job before the second one.
func TestChatFullSyncCancelStopsAfterTheCurrentChat(t *testing.T) {
	tg := &fakeChatFullTelegram{}
	m, db, clock := chatFullFixture(t, "", tg)
	defer m.close()
	var once sync.Once
	clock.onSleep = func(time.Duration) { once.Do(func() { m.cancelJob() }) }

	if _, err := m.start(context.Background(), "", true); err != nil {
		t.Fatalf("start: %v", err)
	}
	final := awaitState(t, m, rpc.ChatFullCancelled, rpc.ChatFullDone)
	if final.State != rpc.ChatFullCancelled {
		t.Fatalf("state = %q", final.State)
	}
	if requests := tg.requests(); len(requests) != 1 {
		t.Fatalf("requests after the cancel = %v", requests)
	}
	// What the first chat saved stays.
	chat, err := db.GetChat(context.Background(), requestsFirst(tg))
	if err != nil {
		t.Fatal(err)
	}
	if chat.FullSyncedAt == nil {
		t.Errorf("the read chat was not saved: %+v", chat)
	}
}

// requestsFirst returns the id of the first requested chat.
func requestsFirst(tg *fakeChatFullTelegram) int64 {
	requests := tg.requests()
	if len(requests) == 0 {
		return 0
	}
	return requests[0]
}

// TestChatFullSyncFloodWait covers the flood wait: the job reports waiting_flood,
// waits the remaining seconds and reads the chat again.
func TestChatFullSyncFloodWait(t *testing.T) {
	tg := &fakeChatFullTelegram{flooding: map[int64]bool{1: true, 2: false, 3: false}}
	m, db, clock := chatFullFixture(t, "", tg)
	defer m.close()
	// The job reports the flood wait while it sleeps: the hook reads the status of
	// the pause itself.
	var seenMu sync.Mutex
	var seen []rpc.ChatFullJob
	clock.onSleep = func(d time.Duration) {
		if d == 3*time.Second {
			job := m.status()
			seenMu.Lock()
			seen = append(seen, job)
			seenMu.Unlock()
		}
	}
	if _, err := m.start(context.Background(), "", true); err != nil {
		t.Fatalf("start: %v", err)
	}
	final := awaitState(t, m, rpc.ChatFullDone, rpc.ChatFullFailed)
	seenMu.Lock()
	observed := append([]rpc.ChatFullJob(nil), seen...)
	seenMu.Unlock()
	if len(observed) != 1 || observed[0].State != rpc.ChatFullWaitingFlood || observed[0].WaitSeconds != 3 {
		t.Errorf("status during the flood wait = %+v, want one waiting_flood with 3 seconds", observed)
	}
	if final.WaitSeconds != 0 {
		t.Errorf("the finished job still reports a wait: %+v", final)
	}
	if final.Done != 3 || final.Failed != 0 {
		t.Fatalf("final job = %+v", final)
	}
	// The wait of three seconds is one of the sleeps, and the chat was read twice.
	var waited bool
	for _, sleep := range clock.slept() {
		if sleep == 3*time.Second {
			waited = true
		}
	}
	if !waited {
		t.Errorf("the job did not wait the flood delay: %v", clock.slept())
	}
	var twice bool
	count := 0
	for _, id := range tg.requests() {
		if id == 1 {
			count++
		}
	}
	twice = count == 2
	if !twice {
		t.Errorf("the flooded chat was requested %d times, want 2", count)
	}
	chat, err := db.GetChat(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if chat.FullSyncedAt == nil {
		t.Errorf("the chat that waited was not saved: %+v", chat)
	}
}

// TestChatFullSyncCancelBeforeTheWait keeps a cancel during a flood wait from
// sending the retry.
func TestChatFullSyncCancelBeforeTheWait(t *testing.T) {
	tg := &fakeChatFullTelegram{flooding: map[int64]bool{1: true}}
	m, _, clock := chatFullFixture(t, "", tg)
	defer m.close()
	clock.onSleep = func(d time.Duration) {
		if d == 3*time.Second {
			m.cancelJob()
		}
	}
	if _, err := m.start(context.Background(), "", true); err != nil {
		t.Fatalf("start: %v", err)
	}
	final := awaitState(t, m, rpc.ChatFullCancelled)
	if final.State != rpc.ChatFullCancelled {
		t.Fatalf("state = %q", final.State)
	}
	if requests := tg.requests(); len(requests) != 1 {
		t.Errorf("requests after the cancel = %v", requests)
	}
}

// TestChatFullSyncSecondStartKeepsTheJob keeps a second start from queueing the
// work twice while the first job is active.
func TestChatFullSyncSecondStartKeepsTheJob(t *testing.T) {
	tg := &fakeChatFullTelegram{}
	m, _, clock := chatFullFixture(t, "", tg)
	defer m.close()
	block := make(chan struct{})
	clock.onSleep = func(time.Duration) { <-block }
	if _, err := m.start(context.Background(), "", true); err != nil {
		t.Fatalf("start: %v", err)
	}
	awaitState(t, m, rpc.ChatFullRunning, rpc.ChatFullWaitingFlood, rpc.ChatFullQueued)
	second, err := m.start(context.Background(), "", true)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	close(block)
	awaitState(t, m, rpc.ChatFullDone)
	if second.Total != 3 {
		t.Fatalf("the second start reported %d chats", second.Total)
	}
	if requests := tg.requests(); len(requests) != 3 {
		t.Errorf("requests = %v, want one per chat and no repeat", requests)
	}
}

// TestChatFullSyncUnknownChat keeps a chat the database does not know a not_found
// answer, with no Telegram request.
func TestChatFullSyncUnknownChat(t *testing.T) {
	tg := &fakeChatFullTelegram{}
	m, _, _ := chatFullFixture(t, "", tg)
	defer m.close()
	_, err := m.start(context.Background(), "999", false)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests := tg.requests(); len(requests) != 0 {
		t.Errorf("requests = %v", requests)
	}
}

// TestAudit04_ChatFullSyncOneChatFollowsTheSelection covers selectChats with a chat
// argument: a chat in the selection is read alone, and a chat that the access
// lists close or that is no group or channel gets not_found from the selection
// itself, with no Telegram request.
func TestAudit04_ChatFullSyncOneChatFollowsTheSelection(t *testing.T) {
	t.Run("one chat of the selection", func(t *testing.T) {
		tg := &fakeChatFullTelegram{}
		m, _, _ := chatFullFixture(t, `{"block":[3]}`, tg)
		defer m.close()
		job, err := m.start(context.Background(), "1", false)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if job.Total != 1 {
			t.Fatalf("total = %d, want the one chat", job.Total)
		}
		awaitState(t, m, rpc.ChatFullDone)
		if requests := tg.requests(); len(requests) != 1 || requests[0] != 1 {
			t.Errorf("requests = %v, want only chat 1", requests)
		}
	})
	for _, tc := range []struct {
		name, chat string
	}{
		{"a chat the access lists close", "3"},
		{"a direct chat outside the selection", "4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := &fakeChatFullTelegram{}
			m, _, _ := chatFullFixture(t, `{"block":[3]}`, tg)
			defer m.close()
			_, err := m.start(context.Background(), tc.chat, false)
			var rpcErr *rpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
				t.Fatalf("unexpected error: %v", err)
			}
			if requests := tg.requests(); len(requests) != 0 {
				t.Errorf("requests = %v", requests)
			}
			if status := m.status(); status.State != rpc.ChatFullIdle {
				t.Errorf("a refused start queued a job: %+v", status)
			}
		})
	}
}
