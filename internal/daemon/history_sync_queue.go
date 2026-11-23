package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

const (
	defaultQueueChats = 50
	maxQueueChats     = 500
	maxQueueActive    = 36500
	queueDialogPage   = 100
)

// queueCandidate is a chat picked for the batch with its saved progress.
type queueCandidate struct {
	chat     rpc.Chat
	progress store.SyncProgress
}

// historySyncQueue puts a batch of chats in the history queue. It reads the
// dialog list of Telegram from the newest dialog to the oldest, keeps the chats
// the access lists show and the filters accept, and starts a load for each, or
// only reports them with dry_run. A closed chat is neither queued nor reported.
func (d *daemon) historySyncQueue(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.HistorySyncQueueParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if len(p.Chats) > 0 {
		if p.Type != "" || p.ActiveDays != 0 || p.Unsynced || p.MaxChats != 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "name chats or filter the dialog list, not both: chats cannot be combined with type, active_days, unsynced or max_chats")
		}
		if len(p.Chats) > maxQueueNamed {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "chats must hold at most 100 chats")
		}
		depth, err := parseHistoryDepth(p.Messages, p.Since, d.sync.clock.Now())
		if err != nil {
			return nil, err
		}
		if err := d.requireAccount(ctx); err != nil {
			return nil, err
		}
		return d.queueNamedChats(ctx, p, depth)
	}
	switch p.Type {
	case "", "user", "bot", "group", "supergroup", "channel":
	default:
		return nil, rpc.NewError(rpc.CodeInvalidParams, "type must be user, bot, group, supergroup or channel")
	}
	if p.ActiveDays < 0 || p.ActiveDays > maxQueueActive {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "active_days must be between 1 and 36500")
	}
	if p.MaxChats == 0 {
		p.MaxChats = defaultQueueChats
	}
	if p.MaxChats < 1 || p.MaxChats > maxQueueChats {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "max_chats must be between 1 and 500")
	}
	now := d.sync.clock.Now()
	depth, err := parseHistoryDepth(p.Messages, p.Since, now)
	if err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	var cutoff int64
	if p.ActiveDays > 0 {
		cutoff = now.AddDate(0, 0, -p.ActiveDays).Unix()
	}
	result := rpc.HistorySyncQueueResult{DryRun: p.DryRun, Chats: []rpc.HistorySyncQueueChat{}}
	picked, err := d.pickQueueChats(ctx, p, depth, cutoff, &result)
	if err != nil {
		return nil, err
	}
	for _, c := range picked {
		entry := rpc.HistorySyncQueueChat{
			ChatID: c.chat.ID, Title: c.chat.Title, LastMessageDate: c.chat.LastMessageDate,
			Action: rpc.HistorySyncActionWouldQueue,
		}
		if p.DryRun {
			result.WouldQueue++
		} else {
			ref := chatref.Ref{Kind: chatref.ID, ID: c.chat.ID}
			if _, err := d.sync.startDepth(c.chat.ID, ref, c.progress, depth, false); err != nil {
				return nil, err
			}
			entry.Action = rpc.HistorySyncActionQueued
			result.Queued++
		}
		result.Chats = append(result.Chats, entry)
	}
	return result, nil
}

