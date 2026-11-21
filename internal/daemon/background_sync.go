package daemon

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/redact"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// backgroundFoldersMaxAge is the age of the folder snapshot after which a run
// reads the folders again before it picks a chat.
const backgroundFoldersMaxAge = 4 * time.Hour

// backgroundSyncRandom returns a random duration from lo to hi, both included.
// It is called from the scheduler goroutine and from the history load worker,
// so it must be safe for concurrent use.
type backgroundSyncRandom func(lo, hi time.Duration) time.Duration

func realBackgroundSyncRandom(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(rand.Int64N(int64(hi-lo)+1))
}

// backgroundSync is the scheduler of the background history load. One
// goroutine wakes at a random moment inside the daily window, picks one chat
// that is not loaded to its first message and hands it to the history sync
// manager. It makes no Telegram request itself except the folder refresh, and
// it only reads. It does not exist when the feature is off.
type backgroundSync struct {
	d      *daemon
	cfg    BackgroundSyncConfig
	cfgMu  sync.RWMutex
	clock  historySyncClock
	random backgroundSyncRandom
	done   chan struct{}

	mu         sync.Mutex
	running    bool
	current    *int64
	nextRun    time.Time
	lastRunAt  *int64
	lastResult string
	// lastChat is the chat of the last run that picked one.
	lastChat *int64
	// failed holds the chats whose load failed on failedDay, a local date.
	failedDay string
	failed    map[int64]struct{}
}

// newBackgroundSync builds the scheduler; start launches it. A nil clock or
// random function means the real ones.
func newBackgroundSync(d *daemon, cfg BackgroundSyncConfig, clock historySyncClock, random backgroundSyncRandom) *backgroundSync {
	if clock == nil {
		clock = realHistorySyncClock{}
	}
	if random == nil {
		random = realBackgroundSyncRandom
	}
	return &backgroundSync{d: d, cfg: cfg, clock: clock, random: random, done: make(chan struct{}), failed: make(map[int64]struct{})}
}

func (s *backgroundSync) config() BackgroundSyncConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *backgroundSync) setConfig(cfg BackgroundSyncConfig) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

// startBackgroundSync launches the scheduler when the setting is on. Without it
// there is no scheduler and no goroutine: the daemon makes no new Telegram
// request for the background history load.
func (d *daemon) startBackgroundSync(ctx context.Context, clock historySyncClock, random backgroundSyncRandom) {
	cfg := d.backgroundConfig()
	if !cfg.Enabled {
		return
	}
	d.background = newBackgroundSync(d, cfg, clock, random)
	d.background.start(ctx)
}

// start schedules the first run, a random interval from now, and launches the
// goroutine. It ends when ctx ends; wait joins it.
func (s *backgroundSync) start(ctx context.Context) {
	s.nextRun = s.scheduleFrom(s.clock.Now())
	go s.loop(ctx)
}

func (s *backgroundSync) wait() { <-s.done }

// windowOf returns the window of the local day of t. The bounds are offsets
// from local midnight added to the date fields of time.Date, not to the instant
// of midnight: on a day with a clock change an offset of 8h still means 08:00
// on the wall clock, where Add(8h) would give 07:00 or 09:00.
func (s *backgroundSync) windowOf(t time.Time) (start, end time.Time) {
	return s.windowOfConfig(t, s.config())
}

func (s *backgroundSync) windowOfConfig(t time.Time, cfg BackgroundSyncConfig) (start, end time.Time) {
	return s.localOffset(t, cfg.WindowStart), s.localOffset(t, cfg.WindowEnd)
}

func (s *backgroundSync) localOffset(day time.Time, offset time.Duration) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, int(offset/time.Second), 0, day.Location())
}

func (s *backgroundSync) inWindow(t time.Time) bool {
	start, end := s.windowOf(t)
	return !t.Before(start) && t.Before(end)
}

// intoWindow moves a moment that is outside the window to the next opening:
// before the start it goes to the start of the same day, from the end on to
// the start of the next day.
func (s *backgroundSync) intoWindow(t time.Time) time.Time {
	return s.intoWindowConfig(t, s.config())
}

func (s *backgroundSync) intoWindowConfig(t time.Time, cfg BackgroundSyncConfig) time.Time {
	start, end := s.windowOfConfig(t, cfg)
	switch {
	case t.Before(start):
		return start
	case !t.Before(end):
		return s.localOffset(time.Date(t.Year(), t.Month(), t.Day()+1, 12, 0, 0, 0, t.Location()), cfg.WindowStart)
	default:
		return t
	}
}

// scheduleFrom is the next run after from: a random interval later, moved into
// the window.
func (s *backgroundSync) scheduleFrom(from time.Time) time.Time {
	cfg := s.config()
	return s.intoWindowConfig(from.Add(s.random(cfg.IntervalMin, cfg.IntervalMax)), cfg)
}

