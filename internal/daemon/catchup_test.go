package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestAliveInvalidSnapshot(t *testing.T) {
	d, _, db, _, _ := syncFixture(t)
	if err := db.SetAccountState(context.Background(), "last_alive_at", json.RawMessage(`"bad"`)); err != nil {
		t.Fatal(err)
	}
	if err := d.loadLastAlive(context.Background(), time.Now()); err != nil || d.catchupSince != nil {
		t.Fatalf("snapshot: %v %v", d.catchupSince, err)
	}
}

func TestCatchupPriorityAndCancel(t *testing.T) {
	d, _, _, _, _ := syncFixture(t)
	m := d.sync
	m.mu.Lock()
	m.enqueueLocked(1, syncRef(1), store.SyncProgress{}, rpc.HistorySyncBackground, nil, historyDepth{}, false, laneBackground)
	m.enqueueLocked(2, syncRef(2), store.SyncProgress{}, rpc.HistorySyncCatchup, nil, historyDepth{}, false, laneCatchup)
	m.enqueueLocked(3, syncRef(3), store.SyncProgress{}, rpc.HistorySyncCatchup, nil, historyDepth{}, false, laneCatchup)
	m.enqueueLocked(4, syncRef(4), store.SyncProgress{}, rpc.HistorySyncBackground, nil, historyDepth{}, false, laneCatchup)
	m.enqueueLocked(5, syncRef(5), store.SyncProgress{}, rpc.HistorySyncCatchup, nil, historyDepth{}, false, laneCatchup)
	m.enqueueLocked(6, syncRef(6), store.SyncProgress{}, rpc.HistorySyncManual, nil, historyDepth{}, false, laneManual)
	want := []int64{6, 2, 3, 4, 5, 1}
	for i, id := range want {
		if m.queue[i].ChatID != id {
			t.Fatalf("priority: %+v", m.queue)
		}
	}
	m.cancelBackgroundLocked()
	if len(m.queue) != 4 {
		t.Fatalf("cancel background: %d", len(m.queue))
	}
	m.cancel()
	m.mu.Unlock()
	if n := m.cancelAll(); n != 4 {
		t.Fatalf("cancel all: %d", n)
	}
}

func TestCatchupAndManualSameChat(t *testing.T) {
	d, _, _, _, _ := syncFixture(t)
	m := d.sync
	m.mu.Lock()
	m.enqueueLocked(42, syncRef(42), store.SyncProgress{}, rpc.HistorySyncCatchup, nil, historyDepth{}, false, laneCatchup)
	m.enqueueLocked(42, syncRef(42), store.SyncProgress{}, rpc.HistorySyncManual, nil, historyDepth{}, false, laneManual)
	m.cancel()
	m.mu.Unlock()
	if s := m.snapshot(42); s == nil || s.Source != rpc.HistorySyncManual {
		t.Fatalf("snapshot: %+v", s)
	}
	m.cancelJob(42)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[42].State != rpc.HistorySyncCancelled || m.catchups[42].State != rpc.HistorySyncCancelled {
		t.Fatal("both jobs must be cancelled")
	}
}

func TestCatchupFirstPauseAndDate(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	calls := make(chan struct{}, 1)
	tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
		calls <- struct{}{}
		return syncPage(42, 10, 9), nil
	}
	if !d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1010, 0), 1000, func() time.Duration { return 3 * time.Second }) {
		t.Fatal("not queued")
	}
	if got := clock.awaitPause(t); got != 3*time.Second {
		t.Fatalf("pause %v", got)
	}
	select {
	case <-calls:
		t.Fatal("page before pause")
	default:
	}
	clock.Advance(3 * time.Second)
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || job.State != rpc.HistorySyncDone || job.Loaded != 2 {
		t.Fatalf("job: %+v %v", job, err)
	}
	if snapshot := d.sync.snapshot(42); snapshot == nil || snapshot.GapFrom != nil || snapshot.GapTo != nil {
		t.Fatalf("catchup without gap: %+v", snapshot)
	}
	count, err := db.CountChatMessages(context.Background(), 42)
	if err != nil || count != 2 {
		t.Fatalf("saved: %d %v", count, err)
	}
}

