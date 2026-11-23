package daemon

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// schedClock is the clock of the scheduler tests: time moves only when a test
// moves it, and a timer fires when the clock reaches its deadline.
type schedClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*schedTimer]struct{}
}

type schedTimer struct {
	clock    *schedClock
	deadline time.Time
	ch       chan time.Time
}

func newSchedClock(now time.Time) *schedClock {
	return &schedClock{now: now, timers: make(map[*schedTimer]struct{})}
}

func (c *schedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *schedClock) NewTimer(d time.Duration) historySyncTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &schedTimer{clock: c, deadline: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		timer.ch <- c.now
		return timer
	}
	c.timers[timer] = struct{}{}
	return timer
}

func (t *schedTimer) C() <-chan time.Time { return t.ch }
func (t *schedTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	return active
}

// AdvanceTo moves the clock and fires the timers whose deadline has come.
func (c *schedClock) AdvanceTo(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
	for timer := range c.timers {
		if !timer.deadline.After(now) {
			timer.ch <- now
			delete(c.timers, timer)
		}
	}
}

func (c *schedClock) hasTimerAt(deadline time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for timer := range c.timers {
		if timer.deadline.Equal(deadline) {
			return true
		}
	}
	return false
}

// awaitCond polls until cond holds. The 2 second bound is wall time spent only
// when a test fails.
func awaitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		default:
			runtime.Gosched()
		}
	}
}

// schedRandom gives the random function scripted answers per range and the
// lower bound of the range when the script is empty.
type schedRandom struct {
	mu    sync.Mutex
	queue map[[2]time.Duration][]time.Duration
	calls map[[2]time.Duration]int
}

func newSchedRandom() *schedRandom {
	return &schedRandom{queue: make(map[[2]time.Duration][]time.Duration), calls: make(map[[2]time.Duration]int)}
}

func (r *schedRandom) push(lo, hi time.Duration, values ...time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := [2]time.Duration{lo, hi}
	r.queue[key] = append(r.queue[key], values...)
}

func (r *schedRandom) fn(lo, hi time.Duration) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := [2]time.Duration{lo, hi}
	r.calls[key]++
	if q := r.queue[key]; len(q) > 0 {
		r.queue[key] = q[1:]
		return q[0]
	}
	return lo
}

func (r *schedRandom) callCount(lo, hi time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[[2]time.Duration{lo, hi}]
}

// schedTelegram counts the requests the scheduler may cause.
type schedTelegram struct {
	*syncTelegram
	cmu         sync.Mutex
	folderCalls int
	folders     rpc.FoldersResult
	foldersErr  error
	// onFolders, when set, runs inside Folders and may block until ctx ends.
	onFolders func(context.Context) error
	pages     []chatCall
	resolves  int
}

func (s *schedTelegram) Folders(ctx context.Context) (rpc.FoldersResult, error) {
	s.cmu.Lock()
	s.folderCalls++
	hook, err, folders := s.onFolders, s.foldersErr, s.folders
	s.cmu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return rpc.FoldersResult{}, err
		}
	}
	if err != nil {
		return rpc.FoldersResult{}, err
	}
	return folders, nil
}

func (s *schedTelegram) failFolders(err error) {
	s.cmu.Lock()
	s.foldersErr = err
	s.cmu.Unlock()
}

func (s *schedTelegram) HistoryPage(ctx context.Context, ref chatref.Ref, offset int) (rpc.HistoryPage, error) {
	s.cmu.Lock()
	s.pages = append(s.pages, chatCall{chat: ref.ID, offset: offset})
	s.cmu.Unlock()
	return s.syncTelegram.HistoryPage(ctx, ref, offset)
}

func (s *schedTelegram) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	s.cmu.Lock()
	s.resolves++
	s.cmu.Unlock()
	return s.syncTelegram.Resolve(ctx, ref)
}

func (s *schedTelegram) pageCalls() []chatCall {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return slices.Clone(s.pages)
}

