package daemon

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func syncRef(chatID int64) chatref.Ref { return chatref.Ref{Kind: chatref.ID, ID: chatID} }

// gatedPages makes every history request of a chat block until the test
// releases it or the job context ends, and reports the chats in request order.
type gatedPages struct {
	mu      sync.Mutex
	release map[int64]chan struct{}
	calls   chan chatCall
}

type chatCall struct {
	chat   int64
	offset int
}

func newGatedPages(tg *syncTelegram, chats ...int64) *gatedPages {
	g := &gatedPages{release: make(map[int64]chan struct{}), calls: make(chan chatCall, 20)}
	for _, id := range chats {
		g.release[id] = make(chan struct{})
	}
	tg.page = g.page
	return g
}

func (g *gatedPages) open(chat int64) { close(g.release[chat]) }

func (g *gatedPages) next(t *testing.T) chatCall {
	t.Helper()
	select {
	case call := <-g.calls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("history request did not start")
		return chatCall{}
	}
}

func (g *gatedPages) none(t *testing.T) {
	t.Helper()
	select {
	case call := <-g.calls:
		t.Fatalf("unexpected history request: %+v", call)
	default:
	}
}

func (c *syncTestClock) awaitPause(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.created:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("history pause was not scheduled")
		return 0
	}
}

func TestHistorySyncPauseAndSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		start  func(*testing.T, *daemon, *syncTelegram) rpc.HistorySyncJob
		pauses []time.Duration
	}{
		{
			name: "manual", source: rpc.HistorySyncManual, pauses: []time.Duration{2 * time.Second, 2 * time.Second},
			start: func(t *testing.T, d *daemon, _ *syncTelegram) rpc.HistorySyncJob { return startSync(t, d, 42) },
		},
		{
			name: "background", source: rpc.HistorySyncBackground, pauses: []time.Duration{5 * time.Second, 3500 * time.Millisecond},
			start: func(t *testing.T, d *daemon, _ *syncTelegram) rpc.HistorySyncJob {
				next := []time.Duration{5 * time.Second, 3500 * time.Millisecond}
				pause := func() time.Duration {
					got := next[0]
					next = next[1:]
					return got
				}
				job, err := d.sync.startBackground(42, syncRef(42), store.SyncProgress{}, pause)
				if err != nil {
					t.Fatal(err)
				}
				return job
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, _, clock, _ := syncFixture(t)
			pages := []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42, 9, 8, 7), syncPage(42)}
			var mu sync.Mutex
			index := 0
			tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
				mu.Lock()
				defer mu.Unlock()
				page := pages[index]
				index++
				return page, nil
			}
			if job := tc.start(t, d, tg); job.Source != tc.source || job.State != rpc.HistorySyncQueued {
				t.Fatalf("start: %+v", job)
			}
			for i, want := range tc.pauses {
				if got := clock.awaitPause(t); got != want {
					t.Fatalf("pause %d = %s, want %s", i, got, want)
				}
				if job := d.sync.snapshot(42); job.Source != tc.source {
					t.Fatalf("running source = %q: %+v", job.Source, job)
				}
				clock.Advance(want)
			}
			job := awaitSyncState(t, d, 42, rpc.HistorySyncDone)
			if job.Source != tc.source || job.Pages != 3 || !job.Complete {
				t.Fatalf("done job: %+v", job)
			}
			list := d.sync.snapshots()
			if len(list) != 1 || list[0].Source != tc.source {
				t.Fatalf("snapshots: %+v", list)
			}
		})
	}
}

func TestHistorySyncIdleStatusHasNoSource(t *testing.T) {
	d, _, _, _, _ := syncFixture(t)
	result, err := d.historySyncStatus(context.Background(), params(t, rpc.HistorySyncStatusParams{Chat: "42"}))
	if err != nil {
		t.Fatal(err)
	}
	if job := result.(rpc.HistorySyncJob); job.State != rpc.HistorySyncIdle || job.Source != "" {
		t.Fatalf("idle job: %+v", job)
	}
}

