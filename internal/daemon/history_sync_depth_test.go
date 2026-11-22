package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// depthPages answers the pages of a chat one after another and records the offset of each request.
type depthPages struct {
	mu      sync.Mutex
	pages   []rpc.HistoryPage
	offsets []int
}

func (p *depthPages) fetch(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offsets = append(p.offsets, offset)
	if len(p.offsets) > len(p.pages) {
		return rpc.HistoryPage{Empty: true}, nil
	}
	return p.pages[len(p.offsets)-1], nil
}

func (p *depthPages) requests() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.offsets...)
}

func startDepthSync(t *testing.T, d *daemon, chatID int64, messages int, since string) (rpc.HistorySyncJob, error) {
	t.Helper()
	result, err := d.historySyncStart(context.Background(), params(t, rpc.HistorySyncParams{Chat: strconv.FormatInt(chatID, 10), Messages: messages, Since: since}))
	if err != nil {
		return rpc.HistorySyncJob{}, err
	}
	return result.(rpc.HistorySyncJob), nil
}

// finishPages lets the pause timers of a job fire until it reaches state.
func finishPages(t *testing.T, d *daemon, clock *syncTestClock, chatID int64, state string) rpc.HistorySyncJob {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if job := d.sync.snapshot(chatID); job != nil && job.State == state {
			return *job
		}
		select {
		case <-clock.created:
			clock.Advance(historySyncPause)
		case <-deadline:
			t.Fatalf("job %d did not reach %s: %+v", chatID, state, d.sync.snapshot(chatID))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHistorySyncDepthMessagesStopsAtPageBoundaryAndResumes(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	pages := &depthPages{pages: []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42, 9, 8, 7), syncPage(42, 6, 5, 4), syncPage(42)}}
	tg.page = pages.fetch
	if job, err := startDepthSync(t, d, 42, 5, ""); err != nil || job.Messages != 5 {
		t.Fatalf("start: %+v err=%v", job, err)
	}
	job := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	// Three messages are fewer than five, six are enough: the limit is met on the second page.
	if job.Pages != 2 || job.Loaded != 6 || !job.DepthReached || job.Complete || job.Messages != 5 {
		t.Fatalf("stopped job: %+v", job)
	}
	state, found, err := db.SyncState(context.Background(), 42)
	if err != nil || !found || state.Complete || state.OldestID == nil || *state.OldestID != 7 {
		t.Fatalf("saved progress: %+v found=%v err=%v", state, found, err)
	}
	if got := pages.requests(); len(got) != 2 {
		t.Fatalf("requests=%v, want two", got)
	}
	// A later start without a limit goes on from the oldest saved message and completes the chat.
	next, err := startDepthSync(t, d, 42, 0, "")
	if err != nil || next.State != rpc.HistorySyncQueued || next.DepthReached || next.Messages != 0 {
		t.Fatalf("full start: %+v err=%v", next, err)
	}
	done := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	if !done.Complete || done.DepthReached {
		t.Fatalf("completed job: %+v", done)
	}
	if got := pages.requests(); len(got) < 3 || got[2] != 7 {
		t.Fatalf("the full load must resume from offset 7: %v", got)
	}
}

func TestHistorySyncDepthSinceStopsOnceOldestMessageIsOldEnough(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	clock.Advance(3 * time.Hour) // the start refuses a time in the future
	pages := &depthPages{pages: []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42, 9, 8, 7), syncPage(42, 6, 5, 4)}}
	tg.page = pages.fetch
	// Message n is dated 1000+n: 1008 lies inside the second page (1009, 1008, 1007).
	since := time.Unix(1008, 0).UTC().Format(time.RFC3339)
	if job, err := startDepthSync(t, d, 42, 0, since); err != nil || job.Since != since {
		t.Fatalf("start: %+v err=%v", job, err)
	}
	job := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	if job.Pages != 2 || !job.DepthReached || job.Complete || job.Since != since {
		t.Fatalf("stopped job: %+v", job)
	}
	if state, _, err := db.SyncState(context.Background(), 42); err != nil || state.Complete {
		t.Fatalf("a depth stop must not complete the chat: %+v err=%v", state, err)
	}
}