func (s *schedTelegram) pageChats() []int64 {
	var ids []int64
	for _, call := range s.pageCalls() {
		ids = append(ids, call.chat)
	}
	return ids
}

func (s *schedTelegram) folderCount() int {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return s.folderCalls
}

var schedZone = time.FixedZone("test", 3*3600)

// schedAt is a moment of 15 January 2027 on the test clock; day shifts the date.
func schedAt(day, hour, minute int) time.Time {
	return time.Date(2027, time.January, 15+day, hour, minute, 0, 0, schedZone)
}

func schedConfig() BackgroundSyncConfig {
	return BackgroundSyncConfig{
		Enabled: true, WindowStart: 8 * time.Hour, WindowEnd: 20 * time.Hour,
		IntervalMin: 40 * time.Minute, IntervalMax: 3 * time.Hour,
		PagePauseMin: 2 * time.Second, PagePauseMax: 8 * time.Second,
	}
}

type schedEnv struct {
	t        *testing.T
	d        *daemon
	tg       *schedTelegram
	db       *store.DB
	mgrClock *syncTestClock
	clock    *schedClock
	rnd      *schedRandom
	access   string
	ctx      context.Context
	cancel   context.CancelFunc
}

// newSchedEnv builds a daemon whose history manager and Telegram are fakes. By
// default a chat page is empty, so a scheduled load completes at once.
func newSchedEnv(t *testing.T, cfg BackgroundSyncConfig, start time.Time) *schedEnv {
	t.Helper()
	d, base, db, mgrClock, accessPath := syncFixture(t)
	tg := &schedTelegram{syncTelegram: base}
	tg.page = func(_ context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		return syncPage(ref.ID), nil
	}
	d.opts.Telegram = tg
	d.opts.BackgroundSync = cfg
	d.account.set(base.user)
	ctx, cancel := context.WithCancel(context.Background())
	env := &schedEnv{t: t, d: d, tg: tg, db: db, mgrClock: mgrClock, clock: newSchedClock(start), rnd: newSchedRandom(), access: accessPath, ctx: ctx, cancel: cancel}
	// Registered after syncFixture's cleanup, so it runs first: the scheduler is
	// joined before the history manager closes.
	t.Cleanup(env.stop)
	return env
}

func (e *schedEnv) start() {
	e.d.startBackgroundSync(e.ctx, e.clock, e.rnd.fn)
}

func (e *schedEnv) stop() {
	e.cancel()
	if e.d.background != nil {
		e.d.background.wait()
	}
}

func (e *schedEnv) status() rpc.BackgroundSyncStatus { return e.d.backgroundSyncStatus() }

// settle waits until a run that started after prev has ended and the next run
// is scheduled. A nil prev means the status had no next run (it was running).
func (e *schedEnv) settle(prev *int64) rpc.BackgroundSyncStatus {
	e.t.Helper()
	var st rpc.BackgroundSyncStatus
	awaitCond(e.t, "the run to end", func() bool {
		st = e.status()
		return st.State != rpc.BackgroundSyncRunning && st.NextRunAt != nil && (prev == nil || *st.NextRunAt != *prev)
	})
	return st
}

// jump moves the clock to when and waits for the run it triggers.
func (e *schedEnv) jump(when time.Time) rpc.BackgroundSyncStatus {
	e.t.Helper()
	prev := e.status().NextRunAt
	e.clock.AdvanceTo(when)
	return e.settle(prev)
}

// step moves the clock to the next run and waits for that run. It first waits
// for the history worker to leave the previous job: a job turns done a moment
// before the manager stops counting it, and the scheduler skips a busy manager.
// A test that holds a manual job on purpose passes busy.
func (e *schedEnv) step(busy ...bool) rpc.BackgroundSyncStatus {
	e.t.Helper()
	next := e.status().NextRunAt
	if next == nil {
		e.t.Fatal("no next run is scheduled")
	}
	if len(busy) == 0 || !busy[0] {
		awaitSyncIdle(e.t, e.d)
	}
	return e.jump(time.Unix(*next, 0))
}

