package daemon

import (
	"context"
	"errors"
	"sync"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// chatFullPause is the pause between two chats of the job. One chat is one
// Telegram request, and the shared throttle of the account already keeps the rate
// low; the pause keeps the job out of the way of ordinary commands.
const chatFullPause = 2 * time.Second

// chatFullTelegram is the part of the Telegram client the job needs: one read of
// the full data of a chat, and the remaining account-wide flood wait. The job never
// writes and never resolves a name.
type chatFullTelegram interface {
	ChatFull(ctx context.Context, chatID int64) (rpc.ChatInfo, error)
	FloodWaitRemaining() time.Duration
}

// chatFullClock makes the pause and the timestamps testable.
type chatFullClock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) bool
}

type realChatFullClock struct{}

func (realChatFullClock) Now() time.Time { return time.Now() }
func (realChatFullClock) Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// chatFullSyncManager runs one full-data job per daemon run: one worker, one chat
// at a time, cancellable, with progress in memory. The database keeps the progress
// of every chat in full_synced_at and full_error, so a later start continues with
// the chats that have none.
type chatFullSyncManager struct {
	// clock is replaceable in tests; the real one sleeps and reads the wall clock.
	clock chatFullClock
	// list is the chat selection of the job.
	list func(ctx context.Context, filter store.ChatFilter, refresh bool) ([]store.FullSyncChat, error)
	// save writes the result of one chat.
	save func(ctx context.Context, chatID int64, full store.ChatFull) error
	// allowed reports whether the access lists show the chat right now.
	allowed func(chatID int64) bool
	// filter is the access filter for the chat selection.
	filter func() store.ChatFilter
	// local is the reference resolution of the daemon: a chat is looked up in the
	// local database, so the job never resolves a name against Telegram.
	local func(ctx context.Context, value string) (int64, error)
	// telegram returns the client and releases it.
	telegram func(ctx context.Context) (context.Context, chatFullTelegram, func(), error)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}

	mu    sync.Mutex
	job   rpc.ChatFullJob
	queue []store.FullSyncChat
	// runID identifies the current run, so a late result of a cancelled run
	// cannot overwrite the state of the next one.
	runID    int
	stopOnce sync.Once
}

// newChatFullSyncManager builds the manager of one daemon run. nil parts are taken
// from the daemon, which is what the daemon itself uses.
func newChatFullSyncManager(ctx context.Context, d *daemon, clock chatFullClock) *chatFullSyncManager {
	if ctx == nil {
		ctx = context.Background()
	}
	if clock == nil {
		clock = realChatFullClock{}
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &chatFullSyncManager{
		clock: clock, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), wake: make(chan struct{}, 1),
	}
	if d != nil {
		db := d.store
		m.list = func(ctx context.Context, filter store.ChatFilter, refresh bool) ([]store.FullSyncChat, error) {
			return db.ChatsForFullSync(ctx, filter, refresh)
		}
		m.save = func(ctx context.Context, chatID int64, full store.ChatFull) error {
			return db.SaveChatFull(ctx, chatID, full)
		}
		m.allowed = func(chatID int64) bool { return d.policy().Allowed(chatID) }
		m.filter = func() store.ChatFilter { return chatFilter(d.policy()) }
		m.local = func(ctx context.Context, value string) (int64, error) {
			id, _, err := d.localChatID(ctx, value)
			return id, err
		}
		m.telegram = func(ctx context.Context) (context.Context, chatFullTelegram, func(), error) {
			leased, client, release, err := d.acquireTelegram(ctx)
			if err != nil {
				return ctx, nil, nil, err
			}
			full, ok := client.(chatFullTelegram)
			if !ok {
				release()
				return leased, nil, nil, errors.New("the Telegram client cannot read the full data of a chat")
			}
			return leased, full, release, nil
		}
	}
	go m.run()
	return m
}

// close stops the job and waits for the worker.
func (m *chatFullSyncManager) close() {
	m.stopOnce.Do(func() {
		m.cancel()
		m.mu.Lock()
		if m.job.State == rpc.ChatFullQueued || m.job.State == rpc.ChatFullRunning || m.job.State == rpc.ChatFullWaitingFlood {
			m.job.State = rpc.ChatFullCancelled
			m.job.UpdatedAt = m.now()
		}
		m.queue = nil
		m.mu.Unlock()
		<-m.done
	})
}

// hasActive reports whether a job is queued, running or waiting.
func (m *chatFullSyncManager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.job.State {
	case rpc.ChatFullQueued, rpc.ChatFullRunning, rpc.ChatFullWaitingFlood:
		return true
	}
	return false
}

func (m *chatFullSyncManager) now() string {
	return m.clock.Now().UTC().Format(time.RFC3339)
}

