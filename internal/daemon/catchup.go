package daemon

import (
	"context"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
)

type catchupChat struct {
	Chat   rpc.Chat
	Action string
}

type catchupResult struct {
	Chats                             []catchupChat
	Queued, WouldQueue, SkippedActive int
}

func (m *historySyncManager) startCatchup(chatID int64, progress store.SyncProgress, since time.Time, aliveAt int64, pause func() time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return false
	}
	if t := m.jobs[chatID]; t != nil && t.active() {
		return false
	}
	if t := m.catchups[chatID]; t != nil && t.active() {
		return false
	}
	m.enqueueLocked(chatID, chatref.Ref{Kind: chatref.ID, ID: chatID}, progress, rpc.HistorySyncCatchup, pause, historyDepth{}, false, laneCatchup)
	task := m.catchups[chatID]
	task.offset = 0
	task.since = since
	task.aliveAt = aliveAt
	return true
}

func (m *historySyncManager) runCatchupTask(task *historySyncTask) {
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
	offset, loaded, minID := 0, 0, 0
	for {
		timer := m.clock.NewTimer(task.pause())
		select {
		case <-ctx.Done():
			timer.Stop()
			m.finish(task, rpc.HistorySyncCancelled, 0, "")
			return
		case <-timer.C():
			timer.Stop()
		}
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
			m.recordPage(task, nil, 0)
			m.finish(task, rpc.HistorySyncDone, 0, "")
			return
		}
		if page.RawNextOffsetID <= 0 || offset > 0 && page.RawNextOffsetID >= offset {
			m.finish(task, rpc.HistorySyncFailed, rpc.CodeTelegram, "history page offset did not advance")
			return
		}
		messages, people, _, err := convertStoredMessages(page.Messages, true)
		if err != nil {
			fail(err)
			return
		}
		if err = m.d.store.SaveCatchupPage(ctx, task.ChatID, messages, people); err != nil {
			fail(err)
			return
		}
		m.recordPage(task, nil, len(messages))
		m.d.voiceSyncPage(ctx, task.ChatID, messages, m.clock.Now())
		stop := false
		for _, msg := range messages {
			if minID == 0 || msg.ID < minID {
				minID = msg.ID
			}
			if msg.Date <= task.since.Unix() {
				stop = true
			}
		}
		loaded += len(messages)
		if stop {
			m.finish(task, rpc.HistorySyncDone, 0, "")
			return
		}
		if loaded >= catchupLimit && minID > 0 {
			if err = m.d.store.MarkCatchupGap(ctx, task.ChatID, minID, task.aliveAt); err != nil {
				fail(err)
				return
			}
			m.finish(task, rpc.HistorySyncDone, 0, "")
			return
		}
		offset = page.RawNextOffsetID
	}
}

// queueCatchup walks recent dialogs, retaining skipped active chats in the result.
func (d *daemon) queueCatchup(ctx context.Context, since time.Time, dryRun bool) (catchupResult, error) {
	result := catchupResult{Chats: []catchupChat{}}
	if !d.opts.CatchupEnabled || d.background == nil || d.getAuthLost() != "" {
		return result, nil
	}
	ctx = throttle.WithBackground(ctx)
	aliveAt := since.Add(catchupOverlap).Unix()
	err := d.walkQueueDialogs(ctx, "", func(chat rpc.Chat) (bool, error) {
		if !d.policy().Allowed(chat.ID) {
			return false, nil
		}
		if chat.LastMessageDate <= since.Unix() {
			return !chat.Pinned, nil
		}
		count, err := d.store.CountChatMessages(ctx, chat.ID)
		if err != nil {
			return false, err
		}
		if count == 0 {
			return false, nil
		}
		entry := catchupChat{Chat: chat}
		if d.sync.activeJob(chat.ID) {
			entry.Action = rpc.HistorySyncActionSkippedActive
			result.SkippedActive++
		} else if dryRun {
			entry.Action = rpc.HistorySyncActionWouldQueue
			result.WouldQueue++
		} else {
			progress, _, err := d.store.SyncState(ctx, chat.ID)
			if err != nil {
				return false, err
			}
			if d.sync.startCatchup(chat.ID, progress, since, aliveAt, d.background.pagePause) {
				entry.Action = rpc.HistorySyncActionQueued
				result.Queued++
			} else {
				entry.Action = rpc.HistorySyncActionSkippedActive
				result.SkippedActive++
			}
		}
		result.Chats = append(result.Chats, entry)
		return false, nil
	})
	return result, err
}

func (d *daemon) autoCatchup(ctx context.Context) {
	if !d.opts.CatchupEnabled || d.background == nil || d.catchupSince == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if d.getAuthLost() != "" {
			return
		}
		if d.backgroundSyncReady() {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	clientCtx := ctx
	release := func() {}
	if d.opts.NewTelegram != nil {
		var client Telegram
		var err error
		clientCtx, client, release, err = d.acquireTelegram(ctx)
		if err != nil {
			return
		}
		clientCtx = context.WithValue(clientCtx, telegramContextKey{}, client)
	}
	defer release()
	result, err := d.queueCatchup(clientCtx, *d.catchupSince, false)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("catchup queue failed", "err", err)
		}
		return
	}
	d.log.Info("catchup queued", "chats", result.Queued, "since", d.catchupSince.Unix())
}