func TestHistorySyncDepthAlreadyMetAtStartMakesNoRequest(t *testing.T) {
	d, tg, db, _, _ := syncFixture(t)
	pages := &depthPages{}
	tg.page = pages.fetch
	messages := []store.Message{
		{ChatID: 42, ID: 7, Date: 1007, Raw: []byte(`{}`)}, {ChatID: 42, ID: 8, Date: 1008, Raw: []byte(`{}`)}, {ChatID: 42, ID: 9, Date: 1009, Raw: []byte(`{}`)},
	}
	if _, err := db.SaveHistoryPage(context.Background(), 42, messages, nil, false); err != nil {
		t.Fatal(err)
	}
	job, err := startDepthSync(t, d, 42, 3, "")
	if err != nil || job.State != rpc.HistorySyncDone || !job.DepthReached || job.Pages != 0 || job.Complete {
		t.Fatalf("start: %+v err=%v", job, err)
	}
	if got := pages.requests(); len(got) != 0 {
		t.Fatalf("Telegram was asked: %v", got)
	}
	// Four are not enough: the same chat starts a real load.
	if more, err := startDepthSync(t, d, 42, 4, ""); err != nil || more.State != rpc.HistorySyncQueued || more.DepthReached {
		t.Fatalf("a deeper limit must load: %+v err=%v", more, err)
	}
}

func TestHistorySyncDepthMetStartLeavesTheBackgroundLoadAlone(t *testing.T) {
	d, tg, db, _, _ := syncFixture(t)
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	message := store.Message{ChatID: 42, ID: 7, Date: 1007, Raw: []byte(`{}`)}
	if _, err := db.SaveHistoryPage(context.Background(), 42, []store.Message{message}, nil, false); err != nil {
		t.Fatal(err)
	}
	d.sync.mu.Lock()
	d.sync.enqueueLocked(20, syncRef(20), store.SyncProgress{}, rpc.HistorySyncBackground, nil, historyDepth{}, false)
	d.sync.mu.Unlock()
	if job, err := startDepthSync(t, d, 42, 1, ""); err != nil || !job.DepthReached {
		t.Fatalf("start: %+v err=%v", job, err)
	}
	if job := d.sync.snapshot(20); job == nil || job.State == rpc.HistorySyncCancelled {
		t.Fatalf("a start that has nothing to load must not cancel the background job: %+v", job)
	}
	// A start that does load takes the place of the background job.
	if job, err := startDepthSync(t, d, 43, 0, ""); err != nil || job.State != rpc.HistorySyncQueued {
		t.Fatalf("manual start: %+v err=%v", job, err)
	}
	if job := d.sync.snapshot(20); job == nil || job.State != rpc.HistorySyncCancelled {
		t.Fatalf("a manual load must cancel the background job: %+v", job)
	}
}

func TestHistorySyncDepthParametersAreChecked(t *testing.T) {
	d, tg, _, clock, _ := syncFixture(t)
	pages := &depthPages{}
	tg.page = pages.fetch
	clock.Advance(time.Hour)
	now := clock.Now()
	for name, tc := range map[string]struct {
		messages int
		since    string
	}{
		"both limits":   {10, now.Add(-time.Minute).UTC().Format(time.RFC3339)},
		"too many":      {maxHistoryDepthMessages + 1, ""},
		"negative":      {-1, ""},
		"not a time":    {0, "yesterday"},
		"in the future": {0, now.Add(time.Hour).UTC().Format(time.RFC3339)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := startDepthSync(t, d, 42, tc.messages, tc.since)
			rpcErr, ok := err.(*rpc.Error)
			if !ok || rpcErr.Code != rpc.CodeInvalidParams {
				t.Fatalf("err=%v, want invalid params", err)
			}
			if job := d.sync.snapshot(42); job != nil {
				t.Fatalf("a rejected start left a job: %+v", job)
			}
		})
	}
	if got := pages.requests(); len(got) != 0 {
		t.Fatalf("Telegram was asked: %v", got)
	}
}

func TestHistorySyncCancelAllDropsQueueAndRunningJob(t *testing.T) {
	d, tg, _, _, _ := syncFixture(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{}, 4)
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return rpc.HistoryPage{}, ctx.Err()
		case <-release:
			return rpc.HistoryPage{Empty: true}, nil
		}
	}
	for _, id := range []int64{41, 42, 43} {
		startSync(t, d, id)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no job started")
	}
	result, err := d.historySyncCancelAll(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(rpc.HistorySyncCancelAllResult); got.Cancelled != 3 {
		t.Fatalf("cancelled=%d, want 3 (one running and two queued)", got.Cancelled)
	}
	for _, id := range []int64{41, 42, 43} {
		if job := d.sync.snapshot(id); job == nil || job.State != rpc.HistorySyncCancelled {
			t.Fatalf("job %d: %+v", id, job)
		}
	}
	again, err := d.historySyncCancelAll(context.Background(), nil)
	if err != nil || again.(rpc.HistorySyncCancelAllResult).Cancelled != 0 {
		t.Fatalf("a second cancel has nothing to cancel: %+v err=%v", again, err)
	}
}