func TestCatchupAutoSwitchesAndDowntime(t *testing.T) {
	for _, tc := range []struct {
		name                string
		catchup, background bool
		downtime            time.Duration
		want                bool
	}{
		{"disabled catchup", false, true, 11 * time.Minute, false}, {"disabled background", true, false, 11 * time.Minute, false}, {"both off", false, false, 11 * time.Minute, false}, {"nine minutes", true, true, 9 * time.Minute, false}, {"eleven minutes", true, true, 11 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, db, _, _ := syncFixture(t)
			ctx := context.Background()
			now := time.Unix(10000, 0)
			d.opts.CatchupEnabled = tc.catchup
			if tc.background {
				d.background = newBackgroundSync(d, BackgroundSyncConfig{PagePauseMin: time.Second, PagePauseMax: time.Second}, nil, nil)
			}
			d.account.set(&rpc.User{ID: 1})
			tg.chats = []rpc.Chat{{ID: 42, LastMessageDate: 10000}}
			tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
				t.Error("unexpected history page")
				return rpc.HistoryPage{}, nil
			}
			if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 1, Date: 1, Raw: json.RawMessage(`{}`)}}, nil, false, store.GapPass{}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetLastAliveAt(ctx, now.Add(-tc.downtime).Unix()); err != nil {
				t.Fatal(err)
			}
			if err := d.loadLastAlive(ctx, now); err != nil {
				t.Fatal(err)
			}
			d.autoCatchup(ctx)
			tg.mu.Lock()
			calls := tg.chatsCalls
			tg.mu.Unlock()
			if (calls > 0) != tc.want || d.sync.activeJob(42) != tc.want {
				t.Fatalf("calls=%d active=%v want=%v", calls, d.sync.activeJob(42), tc.want)
			}
		})
	}
}

func TestCatchupDialogSelection(t *testing.T) {
	d, tg, db, _, path := syncFixture(t)
	ctx := context.Background()
	d.opts.CatchupEnabled = true
	d.background = newBackgroundSync(d, BackgroundSyncConfig{PagePauseMin: time.Second, PagePauseMax: time.Second}, nil, nil)
	writeAccess(t, path, `{"block":[4]}`)
	tg.chats = []rpc.Chat{{ID: 9, Pinned: true, LastMessageDate: 90}, {ID: 4, LastMessageDate: 300}, {ID: 1, LastMessageDate: 250}, {ID: 2, LastMessageDate: 230}, {ID: 3, LastMessageDate: 180}, {ID: 5, LastMessageDate: 99}, {ID: 6, LastMessageDate: 200}}
	for _, id := range []int64{1, 3, 4, 6} {
		if _, err := db.SaveHistoryPage(ctx, id, []store.Message{{ChatID: id, ID: 1, Date: 1, Raw: json.RawMessage(`{}`)}}, nil, false, store.GapPass{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetHistoryMode(ctx, 1, "skip"); err != nil {
		t.Fatal(err)
	}
	result, err := d.queueCatchup(ctx, time.Unix(100, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.WouldQueue != 2 || len(result.Chats) != 2 || result.Chats[0].Chat.ID != 1 || result.Chats[1].Chat.ID != 3 {
		t.Fatalf("result: %+v", result)
	}
	result, err = d.queueCatchup(ctx, time.Unix(100, 0), false)
	if err != nil || result.Queued != 2 || len(result.Chats) != 2 || d.sync.activeJob(4) {
		t.Fatalf("closed chat in queue: %+v, %v", result, err)
	}
}

func TestCatchupLimitMarksGap(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 1, Date: 1, Raw: json.RawMessage(`{}`)}}, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		if offset == 0 {
			offset = 2001
		}
		ids := make([]int, 100)
		for i := range ids {
			ids[i] = offset - i - 1
		}
		return syncPage(42, ids...), nil
	}
	if !d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second }) {
		t.Fatal("not queued")
	}
	for i := 0; i < 10; i++ {
		if clock.awaitPause(t) != time.Second {
			t.Fatal("pause")
		}
		clock.Advance(time.Second)
	}
	job, err := d.sync.waitJob(ctx, 42)
	if err != nil || job.Loaded != 1000 || job.State != rpc.HistorySyncDone {
		t.Fatalf("job: %+v %v", job, err)
	}
	state, _, err := db.SyncState(ctx, 42)
	if err != nil || state.GapFrom == nil || *state.GapFrom != 1 || state.GapTo == nil || *state.GapTo != 1001 {
		t.Fatalf("gap: %+v %v", state, err)
	}
	snapshot := d.sync.snapshot(42)
	if snapshot == nil || snapshot.State != rpc.HistorySyncDone || snapshot.GapFrom == nil || snapshot.GapTo == nil || *snapshot.GapFrom != *state.GapFrom || *snapshot.GapTo != *state.GapTo {
		t.Fatalf("catchup gap snapshot: %+v, stored: %+v", snapshot, state)
	}
}

