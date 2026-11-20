package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

const historySyncPause = 2 * time.Second

// defaultHistorySyncPause is the page pause of a manual load.
func defaultHistorySyncPause() time.Duration { return historySyncPause }

type historySyncTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type historySyncClock interface {
	Now() time.Time
	NewTimer(time.Duration) historySyncTimer
}

type realHistorySyncClock struct{}
type realHistorySyncTimer struct{ *time.Timer }

func (realHistorySyncClock) Now() time.Time { return time.Now() }
func (realHistorySyncClock) NewTimer(d time.Duration) historySyncTimer {
	return realHistorySyncTimer{time.NewTimer(d)}
}
func (t realHistorySyncTimer) C() <-chan time.Time { return t.Timer.C }

type historySyncTask struct {
	rpc.HistorySyncJob
	ref    chatref.Ref
	offset int
	ctx    context.Context
	cancel context.CancelFunc
	// pause returns the wait before the next page; it is called once per page.
	pause func() time.Duration
	// final is closed, under the manager mutex, when the job reaches done,
	// failed or cancelled.
	final chan struct{}
}

func (t *historySyncTask) active() bool {
	return t.State == rpc.HistorySyncQueued || t.State == rpc.HistorySyncRunning
}

// historySyncManager owns one worker for all chats and joins it on shutdown.
type historySyncManager struct {
	d      *daemon
	clock  historySyncClock
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}

	mu      sync.Mutex
	jobs    map[int64]*historySyncTask
	queue   []*historySyncTask
	current *historySyncTask
}

func newHistorySyncManager(ctx context.Context, d *daemon, clock historySyncClock) *historySyncManager {
	if clock == nil {
		clock = realHistorySyncClock{}
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &historySyncManager{
		d: d, clock: clock, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), wake: make(chan struct{}, 1),
		jobs: make(map[int64]*historySyncTask),
	}
	go m.run()
	return m
}

func (m *historySyncManager) close() {
	m.cancel()
	m.mu.Lock()
	for _, task := range m.queue {
		m.cancelTaskLocked(task)
	}
	m.queue = nil
	m.mu.Unlock()
	<-m.done
}

func (m *historySyncManager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current != nil || len(m.queue) > 0
}

func (m *historySyncManager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *historySyncManager) now() string {
	return m.clock.Now().UTC().Format(time.RFC3339)
}

// start queues a manual load. A manual load has priority over a background
// one: it cancels the background job, queued or running, and waits only for
// the manual jobs ahead of it.
func (m *historySyncManager) start(chatID int64, ref chatref.Ref, state store.SyncProgress) (rpc.HistorySyncJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return rpc.HistorySyncJob{}, rpc.NewError(rpc.CodeBusy, "history sync is stopping")
	}
	if existing := m.jobs[chatID]; existing != nil && existing.active() && existing.Source == rpc.HistorySyncManual {
		return existing.HistorySyncJob, nil
	}
	if !state.Complete {
		m.cancelBackgroundLocked()
	}
	return m.enqueueLocked(chatID, ref, state, rpc.HistorySyncManual, nil), nil
}

// startBackground queues a background load only when the manager is idle.
// The check and the queueing happen under one lock, so a manual load that
// arrives meanwhile either goes first or makes this call return busy.
func (m *historySyncManager) startBackground(chatID int64, ref chatref.Ref, state store.SyncProgress, pause func() time.Duration) (rpc.HistorySyncJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return rpc.HistorySyncJob{}, rpc.NewError(rpc.CodeBusy, "history sync is stopping")
	}
	if m.current != nil || len(m.queue) > 0 {
		return rpc.HistorySyncJob{}, rpc.NewError(rpc.CodeBusy, "history sync is busy")
	}
	return m.enqueueLocked(chatID, ref, state, rpc.HistorySyncBackground, pause), nil
}