// queueFixture is a daemon whose dialogs are six chats; the clock stands 100 days after the epoch.
func queueFixture(t *testing.T) (*daemon, *syncTelegram, *store.DB, *syncTestClock) {
	t.Helper()
	d, tg, db, clock, _ := syncFixture(t)
	clock.Advance(100 * 24 * time.Hour)
	now := clock.Now().Unix()
	day := int64(24 * 60 * 60)
	tg.mu.Lock()
	tg.chats = []rpc.Chat{
		{ID: 5, Type: "user", Title: "Pinned old", LastMessageDate: now - 200*day, Pinned: true},
		{ID: 1, Type: "user", Title: "Anna", LastMessageDate: now - 1*day},
		{ID: 4, Type: "group", Title: "Team", LastMessageDate: now - 5*day},
		{ID: 2, Type: "user", Title: "Boris", LastMessageDate: now - 10*day},
		{ID: 3, Type: "user", Title: "Carol", LastMessageDate: now - 70*day},
		{ID: 6, Type: "user", Title: "Dmitry", LastMessageDate: now - 80*day},
	}
	tg.mu.Unlock()
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	return d, tg, db, clock
}

func runQueue(t *testing.T, d *daemon, p rpc.HistorySyncQueueParams) (rpc.HistorySyncQueueResult, error) {
	t.Helper()
	result, err := d.historySyncQueue(context.Background(), params(t, p))
	if err != nil {
		return rpc.HistorySyncQueueResult{}, err
	}
	return result.(rpc.HistorySyncQueueResult), nil
}

func queueIDs(result rpc.HistorySyncQueueResult) []int64 {
	ids := make([]int64, 0, len(result.Chats))
	for _, chat := range result.Chats {
		ids = append(ids, chat.ChatID)
	}
	return ids
}