// status returns the current job state, with the remaining flood wait when the
// worker cannot proceed.
func (m *chatFullSyncManager) status() rpc.ChatFullJob {
	m.mu.Lock()
	job := m.job
	waiting := job.State == rpc.ChatFullWaitingFlood
	m.mu.Unlock()
	if job.State == "" {
		job.State = rpc.ChatFullIdle
		job.UpdatedAt = m.now()
	}
	if waiting && m.telegram != nil {
		// A lease without a request only observes the pause of the account: the
		// worker cannot report it while it sleeps.
		if _, client, release, err := m.telegram(context.Background()); err == nil {
			defer release()
			if remaining := client.FloodWaitRemaining(); remaining > 0 {
				job.WaitSeconds = int((remaining + time.Second - 1) / time.Second)
			}
		}
	}
	return job
}

// start queues the job. Without a chat it reads every allowed group and channel
// without a complete read; with a chat it reads that one chat and refuses a chat
// the access lists close. A job that is already running is reported unchanged.
func (m *chatFullSyncManager) start(ctx context.Context, chat string, refresh bool) (rpc.ChatFullJob, error) {
	if m.ctx.Err() != nil {
		return rpc.ChatFullJob{}, rpc.NewError(rpc.CodeBusy, "the daemon is stopping")
	}
	filter := store.ChatFilter{}
	if m.filter != nil {
		filter = m.filter()
	}
	m.mu.Lock()
	active := m.hasActiveLocked()
	m.mu.Unlock()
	if active {
		return m.status(), nil
	}
	queue, err := m.selectChats(ctx, chat, refresh, filter)
	if err != nil {
		return rpc.ChatFullJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return rpc.ChatFullJob{}, rpc.NewError(rpc.CodeBusy, "the daemon is stopping")
	}
	m.runID++
	now := m.now()
	m.job = rpc.ChatFullJob{State: rpc.ChatFullQueued, Total: len(queue), StartedAt: now, UpdatedAt: now}
	m.queue = queue
	if len(queue) == 0 {
		m.job.State = rpc.ChatFullDone
		return m.job, nil
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return m.job, nil
}

// hasActiveLocked is hasActive for a caller that already holds the lock.
func (m *chatFullSyncManager) hasActiveLocked() bool {
	switch m.job.State {
	case rpc.ChatFullQueued, rpc.ChatFullRunning, rpc.ChatFullWaitingFlood:
		return true
	}
	return false
}

// selectChats resolves the chat argument and asks the database for the work. The
// filter comes from the access lists of the moment, so a closed chat is never
// selected.
func (m *chatFullSyncManager) selectChats(ctx context.Context, chat string, refresh bool, filter store.ChatFilter) ([]store.FullSyncChat, error) {
	if m.list == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "the full sync has no storage")
	}
	if chat == "" {
		return m.list(ctx, filter, refresh)
	}
	if m.local == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "the full sync cannot resolve a chat")
	}
	id, err := m.local(ctx, chat)
	if err != nil {
		return nil, err
	}
	// One chat bypasses the selection of unfinished chats, so the allowed lists
	// still decide: an id the filter hides is not in the answer.
	chats, err := m.list(ctx, filter, true)
	if err != nil {
		return nil, err
	}
	for _, candidate := range chats {
		if candidate.ID == id {
			return []store.FullSyncChat{candidate}, nil
		}
	}
	return nil, rpc.NewError(rpc.CodeNotFound, rpc.ErrChatNotFound().Message)
}

// cancel stops the job after the chat it is reading. A job that is already
// finished is reported as it is.
func (m *chatFullSyncManager) cancelJob() rpc.ChatFullJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.job.State {
	case rpc.ChatFullQueued, rpc.ChatFullRunning, rpc.ChatFullWaitingFlood:
		m.job.State = rpc.ChatFullCancelled
		m.job.UpdatedAt = m.now()
		m.queue = nil
		// The worker checks the run id after every request, so it stops after the
		// current chat instead of mid-request.
		m.runID++
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
	return m.job
}

func (m *chatFullSyncManager) run() {
	defer close(m.done)
	for {
		m.mu.Lock()
		if m.ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		if len(m.queue) == 0 || m.job.State == rpc.ChatFullCancelled {
			m.mu.Unlock()
			select {
			case <-m.ctx.Done():
				return
			case <-m.wake:
				continue
			}
		}
		chat := m.queue[0]
		m.queue = m.queue[1:]
		m.job.State = rpc.ChatFullRunning
		m.job.CurrentChatID = &chat.ID
		m.job.UpdatedAt = m.now()
		runID := m.runID
		m.mu.Unlock()

		m.runChat(chat, runID)

		m.mu.Lock()
		m.job.CurrentChatID = nil
		if len(m.queue) == 0 && m.job.State == rpc.ChatFullRunning {
			m.job.State = rpc.ChatFullDone
		}
		m.job.UpdatedAt = m.now()
		finished := m.job.State == rpc.ChatFullCancelled
		m.mu.Unlock()
		if finished {
			continue
		}
		if !m.clock.Sleep(m.ctx, chatFullPause) {
			return
		}
	}
}