func (m *historySyncManager) enqueueLocked(chatID int64, ref chatref.Ref, state store.SyncProgress, source string, pause func() time.Duration) rpc.HistorySyncJob {
	if pause == nil {
		pause = defaultHistorySyncPause
	}
	when := m.now()
	jobCtx, cancel := context.WithCancel(m.ctx)
	task := &historySyncTask{
		HistorySyncJob: rpc.HistorySyncJob{
			ChatID: chatID, State: rpc.HistorySyncQueued, Source: source,
			OldestID: state.OldestID, NewestID: state.NewestID, Complete: state.Complete,
			FirstMessageDate: state.FirstMessageDate,
			StartedAt:        when, UpdatedAt: when,
		},
		ref: ref, ctx: jobCtx, cancel: cancel, pause: pause, final: make(chan struct{}),
	}
	if state.OldestID != nil {
		task.offset = *state.OldestID
	}
	if state.Complete {
		m.setStateLocked(task, rpc.HistorySyncDone, 0, "")
		cancel()
	} else {
		m.queue = append(m.queue, task)
		m.signal()
	}
	m.jobs[chatID] = task
	return task.HistorySyncJob
}

// setStateLocked moves a job to state and releases its waiters when the state
// is final. The caller holds m.mu.
func (m *historySyncManager) setStateLocked(task *historySyncTask, state string, code int, message string) {
	task.State = state
	task.ErrorCode = code
	task.Error = message
	task.UpdatedAt = m.now()
	if !task.active() {
		select {
		case <-task.final:
		default:
			close(task.final)
		}
	}
	if state == rpc.HistorySyncDone {
		// A finished load brings new messages for the semantic index. The trigger
		// only sets a flag or sends on a buffered channel, so it never blocks here.
		m.d.triggerSemanticIndex()
	}
}

// cancelTaskLocked cancels a queued or running job and drops it from the
// queue. Saved pages stay: the next load continues from oldest_id.
func (m *historySyncManager) cancelTaskLocked(task *historySyncTask) {
	if !task.active() {
		return
	}
	for i, queued := range m.queue {
		if queued == task {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	m.setStateLocked(task, rpc.HistorySyncCancelled, 0, "")
	task.cancel()
}

func (m *historySyncManager) cancelBackgroundLocked() {
	for _, task := range append([]*historySyncTask(nil), m.queue...) {
		if task.Source == rpc.HistorySyncBackground {
			m.cancelTaskLocked(task)
		}
	}
	if m.current != nil && m.current.Source == rpc.HistorySyncBackground {
		m.cancelTaskLocked(m.current)
	}
}

// cancelBackground cancels the current and queued background jobs and leaves
// manual ones alone. It is safe when there are none.
func (m *historySyncManager) cancelBackground() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelBackgroundLocked()
}

// waitJob blocks while the job of the chat is queued or running and returns
// its final snapshot. A chat without a job returns at once as idle.
func (m *historySyncManager) waitJob(ctx context.Context, chatID int64) (rpc.HistorySyncJob, error) {
	m.mu.Lock()
	task := m.jobs[chatID]
	if task == nil {
		m.mu.Unlock()
		return rpc.HistorySyncJob{ChatID: chatID, State: rpc.HistorySyncIdle}, nil
	}
	final := task.final
	m.mu.Unlock()
	select {
	case <-final:
	case <-m.done:
	case <-ctx.Done():
		return rpc.HistorySyncJob{}, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return task.HistorySyncJob, nil
}

func (m *historySyncManager) cancelJob(chatID int64) *rpc.HistorySyncJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.jobs[chatID]
	if task == nil {
		return nil
	}
	m.cancelTaskLocked(task)
	result := task.HistorySyncJob
	return &result
}

func (m *historySyncManager) snapshot(chatID int64) *rpc.HistorySyncJob {
	m.mu.Lock()
	task := m.jobs[chatID]
	if task == nil {
		m.mu.Unlock()
		return nil
	}
	result := task.HistorySyncJob
	running := task == m.current && task.State == rpc.HistorySyncRunning
	m.mu.Unlock()
	if running {
		client := m.d.opts.Telegram
		release := func() {}
		if m.d.opts.NewTelegram != nil {
			var err error
			_, client, release, err = m.d.acquireTelegram(context.Background())
			if err != nil {
				return &result
			}
		}
		defer release()
		if remaining := client.FloodWaitRemaining(); remaining > 0 {
			result.State = rpc.HistorySyncWaitingFlood
			result.WaitSeconds = int(remaining / time.Second)
			if remaining%time.Second != 0 {
				result.WaitSeconds++
			}
		}
	}
	return &result
}

func (m *historySyncManager) chatIDForRef(ref chatref.Ref) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, task := range m.jobs {
		if task.ref == ref {
			return id, true
		}
	}
	return 0, false
}