func TestHistorySyncQueueDryRunPicksNewestActiveChatsOfOneType(t *testing.T) {
	d, tg, _, _ := queueFixture(t)
	result, err := runQueue(t, d, rpc.HistorySyncQueueParams{Type: "user", ActiveDays: 60, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	// The pinned dialog is old but leads the list: it is skipped, and the walk goes on. The
	// group is another type. Carol is 70 days old, so the walk ends before her.
	if ids := queueIDs(result); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("picked %v, want [1 2]: %+v", ids, result)
	}
	if !result.DryRun || result.WouldQueue != 2 || result.Queued != 0 {
		t.Fatalf("counters: %+v", result)
	}
	for _, chat := range result.Chats {
		if chat.Action != rpc.HistorySyncActionWouldQueue || chat.Title == "" || chat.LastMessageDate == 0 {
			t.Fatalf("entry: %+v", chat)
		}
	}
	for _, id := range []int64{1, 2} {
		if d.sync.snapshot(id) != nil {
			t.Fatalf("a dry run queued chat %d", id)
		}
	}
	tg.mu.Lock()
	gotType := tg.lastType
	tg.mu.Unlock()
	if gotType != "user" {
		t.Fatalf("type filter passed to Telegram = %q", gotType)
	}
}

func TestHistorySyncQueueStartsJobsUpToMaxChats(t *testing.T) {
	d, _, _, _ := queueFixture(t)
	result, err := runQueue(t, d, rpc.HistorySyncQueueParams{Type: "user", MaxChats: 2, Messages: 100})
	if err != nil {
		t.Fatal(err)
	}
	// Without an activity window the pinned chat comes first.
	if ids := queueIDs(result); len(ids) != 2 || ids[0] != 5 || ids[1] != 1 || result.Queued != 2 || result.WouldQueue != 0 {
		t.Fatalf("result: %+v", result)
	}
	for _, id := range []int64{5, 1} {
		job := d.sync.snapshot(id)
		if job == nil || job.Source != rpc.HistorySyncManual || job.Messages != 100 {
			t.Fatalf("job %d: %+v", id, job)
		}
	}
	if d.sync.snapshot(2) != nil {
		t.Fatal("a chat beyond max_chats was queued")
	}
}

func TestHistorySyncQueueSkipsLoadedActiveAndPartlyLoadedChats(t *testing.T) {
	d, _, db, _ := queueFixture(t)
	ctx := context.Background()
	// Chat 1 is loaded completely, chat 2 partly, chat 4 has a job already.
	if _, err := db.SaveHistoryPage(ctx, 1, []store.Message{{ChatID: 1, ID: 9, Date: 1009, Raw: []byte(`{}`)}}, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 2, []store.Message{{ChatID: 2, ID: 9, Date: 1009, Raw: []byte(`{}`)}}, nil, false); err != nil {
		t.Fatal(err)
	}
	startSync(t, d, 4)
	all, err := runQueue(t, d, rpc.HistorySyncQueueParams{ActiveDays: 60, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	// 1 is complete, 4 is active; the partly loaded 2 continues; 3 is 70 days old.
	if ids := queueIDs(all); len(ids) != 1 || ids[0] != 2 || all.SkippedLoaded != 1 || all.SkippedActive != 1 {
		t.Fatalf("default queue: %+v", all)
	}
	never, err := runQueue(t, d, rpc.HistorySyncQueueParams{ActiveDays: 60, Unsynced: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	// --unsynced leaves the partly loaded chat for its own load.
	if len(never.Chats) != 0 || never.SkippedLoaded != 2 {
		t.Fatalf("unsynced queue: %+v", never)
	}
}

func TestHistorySyncQueueDepthMetChatsAreSkipped(t *testing.T) {
	d, _, db, _ := queueFixture(t)
	ctx := context.Background()
	var saved []store.Message
	for id := 1; id <= 3; id++ {
		saved = append(saved, store.Message{ChatID: 1, ID: id, Date: int64(1000 + id), Raw: []byte(`{}`)})
	}
	if _, err := db.SaveHistoryPage(ctx, 1, saved, nil, false); err != nil {
		t.Fatal(err)
	}
	result, err := runQueue(t, d, rpc.HistorySyncQueueParams{Type: "user", ActiveDays: 30, Messages: 3, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if ids := queueIDs(result); len(ids) != 1 || ids[0] != 2 || result.SkippedLoaded != 1 {
		t.Fatalf("result: %+v", result)
	}
}

func TestHistorySyncQueueNeverShowsOrQueuesAClosedChat(t *testing.T) {
	d, _, db, _ := queueFixture(t)
	ctx := context.Background()
	if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.block": params(t, []int{2})}); err != nil {
		t.Fatal(err)
	}
	d.opts.Access = nil
	d.refreshAccess(ctx)
	for _, dry := range []bool{true, false} {
		result, err := runQueue(t, d, rpc.HistorySyncQueueParams{Type: "user", ActiveDays: 60, DryRun: dry})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "Boris") || strings.Contains(string(raw), `"chat_id":2`) {
			t.Fatalf("the closed chat shows in the answer: %s", raw)
		}
		if ids := queueIDs(result); len(ids) != 1 || ids[0] != 1 {
			t.Fatalf("dry=%v picked %v", dry, ids)
		}
	}
	if d.sync.snapshot(2) != nil {
		t.Fatal("a closed chat was queued")
	}
}

func TestHistorySyncQueueRejectsBadParameters(t *testing.T) {
	d, tg, _, _ := queueFixture(t)
	for name, p := range map[string]rpc.HistorySyncQueueParams{
		"type":           {Type: "person"},
		"active days":    {ActiveDays: -1},
		"too long":       {ActiveDays: maxQueueActive + 1},
		"max chats low":  {MaxChats: -1},
		"max chats high": {MaxChats: maxQueueChats + 1},
		"both depths":    {Messages: 5, Since: "2026-01-01T00:00:00Z"},
		"bad depth":      {Messages: -2},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runQueue(t, d, p)
			rpcErr, ok := err.(*rpc.Error)
			if !ok || rpcErr.Code != rpc.CodeInvalidParams {
				t.Fatalf("err=%v, want invalid params", err)
			}
		})
	}
	tg.mu.Lock()
	calls := tg.chatsCalls
	tg.mu.Unlock()
	if calls != 0 {
		t.Fatalf("a rejected request read the dialog list %d times", calls)
	}
}

func TestHistoryLimitReached(t *testing.T) {
	since := time.Date(2022, 2, 2, 0, 0, 0, 0, time.UTC).Unix()
	tests := []struct {
		name   string
		limit  historyLimit
		count  int
		oldest int64
		want   bool
	}{
		{"both off", historyLimit{}, 1000000, 1, false},
		{"count below", historyLimit{Messages: 200000}, 199999, since + 1, false},
		{"count reached", historyLimit{Messages: 200000}, 200000, since + 1, true},
		{"date not reached", historyLimit{Since: since}, 10, since + 1, false},
		{"date reached", historyLimit{Since: since}, 10, since, true},
		{"date older", historyLimit{Since: since}, 10, since - 1, true},
		{"no messages never reaches the date", historyLimit{Since: since}, 0, 0, false},
		{"either is enough", historyLimit{Messages: 5, Since: since}, 5, since + 100, true},
	}
	for _, tt := range tests {
		if got := tt.limit.reached(tt.count, tt.oldest); got != tt.want {
			t.Errorf("%s: reached=%v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestHistoryLimitDefaultsAndSettings(t *testing.T) {
	d, _, _, _, _ := syncFixture(t)
	d.liveMu.Lock()
	d.liveValues = nil
	d.liveMu.Unlock()
	got := d.historyLimit()
	if got.Messages != 200000 || got.Since != time.Date(2022, 2, 2, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("defaults: %+v", got)
	}
	setHistoryLimit(d, "2020-01-01", 50)
	got = d.historyLimit()
	if got.Messages != 50 || got.Since != time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("set: %+v", got)
	}
	setHistoryLimit(d, "", 0)
	if got = d.historyLimit(); got != (historyLimit{}) {
		t.Fatalf("off: %+v", got)
	}
}

func TestHistorySyncFullLoadStopsAtMessageLimitAndCompletesTheChat(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	setHistoryLimit(d, "", 5)
	pages := &depthPages{pages: []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42, 9, 8, 7), syncPage(42, 6, 5, 4), syncPage(42)}}
	tg.page = pages.fetch
	startSync(t, d, 42)
	job := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	if job.Pages != 2 || !job.Complete || job.DepthReached || job.FirstMessageDate != nil {
		t.Fatalf("stopped job: %+v", job)
	}
	state, found, err := db.SyncState(context.Background(), 42)
	if err != nil || !found || !state.Complete || state.FirstMessageDate != nil || state.OldestID == nil || *state.OldestID != 7 {
		t.Fatalf("saved progress: %+v found=%v err=%v", state, found, err)
	}
	if got := pages.requests(); len(got) != 2 {
		t.Fatalf("requests=%v, want two", got)
	}
	// The chat is complete now: a new start makes no request.
	if next := startSync(t, d, 42); next.State != rpc.HistorySyncDone || len(pages.requests()) != 2 {
		t.Fatalf("restart: %+v requests=%v", next, pages.requests())
	}
}

func datedPage(chatID int64, date time.Time, ids ...int) rpc.HistoryPage {
	page := syncPage(chatID, ids...)
	for i := range page.Messages {
		page.Messages[i].Date = date.Format(time.RFC3339)
	}
	return page
}

func TestHistorySyncFullLoadStopsAtDateLimit(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	setHistoryLimit(d, "2022-02-02", 0)
	pages := &depthPages{pages: []rpc.HistoryPage{
		datedPage(42, time.Date(2022, 3, 1, 12, 0, 0, 0, time.UTC), 12, 11, 10),
		// A message of the limit day itself, after 00:00 UTC, does not reach the limit.
		datedPage(42, time.Date(2022, 2, 2, 12, 0, 0, 0, time.UTC), 9, 8, 7),
		datedPage(42, time.Date(2022, 2, 1, 12, 0, 0, 0, time.UTC), 6, 5, 4),
		syncPage(42),
	}}
	tg.page = pages.fetch
	startSync(t, d, 42)
	job := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	if job.Pages != 3 || !job.Complete || job.FirstMessageDate != nil {
		t.Fatalf("job: %+v", job)
	}
	state, _, err := db.SyncState(context.Background(), 42)
	if err != nil || !state.Complete || state.FirstMessageDate != nil || state.OldestID == nil || *state.OldestID != 4 {
		t.Fatalf("state: %+v err=%v", state, err)
	}
	if got := pages.requests(); len(got) != 3 {
		t.Fatalf("requests=%v, want three", got)
	}
}

func TestHistorySyncDepthLimitedLoadIgnoresTheFullLimit(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	setHistoryLimit(d, "", 2)
	pages := &depthPages{pages: []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42, 9, 8, 7), syncPage(42)}}
	tg.page = pages.fetch
	if _, err := startDepthSync(t, d, 42, 4, ""); err != nil {
		t.Fatal(err)
	}
	job := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	if job.Pages != 2 || !job.DepthReached || job.Complete {
		t.Fatalf("job: %+v", job)
	}
	state, _, err := db.SyncState(context.Background(), 42)
	if err != nil || state.Complete {
		t.Fatalf("state: %+v err=%v", state, err)
	}
}