func (e *schedEnv) chat(id int64, chatType string, lastMessage int) {
	e.t.Helper()
	ctx := context.Background()
	if err := e.db.UpsertChats(ctx, []store.Chat{{ID: id, Type: chatType, Title: "chat"}}); err != nil {
		e.t.Fatal(err)
	}
	if lastMessage > 0 {
		message := store.Message{ChatID: id, ID: lastMessage, Date: int64(lastMessage), Raw: []byte(`{}`)}
		if _, err := e.db.SaveHistoryPage(ctx, id, []store.Message{message}, nil, false, store.GapPass{}); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *schedEnv) complete(id int64) bool {
	e.t.Helper()
	state, found, err := e.db.SyncState(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return found && state.Complete
}

func unixPtr(t time.Time) *int64 { v := t.Unix(); return &v }

func samePtr(a, b *int64) bool { return a != nil && b != nil && *a == *b }

func TestBackgroundScheduleDisabledStartsNothing(t *testing.T) {
	cfg := schedConfig()
	cfg.Enabled = false
	env := newSchedEnv(t, cfg, schedAt(0, 9, 0))
	env.chat(1, "user", 100)
	env.start()
	if env.d.background != nil {
		t.Fatal("a disabled scheduler created a scheduler")
	}
	st := env.status()
	want := rpc.BackgroundSyncStatus{Window: rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"}, State: rpc.BackgroundSyncDisabled}
	if st != want {
		t.Fatalf("status = %+v, want %+v", st, want)
	}
	env.clock.AdvanceTo(schedAt(3, 12, 0))
	if calls := env.tg.pageCalls(); len(calls) != 0 || env.tg.folderCount() != 0 || env.tg.resolves != 0 {
		t.Fatalf("a disabled scheduler called the client: pages=%v folders=%d resolves=%d", calls, env.tg.folderCount(), env.tg.resolves)
	}
	if env.rnd.callCount(cfg.IntervalMin, cfg.IntervalMax) != 0 {
		t.Fatal("a disabled scheduler drew an interval")
	}
}

func TestBackgroundScheduleNextRunMovesIntoWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from     time.Time
		interval time.Duration
		want     time.Time
	}{
		{"inside the window", schedAt(0, 9, 0), 40 * time.Minute, schedAt(0, 9, 40)},
		{"longest interval", schedAt(0, 9, 0), 3 * time.Hour, schedAt(0, 12, 0)},
		{"before the start moves to the start", schedAt(0, 5, 0), 40 * time.Minute, schedAt(0, 8, 0)},
		{"exactly the start stays", schedAt(0, 7, 20), 40 * time.Minute, schedAt(0, 8, 0)},
		{"exactly the end moves to the next day", schedAt(0, 17, 0), 3 * time.Hour, schedAt(1, 8, 0)},
		{"after the end moves to the next day", schedAt(0, 19, 30), 40 * time.Minute, schedAt(1, 8, 0)},
		{"past midnight lands before the start", schedAt(0, 23, 0), 2 * time.Hour, schedAt(1, 8, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := schedConfig()
			rnd := newSchedRandom()
			rnd.push(cfg.IntervalMin, cfg.IntervalMax, tc.interval)
			s := newBackgroundSync(nil, cfg, newSchedClock(tc.from), rnd.fn)
			if got := s.scheduleFrom(tc.from); !got.Equal(tc.want) {
				t.Fatalf("next run = %s, want %s", got, tc.want)
			}
			if rnd.callCount(cfg.IntervalMin, cfg.IntervalMax) != 1 {
				t.Fatal("the interval must come from IntervalMin..IntervalMax")
			}
		})
	}
}

// TestBackgroundScheduleNextRunKeepsWallClockAcrossClockChange checks that the
// window means 08:00 and 20:00 on the wall clock on the days the clock changes.
func TestBackgroundScheduleNextRunKeepsWallClockAcrossClockChange(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	cfg := schedConfig()
	for _, tc := range []struct {
		name string
		from time.Time
		day  time.Time
	}{
		{"clock goes forward", time.Date(2027, time.March, 27, 21, 0, 0, 0, berlin), time.Date(2027, time.March, 28, 8, 0, 0, 0, berlin)},
		{"clock goes back", time.Date(2027, time.October, 30, 21, 0, 0, 0, berlin), time.Date(2027, time.October, 31, 8, 0, 0, 0, berlin)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rnd := newSchedRandom()
			s := newBackgroundSync(nil, cfg, newSchedClock(tc.from), rnd.fn)
			got := s.scheduleFrom(tc.from)
			if !got.Equal(tc.day) || got.Hour() != 8 || got.Minute() != 0 {
				t.Fatalf("next run = %s, want %s", got, tc.day)
			}
			start, end := s.windowOf(got)
			if start.Hour() != 8 || end.Hour() != 20 {
				t.Fatalf("window = %s - %s", start, end)
			}
			if !s.inWindow(got) || s.inWindow(end) {
				t.Fatalf("window membership: start=%v end=%v", s.inWindow(got), s.inWindow(end))
			}
		})
	}
}

