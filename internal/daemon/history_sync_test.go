package daemon

import (
	"context"
	"encoding/json"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type syncTestClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*syncTestTimer]struct{}
	created chan time.Duration
}

type syncTestTimer struct {
	clock    *syncTestClock
	deadline time.Time
	ch       chan time.Time
}

func newSyncTestClock() *syncTestClock {
	return &syncTestClock{now: time.Unix(100, 0), timers: make(map[*syncTestTimer]struct{}), created: make(chan time.Duration, 20)}
}

func (c *syncTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *syncTestClock) NewTimer(d time.Duration) historySyncTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &syncTestTimer{clock: c, deadline: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers[timer] = struct{}{}
	c.created <- d
	return timer
}

func (t *syncTestTimer) C() <-chan time.Time { return t.ch }
func (t *syncTestTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	return active
}

func (c *syncTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for timer := range c.timers {
		if !timer.deadline.After(c.now) {
			timer.ch <- c.now
			delete(c.timers, timer)
		}
	}
	c.mu.Unlock()
}

func (c *syncTestClock) awaitTimer(t *testing.T) {
	t.Helper()
	select {
	case d := <-c.created:
		if d < historySyncPause {
			t.Fatalf("history pause = %s", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("history pause was not scheduled")
	}
}

type syncTelegram struct {
	*fakeTelegram
	page  func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error)
	flood func() time.Duration
}

func (s *syncTelegram) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	if ref.Kind == chatref.ID {
		s.mu.Lock()
		missing := s.missing
		s.mu.Unlock()
		if missing {
			return rpc.Chat{}, rpc.ErrUnknownChatID()
		}
		return rpc.Chat{ID: ref.ID, Type: "user"}, nil
	}
	return s.fakeTelegram.Resolve(ctx, ref)
}

func TestHistorySyncIdleStatusAndCancel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stored   bool
		complete bool
	}{
		{name: "no saved progress"},
		{name: "saved progress", stored: true},
		{name: "completed progress", stored: true, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, db, _, _ := syncFixture(t)
			if tc.stored {
				message := store.Message{ChatID: 42, ID: 7, Date: 7, Raw: []byte(`{}`)}
				if _, err := db.SaveHistoryPage(context.Background(), 42, []store.Message{message}, nil, tc.complete, store.GapPass{}); err != nil {
					t.Fatal(err)
				}
			}
			for _, handler := range []struct {
				name string
				call func() (any, error)
			}{
				{"status", func() (any, error) {
					return d.historySyncStatus(context.Background(), params(t, rpc.HistorySyncStatusParams{Chat: "42"}))
				}},
				{"cancel", func() (any, error) {
					return d.historySyncCancel(context.Background(), params(t, rpc.HistorySyncParams{Chat: "42"}))
				}},
			} {
				t.Run(handler.name, func(t *testing.T) {
					result, err := handler.call()
					if err != nil {
						t.Fatal(err)
					}
					job := result.(rpc.HistorySyncJob)
					if job.ChatID != 42 || job.State != rpc.HistorySyncIdle || job.Pages != 0 || job.Loaded != 0 || job.Complete != tc.complete || job.StartedAt != "" || job.UpdatedAt != "" || job.Error != "" || job.ErrorCode != 0 {
						t.Fatalf("idle job: %+v", job)
					}
					if tc.stored {
						if job.OldestID == nil || *job.OldestID != 7 || job.NewestID == nil || *job.NewestID != 7 {
							t.Fatalf("saved IDs: %+v", job)
						}
					} else if job.OldestID != nil || job.NewestID != nil {
						t.Fatalf("unsaved IDs: %+v", job)
					}
				})
			}
			result, err := d.historySyncStatus(context.Background(), nil)
			if err != nil || len(result.(rpc.HistorySyncJobs).Jobs) != 0 {
				t.Fatalf("idle job leaked into list: %+v err=%v", result, err)
			}
		})
	}
}