// runChat reads one chat, honouring the access lists before and after the request,
// and records the outcome both in the job and in the database.
func (m *chatFullSyncManager) runChat(chat store.FullSyncChat, runID int) {
	if m.allowed != nil && !m.allowed(chat.ID) {
		// A chat the owner closed is not requested and not counted as failed.
		m.mu.Lock()
		if m.runID == runID {
			m.job.Total--
			m.job.UpdatedAt = m.now()
		}
		m.mu.Unlock()
		return
	}
	ctx, client, release, err := m.telegram(m.ctx)
	if err != nil {
		m.recordFailure(chat.ID, runID, err)
		return
	}
	defer release()
	if !m.stillWanted(runID) {
		return
	}
	info, err := client.ChatFull(ctx, chat.ID)
	if err != nil {
		// The throttle of the account pauses itself on a FLOOD_WAIT, so the
		// remaining pause is the wait the job reports.
		if remaining := client.FloodWaitRemaining(); remaining > 0 {
			m.waitFlood(chat, runID, client, remaining)
			return
		}
		m.recordFailure(chat.ID, runID, err)
		return
	}
	if m.allowed != nil && !m.allowed(info.ID) {
		return
	}
	m.recordSuccess(chat.ID, runID, info)
}

// waitFlood reports the wait and retries the chat once it is over, which is what
// the throttle of the account already controls. A cancel during the wait stops the
// job without the request.
func (m *chatFullSyncManager) waitFlood(chat store.FullSyncChat, runID int, client chatFullTelegram, remaining time.Duration) {
	m.mu.Lock()
	if m.runID != runID || m.job.State != rpc.ChatFullRunning {
		m.mu.Unlock()
		return
	}
	m.job.State = rpc.ChatFullWaitingFlood
	m.job.WaitSeconds = int((remaining + time.Second - 1) / time.Second)
	m.job.UpdatedAt = m.now()
	m.mu.Unlock()
	if !m.clock.Sleep(m.ctx, remaining) {
		return
	}
	m.mu.Lock()
	if m.runID != runID || m.job.State != rpc.ChatFullWaitingFlood {
		m.mu.Unlock()
		return
	}
	m.job.State = rpc.ChatFullRunning
	m.job.WaitSeconds = 0
	m.job.UpdatedAt = m.now()
	m.mu.Unlock()
	ctx, again, release, err := m.telegram(m.ctx)
	if err != nil {
		m.recordFailure(chat.ID, runID, err)
		return
	}
	defer release()
	info, err := again.ChatFull(ctx, chat.ID)
	if err != nil {
		m.recordFailure(chat.ID, runID, err)
		return
	}
	m.recordSuccess(chat.ID, runID, info)
}

// stillWanted reports whether the run is still the current one.
func (m *chatFullSyncManager) stillWanted(runID int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runID == runID && m.job.State == rpc.ChatFullRunning
}

func (m *chatFullSyncManager) recordSuccess(chatID int64, runID int, info rpc.ChatInfo) {
	synced := m.clock.Now().Unix()
	full := store.ChatFull{Raw: info.Raw, Settings: info.Settings, SyncedAt: &synced}
	if info.About != "" {
		about := info.About
		full.About = &about
	}
	if info.Members > 0 {
		members := int64(info.Members)
		full.Members = &members
	}
	if m.save != nil {
		// The save has to finish even when the job is cancelled meanwhile, so the
		// chat is not requested again for nothing.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), saveTimeout)
		err := m.save(ctx, chatID, full)
		cancel()
		if err != nil {
			m.recordFailure(chatID, runID, err)
			return
		}
	}
	m.mu.Lock()
	if m.runID == runID {
		m.job.Done++
		m.job.UpdatedAt = m.now()
	}
	m.mu.Unlock()
}

// recordFailure keeps the chat on the list of the next start and counts it.
func (m *chatFullSyncManager) recordFailure(chatID int64, runID int, err error) {
	message := failureMessage(err)
	if m.save != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), saveTimeout)
		_ = m.save(ctx, chatID, store.ChatFull{Error: &message})
		cancel()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runID != runID {
		return
	}
	m.job.Failed++
	m.job.Error = message
	m.job.UpdatedAt = m.now()
}

// failureMessage keeps a stable text in the database: a Telegram error keeps its
// type, everything else its own message.
func failureMessage(err error) string {
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpc.CodeTelegram {
		return rpcErr.Message
	}
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