func (s *backgroundSync) loop(ctx context.Context) {
	defer close(s.done)
	for {
		s.mu.Lock()
		next := s.nextRun
		s.mu.Unlock()
		if !s.sleepUntil(ctx, next) {
			return
		}
		now := s.clock.Now()
		if !s.inWindow(now) {
			// The timer fired late, for example after the machine slept.
			s.setNext(s.intoWindow(now))
			continue
		}
		result, ok := s.runOnce(ctx, now)
		if !ok {
			return
		}
		s.finish(result)
	}
}

func (s *backgroundSync) setNext(t time.Time) {
	s.mu.Lock()
	s.nextRun = t
	s.mu.Unlock()
}

// sleepUntil waits for the clock to reach t. It reports false when ctx ended.
func (s *backgroundSync) sleepUntil(ctx context.Context, t time.Time) bool {
	for {
		wait := t.Sub(s.clock.Now())
		if wait <= 0 {
			return true
		}
		timer := s.clock.NewTimer(wait)
		// The time may have passed between the two clock reads.
		if !s.clock.Now().Before(t) {
			timer.Stop()
			return true
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C():
			timer.Stop()
		}
	}
}

// finish records the end of a run and schedules the next one from now.
func (s *backgroundSync) finish(result string) {
	now := s.clock.Now()
	unix := now.Unix()
	next := s.scheduleFrom(now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	if s.current != nil {
		// A run that picked no chat leaves the chat of the earlier run in place.
		chat := *s.current
		s.lastChat = &chat
	}
	s.current = nil
	s.lastRunAt = &unix
	s.lastResult = result
	s.nextRun = next
	s.d.log.Info("background sync run ended", "result", result)
	if result != rpc.BackgroundSyncSkipped {
		// The run may have saved pages: the semantic index looks at them.
		s.d.triggerSemanticIndex()
	}
}

// runOnce is one run at now, inside the window. It returns the result for the
// status, or false when the daemon is stopping.
func (s *backgroundSync) runOnce(ctx context.Context, now time.Time) (string, bool) {
	d := s.d
	if d.getAuthLost() != "" || !d.backgroundSyncReady() || d.sync.hasActive() {
		return rpc.BackgroundSyncSkipped, true
	}
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	_, end := s.windowOf(now)
	endTimer := s.clock.NewTimer(end.Sub(now))
	defer endTimer.Stop()

	if s.foldersStale(ctx, now) {
		switch s.refreshFolders(ctx, endTimer.C()) {
		case refreshStopped:
			return "", false
		case refreshWindowEnded:
			return rpc.BackgroundSyncSkipped, true
		}
	}
	chats, err := d.store.ChatsForBackgroundSync(ctx, chatFilter(d.policy()), s.failedToday(now), 1)
	if err != nil {
		if ctx.Err() != nil {
			return "", false
		}
		d.log.Warn("background sync could not pick a chat", "err", redact.Text(err.Error()))
		return rpc.BackgroundSyncSkipped, true
	}
	if len(chats) == 0 {
		return rpc.BackgroundSyncSkipped, true
	}
	chat := chats[0]
	progress, _, err := d.store.SyncState(ctx, chat.ID)
	if err != nil {
		if ctx.Err() != nil {
			return "", false
		}
		d.log.Warn("background sync could not read the progress", "err", redact.Text(err.Error()))
		return rpc.BackgroundSyncSkipped, true
	}
	pause := s.pagePause
	// The ref is the Bot API id of a chat the database knows from a dialog list,
	// the same ref a manual `chat sync start -- <id>` builds for it.
	ref := chatref.Ref{Kind: chatref.ID, ID: chat.ID}
	if _, err := d.sync.startBackground(chat.ID, ref, progress, pause); err != nil {
		// Busy: a manual load got there first. Stopping: the daemon is closing.
		return rpc.BackgroundSyncSkipped, ctx.Err() == nil
	}
	s.mu.Lock()
	id := chat.ID
	s.current = &id
	s.mu.Unlock()
	d.log.Info("background sync started", "chat", chat.ID, "group", chat.Group)
	return s.awaitJob(ctx, chat.ID, now, endTimer.C())
}

func (s *backgroundSync) pagePause() time.Duration {
	cfg := s.config()
	return s.random(cfg.PagePauseMin, cfg.PagePauseMax)
}

// foldersStale reports whether the folder snapshot is missing or older than
// backgroundFoldersMaxAge. A read error counts as stale: the refresh is the
// better guess than a pick from a snapshot nobody can date.
func (s *backgroundSync) foldersStale(ctx context.Context, now time.Time) bool {
	at, err := s.d.store.FoldersSyncedAt(ctx)
	if err != nil || at == nil {
		return true
	}
	return now.Sub(time.Unix(*at, 0)) > backgroundFoldersMaxAge
}

type refreshOutcome int

const (
	refreshDone refreshOutcome = iota
	refreshWindowEnded
	refreshStopped
)

// refreshFolders reads the folders from Telegram. A failure is only logged:
// the pick goes on with the old snapshot. The request stops when the window
// ends or the daemon stops, and the goroutine is joined before it returns.
func (s *backgroundSync) refreshFolders(ctx context.Context, end <-chan time.Time) refreshOutcome {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- s.callRefreshFolders(rctx) }()
	select {
	case err := <-errc:
		if err != nil && ctx.Err() == nil {
			s.d.log.Warn("background sync could not refresh the folders", "err", redact.Text(err.Error()))
		}
		if ctx.Err() != nil {
			return refreshStopped
		}
		return refreshDone
	case <-end:
		cancel()
		<-errc
		return refreshWindowEnded
	case <-ctx.Done():
		cancel()
		<-errc
		return refreshStopped
	}
}