func TestCatchupAccessRevokedDuringPage(t *testing.T) {
	d, tg, db, clock, path := syncFixture(t)
	tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
		writeAccess(t, path, `{"block":[42]}`)
		return syncPage(42, 10), nil
	}
	d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second })
	clock.awaitPause(t)
	clock.Advance(time.Second)
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || job.State != rpc.HistorySyncFailed || job.ErrorCode != rpc.CodeNotFound {
		t.Fatalf("job: %+v %v", job, err)
	}
	count, err := db.CountChatMessages(context.Background(), 42)
	if err != nil || count != 0 {
		t.Fatalf("saved %d %v", count, err)
	}
}

func TestCatchupAuthLost(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	d.opts.CatchupEnabled = true
	d.background = newBackgroundSync(d, BackgroundSyncConfig{}, nil, nil)
	d.account.set(&rpc.User{ID: 1})
	d.setAuthLost("lost")
	since := time.Unix(1, 0)
	d.catchupSince = &since
	d.autoCatchup(context.Background())
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.chatsCalls != 0 || d.sync.hasActive() {
		t.Fatal("auth lost queued catchup")
	}
}

func TestCatchupEmptyAndFlood(t *testing.T) {
	for _, tc := range []struct {
		name  string
		page  rpc.HistoryPage
		err   error
		state string
		code  int
	}{
		{"empty", rpc.HistoryPage{Empty: true}, nil, rpc.HistorySyncDone, 0},
		{"flood", rpc.HistoryPage{}, rpc.NewError(rpc.CodeFloodWait, "wait"), rpc.HistorySyncFailed, rpc.CodeFloodWait},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, _, clock, _ := syncFixture(t)
			tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) { return tc.page, tc.err }
			d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second })
			clock.awaitPause(t)
			clock.Advance(time.Second)
			job, err := d.sync.waitJob(context.Background(), 42)
			if err != nil || job.State != tc.state || job.ErrorCode != tc.code {
				t.Fatalf("job: %+v %v", job, err)
			}
		})
	}
}

func TestCatchupCancelBeforePage(t *testing.T) {
	d, tg, _, clock, _ := syncFixture(t)
	calls := make(chan struct{}, 1)
	tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
		calls <- struct{}{}
		return rpc.HistoryPage{}, nil
	}
	d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second })
	clock.awaitPause(t)
	result := d.sync.cancelJob(42)
	if result == nil || result.State != rpc.HistorySyncCancelled {
		t.Fatalf("cancel: %+v", result)
	}
	select {
	case <-calls:
		t.Fatal("request after cancellation")
	default:
	}
}

func TestCatchupManualStartKeepsCatchup(t *testing.T) {
	d, _, _, clock, _ := syncFixture(t)
	if !d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second }) {
		t.Fatal("catchup not queued")
	}
	clock.awaitPause(t)
	job, err := d.sync.startDepth(42, syncRef(42), store.SyncProgress{}, historyDepth{}, false)
	if err != nil || job.Source != rpc.HistorySyncManual || job.State != rpc.HistorySyncQueued {
		t.Fatalf("manual: %+v %v", job, err)
	}
	if got := d.sync.snapshot(42); got == nil || got.Source != rpc.HistorySyncManual {
		t.Fatalf("status: %+v", got)
	}
	jobs := d.sync.snapshots()
	if len(jobs) != 2 {
		t.Fatalf("all jobs: %+v", jobs)
	}
	result := d.sync.cancelJob(42)
	if result == nil || result.State != rpc.HistorySyncCancelled {
		t.Fatalf("cancel: %+v", result)
	}
	if d.sync.activeJob(42) {
		t.Fatal("catchup survived cancel")
	}
}