func TestBackgroundScheduleIntervalStaysInBounds(t *testing.T) {
	cfg := schedConfig()
	from := schedAt(0, 9, 0)
	seen := map[time.Duration]bool{}
	for range 300 {
		s := newBackgroundSync(nil, cfg, newSchedClock(from), nil)
		got := s.scheduleFrom(from)
		if got.Before(from.Add(cfg.IntervalMin)) || got.After(from.Add(cfg.IntervalMax)) || !s.inWindow(got) {
			t.Fatalf("next run %s is outside %s..%s after %s", got, cfg.IntervalMin, cfg.IntervalMax, from)
		}
		seen[got.Sub(from)] = true
	}
	if len(seen) < 2 {
		t.Fatal("the interval is not random")
	}
	for range 300 {
		if got := realBackgroundSyncRandom(2*time.Second, 8*time.Second); got < 2*time.Second || got > 8*time.Second {
			t.Fatalf("pause %s is outside 2s..8s", got)
		}
	}
	if got := realBackgroundSyncRandom(5*time.Second, 5*time.Second); got != 5*time.Second {
		t.Fatalf("equal bounds gave %s", got)
	}
}

func TestBackgroundScheduleRunsOnlyInWindow(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 7, 0))
	env.chat(1, "user", 100)
	env.rnd.push(40*time.Minute, 3*time.Hour, 40*time.Minute, 90*time.Minute)
	env.start()
	first := schedAt(0, 8, 0)
	if st := env.status(); st.State != rpc.BackgroundSyncWaitingWindow || !samePtr(st.NextRunAt, unixPtr(first)) {
		t.Fatalf("before the window: %+v", st)
	}
	awaitCond(t, "the first timer", func() bool { return env.clock.hasTimerAt(first) })

	env.clock.AdvanceTo(schedAt(0, 7, 59))
	if st := env.status(); st.State != rpc.BackgroundSyncWaitingWindow || len(env.tg.pageCalls()) != 0 {
		t.Fatalf("a run started before the window: %+v pages=%v", st, env.tg.pageCalls())
	}

	st := env.jump(first)
	if st.LastResult != rpc.BackgroundSyncDone || !samePtr(st.LastRunAt, unixPtr(first)) || st.State != rpc.BackgroundSyncWaitingInterval {
		t.Fatalf("after the run: %+v", st)
	}
	// The next run is counted from the end of this one.
	if want := schedAt(0, 9, 30); !samePtr(st.NextRunAt, unixPtr(want)) {
		t.Fatalf("next run = %v, want %v", st.NextRunAt, want.Unix())
	}
	if got := env.tg.pageChats(); !slices.Equal(got, []int64{1}) {
		t.Fatalf("loaded chats = %v", got)
	}
}

