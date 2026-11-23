package daemon

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func gapFixture(t *testing.T, complete bool) (*daemon, *syncTelegram, *store.DB, *syncTestClock) {
	t.Helper()
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 10, Date: 10, Raw: []byte(`{}`)}}, nil, complete, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCatchupGap(ctx, 42, 500, 100); err != nil {
		t.Fatal(err)
	}
	return d, tg, db, clock
}
func gapStart(t *testing.T, d *daemon, db *store.DB, depth historyDepth) rpc.HistorySyncJob {
	t.Helper()
	s, _, err := db.SyncState(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	job, err := d.sync.startDepth(42, syncRef(42), s, depth, false)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func TestHistorySyncGapComplete(t *testing.T) {
	d, tg, _, _ := gapFixture(t, true)
	calls := make(chan int, 1)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		calls <- offset
		return syncPage(42, 20, 10), nil
	}
	response, err := d.historySyncStart(context.Background(), params(t, rpc.HistorySyncParams{Chat: "42"}))
	if err != nil {
		t.Fatal(err)
	}
	job := response.(rpc.HistorySyncJob)
	if job.State != rpc.HistorySyncQueued || job.GapTo == nil || *job.GapTo != 500 {
		t.Fatalf("start: %+v", job)
	}
	if offset := <-calls; offset != 500 {
		t.Fatalf("offset=%d", offset)
	}
	job, err = d.sync.waitJob(context.Background(), 42)
	if err != nil || job.State != rpc.HistorySyncDone || !job.Complete || job.GapTo != nil || job.GapFrom != nil {
		t.Fatalf("job: %+v %v", job, err)
	}
}
func TestHistorySyncGapContinuesFromOldest(t *testing.T) {
	d, tg, db, clock := gapFixture(t, false)
	calls := make(chan int, 2)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		calls <- offset
		if offset == 500 {
			return syncPage(42, 10), nil
		}
		return syncPage(42), nil
	}
	gapStart(t, d, db, historyDepth{})
	if got := <-calls; got != 500 {
		t.Fatal(got)
	}
	clock.awaitPause(t)
	clock.Advance(historySyncPause)
	if got := <-calls; got != 10 {
		t.Fatalf("continuation offset=%d", got)
	}
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || !job.Complete || job.GapFrom != nil {
		t.Fatalf("job: %+v %v", job, err)
	}
}
func TestHistorySyncGapDepthAndResume(t *testing.T) {
	d, tg, db, clock := gapFixture(t, false)
	var mu sync.Mutex
	var offsets []int
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		mu.Lock()
		offsets = append(offsets, offset)
		mu.Unlock()
		ids := make([]int, 100)
		for i := range ids {
			ids[i] = offset - i - 1
		}
		return syncPage(42, ids...), nil
	}
	job := gapStart(t, d, db, historyDepth{Messages: 150})
	if job.State != rpc.HistorySyncQueued {
		t.Fatalf("start: %+v", job)
	}
	clock.awaitPause(t)
	status := d.sync.snapshot(42)
	if status.GapTo == nil || *status.GapTo != 500 {
		t.Fatalf("running status: %+v", status)
	}
	clock.Advance(historySyncPause)
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || !job.DepthReached || job.Loaded != 200 || job.GapTo == nil || *job.GapTo != 300 || *job.GapFrom != 10 || *job.NewestID != 10 {
		t.Fatalf("narrow: %+v %v", job, err)
	}
	gapStart(t, d, db, historyDepth{Messages: 1})
	job, err = d.sync.waitJob(context.Background(), 42)
	if err != nil || job.GapTo == nil || *job.GapTo != 200 {
		t.Fatalf("second: %+v %v", job, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offsets) != 3 || offsets[0] != 500 || offsets[1] != 400 || offsets[2] != 300 {
		t.Fatalf("offsets: %v", offsets)
	}
}
func TestHistorySyncGapCancelPreservesBounds(t *testing.T) {
	d, tg, db, clock := gapFixture(t, false)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		return syncPage(42, 400), nil
	}
	gapStart(t, d, db, historyDepth{})
	clock.awaitPause(t)
	d.sync.cancelJob(42)
	awaitCond(t, "cancelled gap worker", func() bool {
		d.sync.mu.Lock()
		defer d.sync.mu.Unlock()
		return d.sync.current == nil
	})
	s, _, err := db.SyncState(context.Background(), 42)
	if err != nil || *s.GapFrom != 10 || *s.GapTo != 500 || *s.NewestID != 10 {
		t.Fatalf("gap: %+v %v", s, err)
	}
	calls := make(chan int, 1)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		calls <- offset
		return syncPage(42, 10), nil
	}
	gapStart(t, d, db, historyDepth{})
	if got := <-calls; got != 500 {
		t.Fatalf("restart=%d", got)
	}
}

func TestHistorySyncGapSince(t *testing.T) {
	d, tg, db, _ := gapFixture(t, false)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		ids := make([]int, 100)
		for i := range ids {
			ids[i] = offset - i - 1
		}
		return syncPage(42, ids...), nil
	}
	gapStart(t, d, db, historyDepth{Since: 1400})
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || !job.DepthReached || job.GapTo == nil || *job.GapTo != 400 {
		t.Fatalf("since: %+v %v", job, err)
	}
}