func (m *historySyncManager) snapshots() []rpc.HistorySyncJob {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	policy := m.d.policy()
	jobs := make([]rpc.HistorySyncJob, 0, len(ids))
	for _, id := range ids {
		if !policy.Allowed(id) {
			continue
		}
		if job := m.snapshot(id); job != nil {
			jobs = append(jobs, *job)
		}
	}
	return jobs
}

func (m *historySyncManager) run() {
	defer close(m.done)
	for {
		m.mu.Lock()
		if m.ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		if len(m.queue) == 0 {
			m.mu.Unlock()
			select {
			case <-m.ctx.Done():
				return
			case <-m.wake:
				continue
			}
		}
		task := m.queue[0]
		m.queue = m.queue[1:]
		m.current = task
		task.State = rpc.HistorySyncRunning
		task.UpdatedAt = m.now()
		m.mu.Unlock()

		m.runTask(task)
		task.cancel()
		m.mu.Lock()
		m.current = nil
		m.mu.Unlock()
	}
}

func (m *historySyncManager) finish(task *historySyncTask, state string, code int, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task.State == rpc.HistorySyncCancelled {
		return
	}
	m.setStateLocked(task, state, code, message)
}

func (m *historySyncManager) fail(task *historySyncTask, err error) {
	if task.ctx.Err() != nil {
		m.finish(task, rpc.HistorySyncCancelled, 0, "")
		return
	}
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) {
		m.finish(task, rpc.HistorySyncFailed, rpcErr.Code, "history sync failed")
		return
	}
	m.finish(task, rpc.HistorySyncFailed, rpc.CodeInternal, "history sync failed")
}

func (m *historySyncManager) recordPage(task *historySyncTask, progress *store.SyncProgress, loaded int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task.Pages++
	task.Loaded += loaded
	if progress != nil {
		task.OldestID = progress.OldestID
		task.NewestID = progress.NewestID
		task.Complete = progress.Complete
		task.FirstMessageDate = progress.FirstMessageDate
	}
	task.UpdatedAt = m.now()
}

func (m *historySyncManager) runTask(task *historySyncTask) {
	ctx := task.ctx
	client := m.d.opts.Telegram
	release := func() {}
	if m.d.opts.NewTelegram != nil {
		var err error
		ctx, client, release, err = m.d.acquireTelegram(ctx)
		if err != nil {
			m.fail(task, err)
			return
		}
	}
	defer release()
	fail := func(err error) {
		if ctx.Err() != nil {
			m.finish(task, rpc.HistorySyncCancelled, 0, "")
			return
		}
		m.fail(task, err)
	}
	offset := task.offset
	for {
		if ctx.Err() != nil {
			m.finish(task, rpc.HistorySyncCancelled, 0, "")
			return
		}
		if !m.d.policy().Allowed(task.ChatID) {
			m.finish(task, rpc.HistorySyncFailed, rpc.CodeNotFound, rpc.ErrChatNotFound().Message)
			return
		}
		page, err := client.HistoryPage(ctx, task.ref, offset)
		if err != nil {
			fail(err)
			return
		}
		if ctx.Err() != nil {
			m.finish(task, rpc.HistorySyncCancelled, 0, "")
			return
		}
		if !m.d.policy().Allowed(task.ChatID) {
			m.finish(task, rpc.HistorySyncFailed, rpc.CodeNotFound, rpc.ErrChatNotFound().Message)
			return
		}
		if page.Empty {
			progress, err := m.d.store.SaveHistoryPage(ctx, task.ChatID, nil, nil, true)
			if err != nil {
				fail(err)
				return
			}
			m.recordPage(task, &progress, 0)
			m.finish(task, rpc.HistorySyncDone, 0, "")
			return
		}
		if page.RawNextOffsetID <= 0 || offset > 0 && page.RawNextOffsetID >= offset {
			m.finish(task, rpc.HistorySyncFailed, rpc.CodeTelegram, "history page offset did not advance")
			return
		}
		if len(page.Messages) > 0 {
			messages, people, _, err := convertStoredMessages(page.Messages, true)
			if err != nil {
				fail(err)
				return
			}
			progress, err := m.d.store.SaveHistoryPage(ctx, task.ChatID, messages, people, false)
			if err != nil {
				fail(err)
				return
			}
			m.recordPage(task, &progress, len(messages))
		} else {
			m.recordPage(task, nil, 0)
		}
		offset = page.RawNextOffsetID
		timer := m.clock.NewTimer(task.pause())
		select {
		case <-ctx.Done():
			timer.Stop()
			m.finish(task, rpc.HistorySyncCancelled, 0, "")
			return
		case <-timer.C():
			timer.Stop()
		}
	}
}