func TestBackgroundSyncManualStartCancelsRunningBackground(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	gate := newGatedPages(tg, 77, 78)
	pages := 0
	tg.page = func(ctx context.Context, ref chatref.Ref, offset int) (rpc.HistoryPage, error) {
		if ref.ID == 42 && pages == 0 {
			pages++
			gate.calls <- chatCall{chat: 42, offset: offset}
			return syncPage(42, 12, 11, 10), nil
		}
		if ref.ID == 42 {
			gate.calls <- chatCall{chat: 42, offset: offset}
			<-ctx.Done()
			return rpc.HistoryPage{}, ctx.Err()
		}
		return gate.page(ctx, ref, offset)
	}
	if _, err := d.sync.startBackground(42, syncRef(42), store.SyncProgress{}, func() time.Duration { return 4 * time.Second }); err != nil {
		t.Fatal(err)
	}
	if call := gate.next(t); call != (chatCall{chat: 42}) {
		t.Fatalf("first request: %+v", call)
	}
	if got := clock.awaitPause(t); got != 4*time.Second {
		t.Fatalf("pause = %s", got)
	}
	clock.Advance(4 * time.Second)
	if call := gate.next(t); call != (chatCall{chat: 42, offset: 10}) {
		t.Fatalf("second request: %+v", call)
	}

	if job := startSync(t, d, 77); job.State != rpc.HistorySyncQueued || job.Source != rpc.HistorySyncManual {
		t.Fatalf("manual start: %+v", job)
	}
	if job := startSync(t, d, 78); job.State != rpc.HistorySyncQueued {
		t.Fatalf("second manual start: %+v", job)
	}
	cancelled := awaitSyncState(t, d, 42, rpc.HistorySyncCancelled)
	if cancelled.Source != rpc.HistorySyncBackground || cancelled.Pages != 1 || cancelled.Loaded != 3 {
		t.Fatalf("cancelled background job: %+v", cancelled)
	}
	if state, found, err := db.SyncState(context.Background(), 42); err != nil || !found || state.OldestID == nil || *state.OldestID != 10 || state.Complete {
		t.Fatalf("progress after cancel: %+v found=%v err=%v", state, found, err)
	}
	// The manual loads run in their arrival order, one at a time.
	if call := gate.next(t); call.chat != 77 {
		t.Fatalf("first manual request: %+v", call)
	}
	gate.none(t)
	gate.open(77)
	if call := gate.next(t); call.chat != 78 {
		t.Fatalf("second manual request: %+v", call)
	}
	gate.open(78)
	awaitSyncState(t, d, 77, rpc.HistorySyncDone)
	awaitSyncState(t, d, 78, rpc.HistorySyncDone)
}

func (g *gatedPages) page(ctx context.Context, ref chatref.Ref, offset int) (rpc.HistoryPage, error) {
	g.calls <- chatCall{chat: ref.ID, offset: offset}
	g.mu.Lock()
	gate := g.release[ref.ID]
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return rpc.HistoryPage{}, ctx.Err()
	case <-gate:
		return syncPage(ref.ID), nil
	}
}

func TestBackgroundSyncManualStartCancelsQueuedBackground(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	gate := newGatedPages(tg, 10, 30)
	startSync(t, d, 10)
	if call := gate.next(t); call.chat != 10 {
		t.Fatalf("first request: %+v", call)
	}
	// startBackground refuses a busy manager, so a queued background job is
	// placed under the lock the way a start racing the worker would leave it.
	d.sync.mu.Lock()
	d.sync.enqueueLocked(20, syncRef(20), store.SyncProgress{}, rpc.HistorySyncBackground, nil)
	d.sync.mu.Unlock()
	if job := startSync(t, d, 30); job.State != rpc.HistorySyncQueued {
		t.Fatalf("manual start: %+v", job)
	}
	if job := d.sync.snapshot(20); job.State != rpc.HistorySyncCancelled || job.Source != rpc.HistorySyncBackground {
		t.Fatalf("queued background job: %+v", job)
	}
	if job := d.sync.snapshot(10); job.State != rpc.HistorySyncRunning {
		t.Fatalf("running manual job: %+v", job)
	}
	gate.open(10)
	if call := gate.next(t); call.chat != 30 {
		t.Fatalf("request after the first manual job: %+v", call)
	}
	gate.open(30)
	awaitSyncState(t, d, 30, rpc.HistorySyncDone)
	gate.none(t)
}