func TestCatchupSelectionReportsActive(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	d.opts.CatchupEnabled = true
	d.background = newBackgroundSync(d, BackgroundSyncConfig{}, nil, nil)
	tg.chats = []rpc.Chat{{ID: 42, LastMessageDate: 200}}
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 1, Date: 1, Raw: json.RawMessage(`{}`)}}, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	d.sync.startCatchup(42, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second })
	clock.awaitPause(t)
	r, err := d.queueCatchup(ctx, time.Unix(100, 0), true)
	if err != nil || r.SkippedActive != 1 || len(r.Chats) != 1 || r.Chats[0].Action != rpc.HistorySyncActionSkippedActive {
		t.Fatalf("result: %+v %v", r, err)
	}
}

func TestCatchupRunsForSkippedHistoryMode(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	d.opts.CatchupEnabled = true
	d.background = newBackgroundSync(d, BackgroundSyncConfig{PagePauseMin: time.Second, PagePauseMax: time.Second}, nil, nil)
	tg.chats = []rpc.Chat{{ID: 42, LastMessageDate: 2000}}
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 1, Date: 1, Raw: json.RawMessage(`{}`)}}, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetHistoryMode(ctx, 42, "skip"); err != nil {
		t.Fatal(err)
	}
	tg.page = func(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) { return syncPage(42, 9), nil }
	r, err := d.queueCatchup(ctx, time.Unix(1100, 0), false)
	if err != nil || r.Queued != 1 {
		t.Fatalf("queue: %+v %v", r, err)
	}
	clock.awaitPause(t)
	clock.Advance(time.Second)
	job, err := d.sync.waitJob(ctx, 42)
	if err != nil || job.State != rpc.HistorySyncDone || job.Loaded != 1 {
		t.Fatalf("job: %+v %v", job, err)
	}
}

func TestCatchupWaitsForRunningBackground(t *testing.T) {
	d, tg, _, clock, _ := syncFixture(t)
	gate := newGatedPages(tg, 10, 20, 30)
	if _, err := d.sync.startBackground(10, syncRef(10), store.SyncProgress{}, func() time.Duration { return time.Second }); err != nil {
		t.Fatal(err)
	}
	if call := gate.next(t); call.chat != 10 {
		t.Fatalf("first: %+v", call)
	}
	d.sync.mu.Lock()
	d.sync.enqueueLocked(30, syncRef(30), store.SyncProgress{}, rpc.HistorySyncBackground, nil, historyDepth{}, false, laneBackground)
	d.sync.mu.Unlock()
	if !d.sync.startCatchup(20, store.SyncProgress{}, time.Unix(1, 0), 1, func() time.Duration { return time.Second }) {
		t.Fatal("catchup not queued")
	}
	if job := d.sync.snapshot(10); job == nil || job.State != rpc.HistorySyncRunning {
		t.Fatalf("background interrupted: %+v", job)
	}
	gate.open(10)
	clock.awaitPause(t)
	clock.Advance(time.Second)
	if call := gate.next(t); call.chat != 20 {
		t.Fatalf("second: %+v", call)
	}
	gate.open(20)
	if call := gate.next(t); call.chat != 30 {
		t.Fatalf("third: %+v", call)
	}
	gate.open(30)
}