func TestHistorySyncGapMessageLimitContinues(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			d, tg, db, clock, _ := syncFixture(t)
			ctx := context.Background()
			setHistoryLimit(d, "", 5)
			if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 6000, Date: 1, Raw: []byte(`{}`)}}, nil, complete, store.GapPass{}); err != nil {
				t.Fatal(err)
			}
			if err := db.MarkCatchupGap(ctx, 42, 6300, 100); err != nil {
				t.Fatal(err)
			}
			tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
				ids := make([]int, 100)
				for i := range ids {
					ids[i] = offset - i - 1
				}
				return syncPage(42, ids...), nil
			}
			gapStart(t, d, db, historyDepth{Messages: 5000})
			pages := 50
			if complete {
				pages = 3
			}
			for i := 1; i < pages; i++ {
				clock.awaitPause(t)
				clock.Advance(historySyncPause)
			}
			job, err := d.sync.waitJob(ctx, 42)
			if err != nil || job.State != rpc.HistorySyncDone || job.GapTo != nil {
				t.Fatalf("job: %+v %v", job, err)
			}
			if complete && (job.Loaded != 300 || !job.Complete) || !complete && (job.Loaded != 5000 || !job.DepthReached || job.Complete) {
				t.Fatalf("count: %+v", job)
			}
		})
	}
}

func TestHistorySyncGapIgnoresHistoryLimitUntilClosed(t *testing.T) {
	d, tg, db, clock := gapFixture(t, false)
	setHistoryLimit(d, "2022-02-02", 0)
	calls := make(chan int, 2)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		calls <- offset
		if offset == 500 {
			return syncPage(42, 400), nil
		}
		return syncPage(42, 10), nil
	}
	gapStart(t, d, db, historyDepth{})
	if got := <-calls; got != 500 {
		t.Fatal(got)
	}
	clock.awaitPause(t)
	if job := d.sync.snapshot(42); job.State != rpc.HistorySyncRunning || job.GapTo == nil {
		t.Fatalf("stopped before closure: %+v", job)
	}
	clock.Advance(historySyncPause)
	if got := <-calls; got != 400 {
		t.Fatalf("gap interrupted: %d", got)
	}
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || !job.Complete || job.GapTo != nil {
		t.Fatalf("limit after gap: %+v %v", job, err)
	}
}
func TestHistorySyncGapSinceUsesStoredBoundsAfterClosure(t *testing.T) {
	d, tg, db, _ := gapFixture(t, false)
	calls := 0
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		calls++
		return syncPage(42, 9), nil
	}
	gapStart(t, d, db, historyDepth{Since: 500})
	job, err := d.sync.waitJob(context.Background(), 42)
	if err != nil || !job.DepthReached || job.GapTo != nil || calls != 1 {
		t.Fatalf("job: %+v calls=%d err=%v", job, calls, err)
	}
}

func TestHistorySyncGapStartWithExistingDepth(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	old := make([]store.Message, 200)
	for i := range old {
		old[i] = store.Message{ChatID: 42, ID: i + 1, Date: 1, Raw: []byte(`{}`)}
	}
	if _, err := db.SaveHistoryPage(ctx, 42, old, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCatchupGap(ctx, 42, 700, 100); err != nil {
		t.Fatal(err)
	}
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		ids := make([]int, 100)
		for i := range ids {
			ids[i] = offset - i - 1
		}
		return syncPage(42, ids...), nil
	}
	response, err := d.historySyncStart(ctx, params(t, rpc.HistorySyncParams{Chat: "42", Messages: 150}))
	if err != nil {
		t.Fatal(err)
	}
	if job := response.(rpc.HistorySyncJob); job.State != rpc.HistorySyncQueued || job.GapTo == nil || *job.GapTo != 700 {
		t.Fatalf("start: %+v", job)
	}
	clock.awaitPause(t)
	clock.Advance(historySyncPause)
	job, err := d.sync.waitJob(ctx, 42)
	if err != nil || job.Loaded != 200 || !job.DepthReached || job.GapTo == nil || *job.GapTo != 500 {
		t.Fatalf("job: %+v %v", job, err)
	}
}

func TestHistorySyncGapWithoutOldestContinues(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	if err := db.SaveCatchupPage(ctx, 42, []store.Message{{ChatID: 42, ID: 10, Date: 10, Raw: []byte(`{}`)}, {ChatID: 42, ID: 500, Date: 500, Raw: []byte(`{}`)}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCatchupGap(ctx, 42, 500, 100); err != nil {
		t.Fatal(err)
	}
	calls := make(chan int, 2)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		calls <- offset
		if offset == 500 {
			return syncPage(42, 10), nil
		}
		return syncPage(42), nil
	}
	gapStart(t, d, db, historyDepth{})
	if got := <-calls; got != 500 {
		t.Fatal(got)
	}
	clock.awaitPause(t)
	clock.Advance(historySyncPause)
	if got := <-calls; got != 10 {
		t.Fatalf("continuation: %d", got)
	}
	job, err := d.sync.waitJob(ctx, 42)
	if err != nil || !job.Complete || job.GapTo != nil {
		t.Fatalf("job: %+v %v", job, err)
	}
}