func TestBackgroundSyncStartOnBusyManagerReturnsBusy(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	gate := newGatedPages(tg, 10)
	startSync(t, d, 10)
	gate.next(t)
	for name, prepare := range map[string]func(){
		"current job": func() {},
		"queued job": func() {
			d.sync.mu.Lock()
			d.sync.enqueueLocked(11, syncRef(11), store.SyncProgress{}, rpc.HistorySyncManual, nil)
			d.sync.mu.Unlock()
		},
	} {
		prepare()
		_, err := d.sync.startBackground(55, syncRef(55), store.SyncProgress{}, nil)
		if rpcCode(t, err) != rpc.CodeBusy {
			t.Fatalf("%s: %v", name, err)
		}
		if job := d.sync.snapshot(55); job != nil {
			t.Fatalf("%s: busy start left a job: %+v", name, job)
		}
	}
	gate.open(10)
}

func TestBackgroundSyncStartOnIdleManagerQueuesAndStopsWithManager(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	gate := newGatedPages(tg, 55)
	job, err := d.sync.startBackground(55, syncRef(55), store.SyncProgress{}, nil)
	if err != nil || job.Source != rpc.HistorySyncBackground {
		t.Fatalf("start: %+v err=%v", job, err)
	}
	gate.next(t)
	d.sync.close()
	if _, err := d.sync.startBackground(56, syncRef(56), store.SyncProgress{}, nil); rpcCode(t, err) != rpc.CodeBusy {
		t.Fatalf("start after close: %v", err)
	}
}

// awaitSyncIdle waits until the worker has left the finished job: a job turns
// done a moment before the manager stops counting it as current.
func awaitSyncIdle(t *testing.T, d *daemon) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for d.sync.hasActive() {
		select {
		case <-deadline:
			t.Fatal("history sync manager stayed busy")
		default:
			runtime.Gosched()
		}
	}
}

func TestBackgroundSyncCancelBackgroundLeavesManualJobs(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	d.sync.cancelBackground() // nothing to cancel
	gate := newGatedPages(tg, 10)
	startSync(t, d, 10)
	gate.next(t)
	d.sync.cancelBackground()
	if job := d.sync.snapshot(10); job.State != rpc.HistorySyncRunning {
		t.Fatalf("manual job after cancelBackground: %+v", job)
	}
	gate.open(10)
	awaitSyncState(t, d, 10, rpc.HistorySyncDone)
	awaitSyncIdle(t, d)

	// A background job cancelled while it waits between pages keeps its
	// saved page, and the next load continues from oldest_id.
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		gate.calls <- chatCall{chat: 42, offset: offset}
		return syncPage(42, 12, 11, 10), nil
	}
	if _, err := d.sync.startBackground(42, syncRef(42), store.SyncProgress{}, func() time.Duration { return 6 * time.Second }); err != nil {
		t.Fatal(err)
	}
	gate.next(t)
	if got := clock.awaitPause(t); got != 6*time.Second {
		t.Fatalf("pause = %s", got)
	}
	d.sync.cancelBackground()
	job := awaitSyncState(t, d, 42, rpc.HistorySyncCancelled)
	if job.Pages != 1 || job.Source != rpc.HistorySyncBackground {
		t.Fatalf("cancelled job: %+v", job)
	}
	if state, _, err := db.SyncState(context.Background(), 42); err != nil || state.OldestID == nil || *state.OldestID != 10 {
		t.Fatalf("saved progress: %+v err=%v", state, err)
	}
	startSync(t, d, 42)
	if call := gate.next(t); call != (chatCall{chat: 42, offset: 10}) {
		t.Fatalf("resumed request: %+v", call)
	}
}