// TestBackgroundScheduleLateWakeWaitsForTheWindow covers a timer that fires after
// the window has closed, as after a machine sleep: the run moves to the next
// opening instead of starting at night.
func TestBackgroundScheduleLateWakeWaitsForTheWindow(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 19, 0))
	env.chat(1, "user", 100)
	env.start()
	next := env.status().NextRunAt
	env.clock.AdvanceTo(schedAt(0, 22, 0))
	awaitCond(t, "the run to move", func() bool {
		st := env.status()
		return st.NextRunAt != nil && *st.NextRunAt != *next
	})
	st := env.status()
	if !samePtr(st.NextRunAt, unixPtr(schedAt(1, 8, 0))) || st.State != rpc.BackgroundSyncWaitingWindow || len(env.tg.pageCalls()) != 0 || st.LastRunAt != nil {
		t.Fatalf("late wake: %+v pages=%v", st, env.tg.pageCalls())
	}
}

func TestBackgroundSchedulePicksPeopleFoldersOthers(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
	env.chat(1, "user", 100)
	env.chat(2, "user", 200)
	env.chat(10, "channel", 50)
	env.chat(20, "group", 999)
	env.chat(21, "bot", 500)
	env.chat(30, "user", 900) // closed by the access list
	env.chat(40, "channel", 0)
	writeAccess(t, env.access, `{"block":[30]}`)
	env.tg.folders = rpc.FoldersResult{Folders: []rpc.Folder{{ID: 1, Kind: "filter", Title: "work", Include: []rpc.FolderPeer{{ID: 10}}, Exclude: []rpc.FolderPeer{{ID: 40}}}}}
	env.start()
	for range 6 {
		env.step()
	}
	// People first, the fresher one first; then the folder chat; then the rest,
	// the fresher first. A folder `exclude` entry does not make a folder chat.
	if got, want := env.tg.pageChats(), []int64{2, 1, 10, 20, 21, 40}; !slices.Equal(got, want) {
		t.Fatalf("load order = %v, want %v", got, want)
	}
	if env.complete(30) {
		t.Fatal("the closed chat was loaded")
	}
	// Nothing is left but the closed chat: the run is skipped.
	if st := env.step(); st.LastResult != rpc.BackgroundSyncSkipped {
		t.Fatalf("run with nothing to load: %+v", st)
	}
	if slices.Contains(env.tg.pageChats(), 30) {
		t.Fatal("the closed chat was requested from Telegram")
	}
}

func TestBackgroundScheduleFailedChatWaitsForNextDay(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
	env.chat(1, "user", 100)
	env.chat(2, "user", 200)
	var failOnce sync.Once
	env.tg.page = func(_ context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		var err error
		if ref.ID == 2 {
			failOnce.Do(func() { err = errors.New("boom") })
		}
		return syncPage(ref.ID), err
	}
	env.start()
	if st := env.step(); st.LastResult != rpc.BackgroundSyncFailed {
		t.Fatalf("first run: %+v", st)
	}
	if st := env.step(); st.LastResult != rpc.BackgroundSyncDone || !env.complete(1) {
		t.Fatalf("second run must take the other chat: %+v", st)
	}
	// The failed chat is skipped for the rest of the day.
	if st := env.step(); st.LastResult != rpc.BackgroundSyncSkipped {
		t.Fatalf("third run: %+v", st)
	}
	if got := env.tg.pageChats(); !slices.Equal(got, []int64{2, 1}) {
		t.Fatalf("chats requested the same day = %v", got)
	}
	// The next local date takes it again.
	awaitSyncIdle(t, env.d)
	if st := env.jump(schedAt(1, 9, 0)); st.LastResult != rpc.BackgroundSyncDone || !env.complete(2) {
		t.Fatalf("run on the next day: %+v", st)
	}
	if got := env.tg.pageChats(); !slices.Equal(got, []int64{2, 1, 2}) {
		t.Fatalf("chats requested = %v", got)
	}
}