func TestHistorySyncIdleRejectsUnknownAndClosedChat(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *syncTelegram, string)
	}{
		{"unknown", func(_ *testing.T, tg *syncTelegram, _ string) {
			tg.mu.Lock()
			tg.missing = true
			tg.mu.Unlock()
		}},
		{"closed", func(t *testing.T, _ *syncTelegram, path string) { writeAccess(t, path, `{"allow":[99]}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, _, _, path := syncFixture(t)
			tc.setup(t, tg, path)
			for _, call := range []func() (any, error){
				func() (any, error) {
					return d.historySyncStatus(context.Background(), params(t, rpc.HistorySyncStatusParams{Chat: "42"}))
				},
				func() (any, error) {
					return d.historySyncCancel(context.Background(), params(t, rpc.HistorySyncParams{Chat: "42"}))
				},
			} {
				if _, err := call(); rpcCode(t, err) != rpc.CodeNotFound {
					t.Fatalf("%s chat: %v", tc.name, err)
				}
			}
		})
	}
}

func (s *syncTelegram) HistoryPage(ctx context.Context, ref chatref.Ref, offset int) (rpc.HistoryPage, error) {
	return s.page(ctx, ref, offset)
}

func (s *syncTelegram) FloodWaitRemaining() time.Duration {
	if s.flood != nil {
		return s.flood()
	}
	return 0
}

func syncFixture(t *testing.T) (*daemon, *syncTelegram, *store.DB, *syncTestClock, string) {
	t.Helper()
	d, base, db, accessPath := accessTestDaemon(t)
	tg := &syncTelegram{fakeTelegram: base}
	d.opts.Telegram = tg
	clock := newSyncTestClock()
	// The test messages are dated in 1970: the default limits would end every load.
	setHistoryLimit(d, "", 0)
	d.sync = newHistorySyncManager(context.Background(), d, clock)
	t.Cleanup(func() { d.sync.close() })
	return d, tg, db, clock, accessPath
}

func syncMessage(chatID int64, id int) rpc.Message {
	return rpc.Message{ChatID: chatID, ID: id, Date: time.Unix(int64(1000+id), 0).UTC().Format(time.RFC3339), Text: "message"}
}

func syncPage(chatID int64, ids ...int) rpc.HistoryPage {
	if len(ids) == 0 {
		return rpc.HistoryPage{Empty: true}
	}
	page := rpc.HistoryPage{Messages: make([]rpc.Message, 0, len(ids)), RawNextOffsetID: ids[0], OldestID: ids[0]}
	for _, id := range ids {
		page.Messages = append(page.Messages, syncMessage(chatID, id))
		page.RawNextOffsetID = min(page.RawNextOffsetID, id)
		page.OldestID = min(page.OldestID, id)
	}
	return page
}

func startSync(t *testing.T, d *daemon, chatID int64) rpc.HistorySyncJob {
	t.Helper()
	result, err := d.historySyncStart(context.Background(), params(t, rpc.HistorySyncParams{Chat: strconv.FormatInt(chatID, 10)}))
	if err != nil {
		t.Fatal(err)
	}
	return result.(rpc.HistorySyncJob)
}

func cancelSync(t *testing.T, d *daemon, chatID int64) *rpc.HistorySyncJob {
	t.Helper()
	result, err := d.historySyncCancel(context.Background(), params(t, rpc.HistorySyncParams{Chat: strconv.FormatInt(chatID, 10)}))
	if err != nil {
		t.Fatal(err)
	}
	return result.(*rpc.HistorySyncJob)
}

func awaitSyncState(t *testing.T, d *daemon, chatID int64, state string) rpc.HistorySyncJob {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		job := d.sync.snapshot(chatID)
		if job != nil && job.State == state {
			return *job
		}
		select {
		case <-deadline:
			t.Fatalf("job %d did not reach %s: %+v", chatID, state, job)
		default:
			runtime.Gosched()
		}
	}
}

func receiveOffset(t *testing.T, offsets <-chan int) int {
	t.Helper()
	select {
	case offset := <-offsets:
		return offset
	case <-time.After(2 * time.Second):
		t.Fatal("history request did not start")
		return 0
	}
}

func TestHistorySyncCompletePagesAndNoImplicitStart(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	offsets := make(chan int, 5)
	pages := []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42, 9, 8, 7), syncPage(42, 6, 5, 4), syncPage(42)}
	var mu sync.Mutex
	calls, active, maxActive := 0, 0, 0
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		mu.Lock()
		active++
		maxActive = max(maxActive, active)
		index := calls
		calls++
		mu.Unlock()
		// The request stays active until it returns, so an overlapping
		// request from the manager would raise maxActive.
		defer func() {
			mu.Lock()
			active--
			mu.Unlock()
		}()
		offsets <- offset
		runtime.Gosched()
		return pages[index], nil
	}
	if _, found, err := db.SyncState(context.Background(), 42); err != nil || found {
		t.Fatalf("sync state before start: found=%v err=%v", found, err)
	}
	select {
	case <-offsets:
		t.Fatal("history started without command")
	default:
	}
	if job := startSync(t, d, 42); job.State != rpc.HistorySyncQueued {
		t.Fatalf("start: %+v", job)
	}
	for i, want := range []int{0, 10, 7, 4} {
		if got := receiveOffset(t, offsets); got != want {
			t.Fatalf("request %d offset=%d want=%d", i, got, want)
		}
		if i < 3 {
			clock.awaitTimer(t)
			select {
			case <-offsets:
				t.Fatal("next page started before the pause")
			default:
			}
			clock.Advance(historySyncPause)
		}
	}
	job := awaitSyncState(t, d, 42, rpc.HistorySyncDone)
	if job.Pages != 4 || job.Loaded != 9 || job.OldestID == nil || *job.OldestID != 4 || job.NewestID == nil || *job.NewestID != 12 || !job.Complete || job.StartedAt == "" || job.UpdatedAt == "" {
		t.Fatalf("completed job: %+v", job)
	}
	mu.Lock()
	gotCalls, gotMax := calls, maxActive
	mu.Unlock()
	if gotCalls != 4 || gotMax != 1 {
		t.Fatalf("calls=%d concurrent=%d", gotCalls, gotMax)
	}
	if state, found, err := db.SyncState(context.Background(), 42); err != nil || !found || !state.Complete || state.OldestID == nil || *state.OldestID != 4 {
		t.Fatalf("stored progress: %+v found=%v err=%v", state, found, err)
	}
	if job := startSync(t, d, 42); job.State != rpc.HistorySyncDone {
		t.Fatalf("complete start: %+v", job)
	}
	mu.Lock()
	gotCalls = calls
	mu.Unlock()
	if gotCalls != 4 {
		t.Fatalf("complete history queried again: %d calls", gotCalls)
	}
}

func TestHistorySyncResumeAfterManagerRestart(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	offsets := make(chan int, 3)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		offsets <- offset
		return syncPage(42, 10), nil
	}
	startSync(t, d, 42)
	if got := receiveOffset(t, offsets); got != 0 {
		t.Fatalf("first offset=%d", got)
	}
	clock.awaitTimer(t)
	cancelSync(t, d, 42)
	d.sync.close()
	d.sync = newHistorySyncManager(context.Background(), d, clock)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		offsets <- offset
		return syncPage(42), nil
	}
	if job := startSync(t, d, 42); job.State != rpc.HistorySyncQueued || job.OldestID == nil || *job.OldestID != 10 {
		t.Fatalf("resumed start: %+v", job)
	}
	if got := receiveOffset(t, offsets); got != 10 {
		t.Fatalf("resumed offset=%d", got)
	}
	awaitSyncState(t, d, 42, rpc.HistorySyncDone)
	if state, found, err := db.SyncState(context.Background(), 42); err != nil || !found || !state.Complete {
		t.Fatalf("resumed progress: %+v found=%v err=%v", state, found, err)
	}
}

func TestHistorySyncDuplicateStartAndQueuedChats(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	entered := make(chan int, 2)
	release := make(chan struct{})
	tg.page = func(ctx context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		entered <- int(ref.ID)
		if ref.ID == 42 {
			select {
			case <-release:
			case <-ctx.Done():
				return rpc.HistoryPage{}, ctx.Err()
			}
		}
		return syncPage(ref.ID), nil
	}
	startSync(t, d, 42)
	if got := receiveOffset(t, entered); got != 42 {
		t.Fatalf("first chat=%d", got)
	}
	first := startSync(t, d, 42)
	if first.State != rpc.HistorySyncRunning || len(d.sync.snapshots()) != 1 {
		t.Fatalf("duplicate start: %+v", first)
	}
	second := startSync(t, d, 43)
	if second.State != rpc.HistorySyncQueued {
		t.Fatalf("second chat: %+v", second)
	}
	if again := startSync(t, d, 43); again.State != rpc.HistorySyncQueued || len(d.sync.snapshots()) != 2 {
		t.Fatalf("duplicate queued start: %+v", again)
	}
	select {
	case id := <-entered:
		t.Fatalf("chat %d started while first was active", id)
	default:
	}
	close(release)
	awaitSyncState(t, d, 42, rpc.HistorySyncDone)
	if got := receiveOffset(t, entered); got != 43 {
		t.Fatalf("queued chat=%d", got)
	}
	awaitSyncState(t, d, 43, rpc.HistorySyncDone)
}

func TestHistorySyncCancelDuringRequestAndPause(t *testing.T) {
	for _, during := range []string{"request", "pause"} {
		t.Run(during, func(t *testing.T) {
			d, tg, db, clock, _ := syncFixture(t)
			entered := make(chan int, 2)
			tg.page = func(ctx context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
				entered <- offset
				if during == "request" {
					<-ctx.Done()
					return rpc.HistoryPage{}, ctx.Err()
				}
				return syncPage(42, 10), nil
			}
			startSync(t, d, 42)
			receiveOffset(t, entered)
			if during == "pause" {
				clock.awaitTimer(t)
			}
			if job := cancelSync(t, d, 42); job.State != rpc.HistorySyncCancelled {
				t.Fatalf("cancel: %+v", job)
			}
			awaitSyncState(t, d, 42, rpc.HistorySyncCancelled)
			if job := cancelSync(t, d, 42); job.State != rpc.HistorySyncCancelled {
				t.Fatalf("second cancel: %+v", job)
			}
			select {
			case <-entered:
				t.Fatal("request after cancel")
			default:
			}
			_, found, err := db.SyncState(context.Background(), 42)
			if err != nil || found != (during == "pause") {
				t.Fatalf("progress after cancel: found=%v err=%v", found, err)
			}
		})
	}
}

func TestHistorySyncAccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *daemon, *syncTelegram, string)
		code  int
	}{
		{"closed before start", func(t *testing.T, _ *daemon, _ *syncTelegram, path string) {
			writeAccess(t, path, `{"allow":[999]}`)
		}, rpc.CodeNotFound},
		{"telegram error", func(_ *testing.T, _ *daemon, tg *syncTelegram, _ string) {
			tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
				return rpc.HistoryPage{}, rpc.NewError(rpc.CodeTelegram, "TELEGRAM_FAILURE")
			}
		}, rpc.CodeTelegram},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, _, _, path := syncFixture(t)
			tc.setup(t, d, tg, path)
			if tc.name == "closed before start" {
				_, err := d.historySyncStart(context.Background(), params(t, rpc.HistorySyncParams{Chat: "42"}))
				if rpcCode(t, err) != tc.code {
					t.Fatalf("closed chat: %v", err)
				}
				if jobs := d.sync.snapshots(); len(jobs) != 0 {
					t.Fatalf("closed chat visible: %+v", jobs)
				}
				return
			}
			startSync(t, d, 42)
			job := awaitSyncState(t, d, 42, rpc.HistorySyncFailed)
			if job.ErrorCode != tc.code || job.Error != "history sync failed" {
				t.Fatalf("failed job: %+v", job)
			}
		})
	}
}

func TestHistorySyncClosureAfterFirstPage(t *testing.T) {
	d, tg, db, clock, path := syncFixture(t)
	offsets := make(chan int, 2)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		offsets <- offset
		return syncPage(42, 10), nil
	}
	writeAccess(t, path, `{"allow":[42]}`)
	startSync(t, d, 42)
	receiveOffset(t, offsets)
	clock.awaitTimer(t)
	writeAccess(t, path, `{"allow":[999]}`)
	clock.Advance(historySyncPause)
	job := awaitSyncState(t, d, 42, rpc.HistorySyncFailed)
	if job.ErrorCode != rpc.CodeNotFound || job.Pages != 1 {
		t.Fatalf("closed job: %+v", job)
	}
	select {
	case <-offsets:
		t.Fatal("closed chat fetched again")
	default:
	}
	if state, found, err := db.SyncState(context.Background(), 42); err != nil || !found || state.OldestID == nil || *state.OldestID != 10 {
		t.Fatalf("previous progress: %+v found=%v err=%v", state, found, err)
	}
	result, err := d.historySyncStatus(context.Background(), nil)
	if err != nil || len(result.(rpc.HistorySyncJobs).Jobs) != 0 {
		t.Fatalf("closed job leaked to status: %+v err=%v", result, err)
	}
	_, err = d.historySyncStatus(context.Background(), params(t, rpc.HistorySyncStatusParams{Chat: "42"}))
	if rpcCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed chat status: %v", err)
	}
}

func TestHistorySyncClosesChatDuringRequest(t *testing.T) {
	d, tg, db, _, path := syncFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		close(entered)
		select {
		case <-release:
			return syncPage(42, 10), nil
		case <-ctx.Done():
			return rpc.HistoryPage{}, ctx.Err()
		}
	}
	writeAccess(t, path, `{"allow":[42]}`)
	startSync(t, d, 42)
	audit05Recv(t, entered, "history request start")
	writeAccess(t, path, `{"allow":[999]}`)
	close(release)
	job := awaitSyncState(t, d, 42, rpc.HistorySyncFailed)
	if job.ErrorCode != rpc.CodeNotFound || job.Pages != 0 {
		t.Fatalf("closed during request: %+v", job)
	}
	if _, found, err := db.SyncState(context.Background(), 42); err != nil || found {
		t.Fatalf("closed chat was saved: found=%v err=%v", found, err)
	}
}

func TestHistorySyncCancelQueuedTask(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	entered := make(chan int, 2)
	tg.page = func(ctx context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		entered <- int(ref.ID)
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	startSync(t, d, 42)
	receiveOffset(t, entered)
	startSync(t, d, 43)
	if job := cancelSync(t, d, 43); job.State != rpc.HistorySyncCancelled {
		t.Fatalf("queued cancel: %+v", job)
	}
	cancelSync(t, d, 42)
	d.sync.close()
	select {
	case id := <-entered:
		t.Fatalf("cancelled chat %d was queried", id)
	default:
	}
}

func TestHistorySyncPlaceholdersAndLoopGuard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pages      []rpc.HistoryPage
		wantState  string
		wantOldest int
		// storedAfterFirst: the first page already leaves sync progress. A
		// page of placeholders only moves the offset and stores nothing.
		storedAfterFirst bool
	}{
		{"placeholders", []rpc.HistoryPage{{RawNextOffsetID: 8}, syncPage(42, 7), syncPage(42)}, rpc.HistorySyncDone, 7, false},
		{"repeated offset", []rpc.HistoryPage{syncPage(42, 10), syncPage(42, 10)}, rpc.HistorySyncFailed, 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, db, clock, _ := syncFixture(t)
			offsets := make(chan int, 3)
			var mu sync.Mutex
			index := 0
			tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
				mu.Lock()
				page := tc.pages[index]
				index++
				mu.Unlock()
				offsets <- offset
				return page, nil
			}
			startSync(t, d, 42)
			receiveOffset(t, offsets)
			clock.awaitTimer(t)
			if _, found, err := db.SyncState(context.Background(), 42); err != nil || found != tc.storedAfterFirst {
				t.Fatalf("progress after first page: found=%v err=%v", found, err)
			}
			clock.Advance(historySyncPause)
			if got := receiveOffset(t, offsets); got != tc.pages[0].RawNextOffsetID {
				t.Fatalf("next offset=%d", got)
			}
			if tc.name == "placeholders" {
				clock.awaitTimer(t)
				clock.Advance(historySyncPause)
				receiveOffset(t, offsets)
			}
			job := awaitSyncState(t, d, 42, tc.wantState)
			if tc.name == "repeated offset" && (job.ErrorCode != rpc.CodeTelegram || job.Error == "") {
				t.Fatalf("loop guard: %+v", job)
			}
			if state, found, err := db.SyncState(context.Background(), 42); err != nil || !found || state.OldestID == nil || *state.OldestID != tc.wantOldest {
				t.Fatalf("stored progress: %+v found=%v err=%v", state, found, err)
			}
		})
	}
}

func TestHistorySyncWaitingFloodAndShutdown(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	entered := make(chan struct{})
	finished := make(chan struct{})
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return rpc.HistoryPage{}, ctx.Err()
	}
	tg.flood = func() time.Duration { return 119*time.Second + time.Millisecond }
	startSync(t, d, 42)
	audit05Recv(t, entered, "history request start")
	result, err := d.historySyncStatus(context.Background(), params(t, rpc.HistorySyncStatusParams{Chat: "42"}))
	if err != nil {
		t.Fatal(err)
	}
	job := result.(*rpc.HistorySyncJob)
	if job.State != rpc.HistorySyncWaitingFlood || job.WaitSeconds != 120 {
		t.Fatalf("flood status: %+v", job)
	}
	d.sync.close()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
	if state := d.sync.snapshot(42); state == nil || state.State != rpc.HistorySyncCancelled {
		t.Fatalf("shutdown job: %+v", state)
	}
}

func TestHistorySyncStatusAndCancelUseKnownReferenceLocally(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	entered := make(chan struct{})
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		close(entered)
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	if _, err := d.historySyncStart(context.Background(), params(t, rpc.HistorySyncParams{Chat: "@myteam"})); err != nil {
		t.Fatal(err)
	}
	audit05Recv(t, entered, "history request start")
	if result, err := d.historySyncStatus(context.Background(), params(t, rpc.HistorySyncStatusParams{Chat: "@myteam"})); err != nil || result.(*rpc.HistorySyncJob).ChatID != 77 {
		t.Fatalf("known status: %+v err=%v", result, err)
	}
	if result, err := d.historySyncCancel(context.Background(), params(t, rpc.HistorySyncParams{Chat: "@myteam"})); err != nil || result.(*rpc.HistorySyncJob).State != rpc.HistorySyncCancelled {
		t.Fatalf("known cancel: %+v err=%v", result, err)
	}
	tg.mu.Lock()
	resolves := tg.resolveCalls
	tg.mu.Unlock()
	if resolves != 1 {
		t.Fatalf("status/cancel resolved through Telegram %d times", resolves)
	}
}

func TestHistorySyncCancelUnknownDoesNotCreateJob(t *testing.T) {
	d, _, db, _, _ := syncFixture(t)
	result, err := d.historySyncCancel(context.Background(), params(t, rpc.HistorySyncParams{Chat: "42"}))
	if err != nil || result.(rpc.HistorySyncJob).State != rpc.HistorySyncIdle {
		t.Fatalf("idle cancel: %+v err=%v", result, err)
	}
	if len(d.sync.snapshots()) != 0 {
		t.Fatal("idle cancel created a job")
	}
	if _, found, err := db.SyncState(context.Background(), 42); err != nil || found {
		t.Fatalf("unknown cancel changed progress: found=%v err=%v", found, err)
	}
}

func TestHistorySyncFailedPageIsNotSaved(t *testing.T) {
	d, tg, db, _, _ := syncFixture(t)
	tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
		page := syncPage(42, 10)
		page.Messages[0].Date = "bad"
		return page, nil
	}
	startSync(t, d, 42)
	job := awaitSyncState(t, d, 42, rpc.HistorySyncFailed)
	if job.ErrorCode != rpc.CodeInternal {
		t.Fatalf("invalid page failure: %+v", job)
	}
	if _, found, err := db.SyncState(context.Background(), 42); err != nil || found {
		t.Fatalf("failed page changed progress: found=%v err=%v", found, err)
	}
}

func TestHistorySyncAdmissionDrainsAfterCancellation(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	d.opts.NewTelegram = func(context.Context, TelegramConfig) (Telegram, error) { return tg, nil }
	d.admission.unblock(tg)
	entered := make(chan struct{})
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		close(entered)
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	startSync(t, d, 42)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("history request did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.admission.drain(ctx, 0); err != nil {
		t.Fatal(err)
	}
	awaitSyncState(t, d, 42, rpc.HistorySyncCancelled)
}

// setHistoryLimit sets the live history limits of a test daemon: since is
// YYYY-MM-DD or empty, messages 0 turns the count limit off.
func setHistoryLimit(d *daemon, since string, messages int) {
	d.liveMu.Lock()
	defer d.liveMu.Unlock()
	if d.liveValues == nil {
		d.liveValues = make(map[string]json.RawMessage)
	}
	d.liveValues["history_limit.since"], _ = json.Marshal(since)
	d.liveValues["history_limit.messages"], _ = json.Marshal(messages)
}