type waitResult struct {
	job rpc.HistorySyncJob
	err error
}

func waitJobAsync(ctx context.Context, d *daemon, chatID int64) <-chan waitResult {
	out := make(chan waitResult, 1)
	go func() {
		job, err := d.sync.waitJob(ctx, chatID)
		out <- waitResult{job, err}
	}()
	return out
}

func receiveWait(t *testing.T, ch <-chan waitResult) waitResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("waitJob did not return")
		return waitResult{}
	}
}

func TestBackgroundSyncWaitJobReturnsFinalSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		want  string
		setup func(*syncTelegram, *gatedPages)
		end   func(*testing.T, *daemon, *gatedPages)
	}{
		{"done", rpc.HistorySyncDone, nil, func(_ *testing.T, _ *daemon, g *gatedPages) { g.open(42) }},
		{"failed", rpc.HistorySyncFailed, func(tg *syncTelegram, g *gatedPages) {
			tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
				g.calls <- chatCall{chat: 42}
				<-g.release[42]
				return rpc.HistoryPage{}, errors.New("boom")
			}
		}, func(_ *testing.T, _ *daemon, g *gatedPages) { g.open(42) }},
		{"cancelled", rpc.HistorySyncCancelled, nil, func(_ *testing.T, d *daemon, _ *gatedPages) { d.sync.cancelJob(42) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, _, _, _ := syncFixture(t)
			gate := newGatedPages(tg, 42)
			if tc.setup != nil {
				tc.setup(tg, gate)
			}
			startSync(t, d, 42)
			gate.next(t)
			waiter := waitJobAsync(context.Background(), d, 42)
			tc.end(t, d, gate)
			result := receiveWait(t, waiter)
			if result.err != nil || result.job.State != tc.want || result.job.ChatID != 42 {
				t.Fatalf("waitJob: %+v err=%v", result.job, result.err)
			}
		})
	}
}

func TestBackgroundSyncWaitJobHonorsContextAndMissingJob(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	gate := newGatedPages(tg, 42)
	if job, err := d.sync.waitJob(context.Background(), 99); err != nil || job.State != rpc.HistorySyncIdle || job.ChatID != 99 {
		t.Fatalf("chat without a job: %+v err=%v", job, err)
	}
	startSync(t, d, 42)
	gate.next(t)
	ctx, cancel := context.WithCancel(context.Background())
	waiter := waitJobAsync(ctx, d, 42)
	cancel()
	if result := receiveWait(t, waiter); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("waitJob after context cancel: %+v err=%v", result.job, result.err)
	}
	if job := d.sync.snapshot(42); job.State != rpc.HistorySyncRunning {
		t.Fatalf("job after a cancelled wait: %+v", job)
	}
	gate.open(42)
	if job, err := d.sync.waitJob(context.Background(), 42); err != nil || job.State != rpc.HistorySyncDone {
		t.Fatalf("waitJob on a finished job: %+v err=%v", job, err)
	}
}

func TestBackgroundSyncCloseReleasesWaiters(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	gate := newGatedPages(tg, 10)
	startSync(t, d, 10)
	gate.next(t)
	startSync(t, d, 11)
	var waiters []<-chan waitResult
	for _, id := range []int64{10, 11} {
		waiters = append(waiters, waitJobAsync(context.Background(), d, id))
	}
	d.sync.close()
	for i, waiter := range waiters {
		result := receiveWait(t, waiter)
		if result.err != nil || result.job.State != rpc.HistorySyncCancelled {
			t.Fatalf("waiter %d: %+v err=%v", i, result.job, result.err)
		}
	}
}