// walkQueueDialogs shares the paginated, access-filtered dialog walk.
func (d *daemon) walkQueueDialogs(ctx context.Context, chatType string, visit func(rpc.Chat) (bool, error)) error {
	client, policy := d.telegram(ctx), d.policy()
	after := rpc.DialogOffset{}
	for {
		chats, next, err := client.Chats(ctx, queueDialogPage, chatType, after, policy.Allowed)
		if err != nil {
			return err
		}
		for _, chat := range chats {
			stop, err := visit(chat)
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
		if next == nil {
			return nil
		}
		after = *next
	}
}

// pickQueueChats selects up to MaxChats, skipping old pinned dialogs and stopping
// at the first old unpinned one.
func (d *daemon) pickQueueChats(ctx context.Context, p rpc.HistorySyncQueueParams, depth historyDepth, cutoff int64, result *rpc.HistorySyncQueueResult) ([]queueCandidate, error) {
	var picked []queueCandidate
	err := d.walkQueueDialogs(ctx, p.Type, func(chat rpc.Chat) (bool, error) {
		if cutoff > 0 && chat.LastMessageDate < cutoff {
			return !chat.Pinned, nil
		}
		progress, found, err := d.store.SyncState(ctx, chat.ID)
		if err != nil {
			return false, err
		}
		switch {
		case progress.Complete:
			result.SkippedLoaded++
			return false, nil
		case p.Unsynced && found:
			result.SkippedLoaded++
			return false, nil
		case d.sync.activeJob(chat.ID):
			result.SkippedActive++
			return false, nil
		}
		if depth.active() {
			count, oldest, err := d.store.MessageBounds(ctx, chat.ID)
			if err != nil {
				return false, err
			}
			if depth.satisfied(count, oldest) {
				result.SkippedLoaded++
				return false, nil
			}
		}
		picked = append(picked, queueCandidate{chat: chat, progress: progress})
		return len(picked) == p.MaxChats, nil
	})
	if err != nil {
		return nil, err
	}
	return picked, nil
}

// maxQueueNamed bounds the chats of a request that names them.
const maxQueueNamed = 100

// queueNamedChats queues the chats a request names. Every chat is resolved and
// checked before anything is queued, so a closed or unknown chat fails the
// whole call with its own error and leaves the queue as it was. A chat that is
// loaded enough already, or has a job, is reported with a skip action.
func (d *daemon) queueNamedChats(ctx context.Context, p rpc.HistorySyncQueueParams, depth historyDepth) (rpc.HistorySyncQueueResult, error) {
	type named struct {
		ref      chatref.Ref
		id       int64
		progress store.SyncProgress
	}
	var items []named
	seen := make(map[int64]bool, len(p.Chats))
	for _, name := range p.Chats {
		if name == "" {
			return rpc.HistorySyncQueueResult{}, rpc.NewError(rpc.CodeInvalidParams, "give a chat")
		}
		ref, id, err := d.resolveHistorySyncChat(ctx, name)
		if err != nil {
			return rpc.HistorySyncQueueResult{}, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		progress, _, err := d.store.SyncState(ctx, id)
		if err != nil {
			return rpc.HistorySyncQueueResult{}, err
		}
		items = append(items, named{ref: ref, id: id, progress: progress})
	}
	result := rpc.HistorySyncQueueResult{DryRun: p.DryRun, Chats: []rpc.HistorySyncQueueChat{}}
	for _, item := range items {
		entry := rpc.HistorySyncQueueChat{ChatID: item.id}
		switch chat, err := d.store.GetChat(ctx, item.id); {
		case err == nil:
			entry.Title = chat.Title
		case !errors.Is(err, store.ErrNotFound):
			return rpc.HistorySyncQueueResult{}, err
		}
		skip := ""
		switch {
		case item.progress.Complete:
			skip, entry.Action = "loaded", rpc.HistorySyncActionSkippedLoaded
		case d.sync.activeJob(item.id):
			skip, entry.Action = "active", rpc.HistorySyncActionSkippedActive
		case depth.active():
			count, oldest, err := d.store.MessageBounds(ctx, item.id)
			if err != nil {
				return rpc.HistorySyncQueueResult{}, err
			}
			if depth.satisfied(count, oldest) {
				skip, entry.Action = "loaded", rpc.HistorySyncActionSkippedLoaded
			}
		}
		switch skip {
		case "loaded":
			result.SkippedLoaded++
		case "active":
			result.SkippedActive++
		default:
			if p.DryRun {
				entry.Action = rpc.HistorySyncActionWouldQueue
				result.WouldQueue++
			} else {
				if _, err := d.sync.startDepth(item.id, item.ref, item.progress, depth, false); err != nil {
					return rpc.HistorySyncQueueResult{}, err
				}
				entry.Action = rpc.HistorySyncActionQueued
				result.Queued++
			}
		}
		result.Chats = append(result.Chats, entry)
	}
	return result, nil
}