// last_chat_id follows last_result: every run that picked a chat records it, a
// run that picked none leaves the earlier chat in place.
func TestBackgroundScheduleRecordsTheLastChat(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
	env.chat(1, "user", 100)
	env.chat(2, "user", 200)
	var failOnce sync.Once
	env.tg.page = func(_ context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		var err error
		if ref.ID == 2 {
			failOnce.Do(func() { err = errors.New("boom") })
		}
		return syncPage(ref.ID), err
	}
	env.start()
	if st := env.status(); st.LastChatID != nil {
		t.Fatalf("before the first run: %+v", st)
	}
	chatOf := func(st rpc.BackgroundSyncStatus) int64 {
		if st.LastChatID == nil {
			return 0
		}
		return *st.LastChatID
	}
	// The fresher chat 2 comes first and fails.
	if st := env.step(); st.LastResult != rpc.BackgroundSyncFailed || chatOf(st) != 2 {
		t.Fatalf("failed run: %+v", st)
	}
	if st := env.step(); st.LastResult != rpc.BackgroundSyncDone || chatOf(st) != 1 {
		t.Fatalf("done run: %+v", st)
	}
	// Nothing is left for today: the run is skipped and the chat stays.
	if st := env.step(); st.LastResult != rpc.BackgroundSyncSkipped || chatOf(st) != 1 {
		t.Fatalf("skipped run: %+v", st)
	}
}

func TestBackgroundScheduleWindowEndCancelsLoad(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 18, 0))
	env.chat(1, "user", 0)
	env.rnd.push(40*time.Minute, 3*time.Hour, 60*time.Minute)
	var mu sync.Mutex
	calls := 0
	blocked := make(chan struct{})
	env.tg.page = func(ctx context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		switch n {
		case 1:
			return syncPage(ref.ID, 12, 11, 10), nil
		case 2:
			close(blocked)
			<-ctx.Done()
			return rpc.HistoryPage{}, ctx.Err()
		}
		return syncPage(ref.ID), nil
	}
	env.start()
	runAt := schedAt(0, 19, 0)
	awaitCond(t, "the first timer", func() bool { return env.clock.hasTimerAt(runAt) })
	env.clock.AdvanceTo(runAt)
	if got := env.mgrClock.awaitPause(t); got < env.d.opts.BackgroundSync.PagePauseMin || got > env.d.opts.BackgroundSync.PagePauseMax {
		t.Fatalf("page pause %s is outside the configured range", got)
	}
	env.mgrClock.Advance(8 * time.Second)
	<-blocked
	if st := env.status(); st.State != rpc.BackgroundSyncRunning || st.CurrentChatID == nil || *st.CurrentChatID != 1 || st.NextRunAt != nil {
		t.Fatalf("running status: %+v", st)
	}

	st := env.jump(schedAt(0, 20, 0))
	if st.LastResult != rpc.BackgroundSyncCancelled || st.State != rpc.BackgroundSyncWaitingWindow || st.CurrentChatID != nil {
		t.Fatalf("after the window end: %+v", st)
	}
	if !samePtr(st.NextRunAt, unixPtr(schedAt(1, 8, 0))) {
		t.Fatalf("next run = %v", st.NextRunAt)
	}
	if state, found, err := env.db.SyncState(context.Background(), 1); err != nil || !found || state.Complete || state.OldestID == nil || *state.OldestID != 10 {
		t.Fatalf("saved progress: %+v found=%v err=%v", state, found, err)
	}
	if job := env.d.sync.snapshot(1); job == nil || job.State != rpc.HistorySyncCancelled || job.Pages != 1 {
		t.Fatalf("job: %+v", job)
	}

	// The next day picks the chat again and continues from the saved page.
	if st := env.step(); st.LastResult != rpc.BackgroundSyncDone || !env.complete(1) {
		t.Fatalf("next day: %+v", st)
	}
	pages := env.tg.pageCalls()
	if want := (chatCall{chat: 1, offset: 10}); pages[len(pages)-1] != want {
		t.Fatalf("the next load must continue from oldest_id: %v", pages)
	}
}