func TestCatchupRPC(t *testing.T) {
	ctx := context.Background()
	t.Run("disabled", func(t *testing.T) {
		d, tg, _, _, _ := syncFixture(t)
		d.background = newBackgroundSync(d, BackgroundSyncConfig{}, nil, nil)
		_, err := d.historySyncCatchup(ctx, params(t, rpc.HistorySyncCatchupParams{DryRun: true}))
		if rpcCode(t, err) != rpc.CodeInvalidParams || !strings.Contains(err.Error(), "catchup.enabled") {
			t.Fatalf("error: %v", err)
		}
		d.opts.CatchupEnabled = true
		d.background = nil
		_, err = d.historySyncCatchup(ctx, nil)
		if rpcCode(t, err) != rpc.CodeInvalidParams || !strings.Contains(err.Error(), "background_sync.enabled") {
			t.Fatalf("error: %v", err)
		}
		tg.mu.Lock()
		defer tg.mu.Unlock()
		if tg.chatsCalls != 0 || d.sync.hasActive() {
			t.Fatal("disabled catchup made requests")
		}
	})
	t.Run("no downtime", func(t *testing.T) {
		d, tg, _, _, _ := syncFixture(t)
		d.opts.CatchupEnabled = true
		d.background = newBackgroundSync(d, BackgroundSyncConfig{}, nil, nil)
		v, err := d.historySyncCatchup(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := v.(rpc.HistorySyncCatchupResult)
		if r.Since != nil || len(r.Chats) != 0 {
			t.Fatalf("result: %+v", r)
		}
		tg.mu.Lock()
		defer tg.mu.Unlock()
		if tg.chatsCalls != 0 {
			t.Fatal("read dialogs")
		}
	})
	t.Run("no login", func(t *testing.T) {
		d, tg, _, _, _ := syncFixture(t)
		d.opts.CatchupEnabled = true
		d.background = newBackgroundSync(d, BackgroundSyncConfig{}, nil, nil)
		tg.mu.Lock()
		tg.user = nil
		tg.mu.Unlock()
		_, err := d.historySyncCatchup(ctx, nil)
		if rpcCode(t, err) != rpc.CodeNotAuthorized {
			t.Fatalf("error: %v", err)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		d, tg, db, _, _ := syncFixture(t)
		d.opts.CatchupEnabled = true
		d.background = newBackgroundSync(d, BackgroundSyncConfig{}, nil, nil)
		since := time.Unix(100, 0)
		d.catchupSince = &since
		tg.chats = []rpc.Chat{{ID: 42, Title: "Test", LastMessageDate: 200}}
		if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 1, Date: 1, Raw: []byte(`{}`)}}, nil, false, store.GapPass{}); err != nil {
			t.Fatal(err)
		}
		v, err := d.historySyncCatchup(ctx, params(t, rpc.HistorySyncCatchupParams{DryRun: true}))
		if err != nil {
			t.Fatal(err)
		}
		r := v.(rpc.HistorySyncCatchupResult)
		if r.Since == nil || *r.Since != 100 || r.WouldQueue != 1 || len(r.Chats) != 1 || r.Chats[0].Action != rpc.HistorySyncActionWouldQueue || d.sync.hasActive() {
			t.Fatalf("result: %+v", r)
		}
	})
	t.Run("queue", func(t *testing.T) {
		d, tg, db, clock, _ := syncFixture(t)
		d.opts.CatchupEnabled = true
		d.background = newBackgroundSync(d, BackgroundSyncConfig{PagePauseMin: time.Second, PagePauseMax: time.Second}, nil, nil)
		since := time.Unix(100, 0)
		d.catchupSince = &since
		tg.chats = []rpc.Chat{{ID: 42, Title: "Test", LastMessageDate: 200}}
		if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 1, Date: 1, Raw: []byte(`{}`)}}, nil, false, store.GapPass{}); err != nil {
			t.Fatal(err)
		}
		v, err := d.historySyncCatchup(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := v.(rpc.HistorySyncCatchupResult)
		if r.Queued != 1 || len(r.Chats) != 1 || r.Chats[0].Action != rpc.HistorySyncActionQueued {
			t.Fatalf("result: %+v", r)
		}
		clock.awaitPause(t)
	})
}

func TestCatchupRPCDisabledBeforeClientAdmission(t *testing.T) {
	d, client, _ := settingsFixture(t)
	d.opts.CatchupEnabled = true
	d.background = nil
	d.opts.NewTelegram = func(context.Context, TelegramConfig) (Telegram, error) { return nil, errors.New("unexpected client") }
	var result rpc.HistorySyncCatchupResult
	err := client.Call(context.Background(), rpc.MethodHistorySyncCatchup, rpc.HistorySyncCatchupParams{DryRun: true}, &result)
	if rpcCode(t, err) != rpc.CodeInvalidParams || !strings.Contains(err.Error(), "background_sync.enabled") {
		t.Fatalf("error: %v", err)
	}
}