func (d *daemon) resolveHistorySyncChat(ctx context.Context, name string) (chatref.Ref, int64, error) {
	ref, err := parseChatNoInvite(name)
	if err != nil {
		return chatref.Ref{}, 0, err
	}
	chat, err := d.requireAllowedChat(ctx, ref)
	if err != nil {
		return chatref.Ref{}, 0, err
	}
	if chat == nil {
		resolved, err := d.telegram(ctx).Resolve(ctx, ref)
		if err != nil {
			return chatref.Ref{}, 0, err
		}
		chat = &resolved
	}
	if !d.policy().Allowed(chat.ID) {
		return chatref.Ref{}, 0, rpc.ErrChatNotFound()
	}
	return ref, chat.ID, nil
}

// existingHistorySyncChat resolves a known job locally so a flood gate cannot
// prevent its status check or cancellation.
func (d *daemon) existingHistorySyncChat(ctx context.Context, name string) (int64, error) {
	ref, err := parseChatNoInvite(name)
	if err != nil {
		return 0, err
	}
	if ref.Kind == chatref.ID {
		if !d.policy().Allowed(ref.ID) {
			return 0, rpc.ErrUnknownChatID()
		}
		return ref.ID, nil
	}
	if id, found := d.sync.chatIDForRef(ref); found {
		if !d.policy().Allowed(id) {
			return 0, rpc.ErrChatNotFound()
		}
		return id, nil
	}
	_, id, err := d.resolveHistorySyncChat(ctx, name)
	return id, err
}

func (d *daemon) historySyncStart(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.HistorySyncParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, chatID, err := d.resolveHistorySyncChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	progress, _, err := d.store.SyncState(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return d.sync.start(chatID, ref, progress)
}

func (d *daemon) historySyncStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.HistorySyncStatusParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return rpc.HistorySyncJobs{Jobs: d.sync.snapshots()}, nil
	}
	chatID, err := d.existingHistorySyncChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	if job := d.sync.snapshot(chatID); job != nil {
		return job, nil
	}
	return d.idleHistorySyncJob(ctx, p.Chat)
}

func (d *daemon) historySyncCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.HistorySyncParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	chatID, err := d.existingHistorySyncChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	if job := d.sync.cancelJob(chatID); job != nil {
		return job, nil
	}
	return d.idleHistorySyncJob(ctx, p.Chat)
}

func (d *daemon) idleHistorySyncJob(ctx context.Context, chat string) (rpc.HistorySyncJob, error) {
	_, chatID, err := d.resolveHistorySyncChat(ctx, chat)
	if err != nil {
		return rpc.HistorySyncJob{}, err
	}
	progress, _, err := d.store.SyncState(ctx, chatID)
	if err != nil {
		return rpc.HistorySyncJob{}, err
	}
	return rpc.HistorySyncJob{
		ChatID: chatID, State: rpc.HistorySyncIdle,
		OldestID: progress.OldestID, NewestID: progress.NewestID, Complete: progress.Complete,
		FirstMessageDate: progress.FirstMessageDate,
	}, nil
}