func TestBackgroundScheduleManualStartCancelsLoad(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
	env.chat(1, "user", 100)
	blocked := make(chan struct{}, 1)
	var once sync.Once
	env.tg.page = func(ctx context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		if ref.ID == 1 {
			held := false
			once.Do(func() { held = true })
			if held {
				blocked <- struct{}{}
				<-ctx.Done()
				return rpc.HistoryPage{}, ctx.Err()
			}
		}
		return syncPage(ref.ID), nil
	}
	env.start()
	prev := env.status().NextRunAt
	env.clock.AdvanceTo(time.Unix(*prev, 0))
	<-blocked
	if st := env.status(); st.State != rpc.BackgroundSyncRunning {
		t.Fatalf("status: %+v", st)
	}

	if job := startSync(t, env.d, 77); job.Source != rpc.HistorySyncManual {
		t.Fatalf("manual start: %+v", job)
	}
	st := env.settle(nil)
	if st.LastResult != rpc.BackgroundSyncCancelled {
		t.Fatalf("a manual start must cancel the scheduled load without failing it: %+v", st)
	}
	awaitSyncState(t, env.d, 77, rpc.HistorySyncDone)
	// A cancelled chat is not a failed one: the next run takes it again.
	if st := env.step(); st.LastResult != rpc.BackgroundSyncDone || !env.complete(1) {
		t.Fatalf("next run: %+v", st)
	}
	if got := env.tg.pageChats(); !slices.Equal(got, []int64{1, 77, 1}) {
		t.Fatalf("requested chats = %v", got)
	}
}

func TestBackgroundScheduleSkips(t *testing.T) {
	t.Run("auth lost", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		env.start()
		env.d.setAuthLost("session revoked")
		st := env.step()
		if st.LastResult != rpc.BackgroundSyncSkipped || len(env.tg.pageCalls()) != 0 || env.tg.folderCount() != 0 {
			t.Fatalf("auth lost: %+v pages=%v folders=%d", st, env.tg.pageCalls(), env.tg.folderCount())
		}
		if st.NextRunAt == nil || st.LastRunAt == nil || *st.NextRunAt <= *st.LastRunAt {
			t.Fatalf("a skipped run must schedule the next one: %+v", st)
		}
		env.d.setAuthLost("")
		if st := env.step(); st.LastResult != rpc.BackgroundSyncDone {
			t.Fatalf("after login: %+v", st)
		}
	})
	t.Run("no account", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		env.d.account.set(nil)
		env.start()
		if st := env.step(); st.LastResult != rpc.BackgroundSyncSkipped || len(env.tg.pageCalls()) != 0 {
			t.Fatalf("no account: %+v", st)
		}
	})
	t.Run("client not ready", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		env.d.opts.Telegram = &notReadyTelegram{env.tg}
		env.start()
		if st := env.step(); st.LastResult != rpc.BackgroundSyncSkipped || len(env.tg.pageCalls()) != 0 {
			t.Fatalf("client not ready: %+v", st)
		}
	})
	t.Run("busy manager", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		held := make(chan struct{}, 1)
		env.tg.page = func(ctx context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
			if ref.ID == 90 {
				held <- struct{}{}
				<-ctx.Done()
				return rpc.HistoryPage{}, ctx.Err()
			}
			return syncPage(ref.ID), nil
		}
		startSync(t, env.d, 90)
		<-held
		env.start()
		st := env.step(true)
		if st.LastResult != rpc.BackgroundSyncSkipped || !slices.Equal(env.tg.pageChats(), []int64{90}) {
			t.Fatalf("busy manager: %+v pages=%v", st, env.tg.pageChats())
		}
		if job := env.d.sync.snapshot(90); job == nil || job.State != rpc.HistorySyncRunning {
			t.Fatalf("the manual job must go on: %+v", job)
		}
		env.d.sync.cancelJob(90)
		awaitSyncIdle(t, env.d)
		if st := env.step(); st.LastResult != rpc.BackgroundSyncDone {
			t.Fatalf("idle manager: %+v", st)
		}
	})
}

// notReadyTelegram is a client whose connection is not up yet.
type notReadyTelegram struct{ *schedTelegram }

func (*notReadyTelegram) Ready() <-chan struct{} { return make(chan struct{}) }