func (s *backgroundSync) callRefreshFolders(ctx context.Context) error {
	d := s.d
	if d.opts.NewTelegram != nil {
		leased, client, release, err := d.acquireTelegram(ctx)
		if err != nil {
			return err
		}
		defer release()
		ctx = context.WithValue(leased, telegramContextKey{}, client)
	}
	_, err := d.refreshFolders(throttle.WithBackground(ctx))
	return err
}

type backgroundJobResult struct {
	job rpc.HistorySyncJob
	err error
}

// awaitJob waits for the job of chatID. At the end of the window it cancels the
// background job and waits for the worker to leave it. A stopping daemon ends
// the wait without a result.
func (s *backgroundSync) awaitJob(ctx context.Context, chatID int64, now time.Time, end <-chan time.Time) (string, bool) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make(chan backgroundJobResult, 1)
	go func() {
		job, err := s.d.sync.waitJob(wctx, chatID)
		out <- backgroundJobResult{job, err}
	}()
	var got backgroundJobResult
	select {
	case got = <-out:
	case <-end:
		s.d.sync.cancelBackground()
		got = <-out
	case <-ctx.Done():
		cancel()
		<-out
		return "", false
	}
	if got.err != nil {
		return rpc.BackgroundSyncSkipped, ctx.Err() == nil
	}
	switch got.job.State {
	case rpc.HistorySyncDone:
		return rpc.BackgroundSyncDone, true
	case rpc.HistorySyncCancelled:
		return rpc.BackgroundSyncCancelled, true
	case rpc.HistorySyncFailed:
		s.markFailed(chatID, now)
		return rpc.BackgroundSyncFailed, true
	}
	return rpc.BackgroundSyncSkipped, true
}

func localDay(t time.Time) string { return t.Format(time.DateOnly) }

// markFailed keeps a chat out of the pick until the local date changes.
func (s *backgroundSync) markFailed(chatID int64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetFailedLocked(now)
	s.failed[chatID] = struct{}{}
}

// failedToday returns the chats whose load failed on the local date of now.
func (s *backgroundSync) failedToday(now time.Time) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetFailedLocked(now)
	ids := make([]int64, 0, len(s.failed))
	for id := range s.failed {
		ids = append(ids, id)
	}
	return ids
}

func (s *backgroundSync) resetFailedLocked(now time.Time) {
	if day := localDay(now); day != s.failedDay {
		s.failedDay = day
		clear(s.failed)
	}
}

// status is the background_sync block of the daemon status.
func (s *backgroundSync) status() rpc.BackgroundSyncStatus {
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := rpc.BackgroundSyncStatus{Enabled: true, Window: backgroundWindow(s.config()), LastResult: s.lastResult, LastRunAt: s.lastRunAt, LastChatID: s.lastChat}
	switch {
	case s.running:
		st.State = rpc.BackgroundSyncRunning
		st.CurrentChatID = s.current
	case !s.inWindow(now):
		st.State = rpc.BackgroundSyncWaitingWindow
	default:
		st.State = rpc.BackgroundSyncWaitingInterval
	}
	if !s.running {
		next := s.nextRun.Unix()
		st.NextRunAt = &next
	}
	return st
}

func backgroundWindow(cfg BackgroundSyncConfig) rpc.BackgroundSyncWindow {
	return rpc.BackgroundSyncWindow{Start: clockOffset(cfg.WindowStart), End: clockOffset(cfg.WindowEnd)}
}

// clockOffset formats an offset from midnight as "HH:MM".
func clockOffset(offset time.Duration) string {
	minutes := int(offset / time.Minute)
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// backgroundSyncStatus is the block for `daemon status`; it exists also when
// the scheduler is off.
func (d *daemon) backgroundSyncStatus() rpc.BackgroundSyncStatus {
	if d.background == nil {
		return rpc.BackgroundSyncStatus{Window: backgroundWindow(d.backgroundConfig()), State: rpc.BackgroundSyncDisabled}
	}
	return d.background.status()
}

// backgroundSyncReady reports, without a request, whether the client is up and
// an account is logged in. The scheduler never waits for a connection.
func (d *daemon) backgroundSyncReady() bool {
	client := d.opts.Telegram
	if d.opts.NewTelegram != nil {
		_, leased, release, err := d.acquireTelegram(context.Background())
		if err != nil {
			return false
		}
		defer release()
		client = leased
	}
	select {
	case <-client.Ready():
	default:
		return false
	}
	d.account.mu.RLock()
	defer d.account.mu.RUnlock()
	return d.account.user != nil
}
