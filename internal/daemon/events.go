package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

const (
	eventSaveAttempts = 3
	eventRetryPause   = 25 * time.Millisecond
	eventSaveTimeout  = 2 * time.Second
	eventPruneEvery   = time.Hour
	eventRetention    = 7 * 24 * time.Hour
	eventKeep         = 100000
	eventReplayPage   = 500
)

func eventCursor(id int64, pts, qts, date int) string {
	return fmt.Sprintf("%d.%d.%d.%d", id, pts, qts, date)
}

func (d *daemon) cursor(ctx context.Context, id int64) string {
	client := d.telegram(ctx)
	release := func() {}
	if d.opts.NewTelegram != nil {
		var err error
		ctx, client, release, err = d.acquireTelegram(ctx)
		if err != nil {
			return eventCursor(id, 0, 0, 0)
		}
	}
	defer release()
	user, err := client.Self(ctx)
	if err != nil || user == nil {
		return eventCursor(id, 0, 0, 0)
	}
	raw, found, err := d.store.GetAccountState(ctx, fmt.Sprintf("updates.state.%d", user.ID))
	if err != nil || !found {
		return eventCursor(id, 0, 0, 0)
	}
	var state struct {
		Pts  int
		Qts  int
		Date int
	}
	if json.Unmarshal(raw, &state) != nil {
		return eventCursor(id, 0, 0, 0)
	}
	return eventCursor(id, state.Pts, state.Qts, state.Date)
}

func (d *daemon) handleEvent(event rpc.Event) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	defer func() {
		if recover() != nil {
			d.log.Error("event handler panic")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), eventSaveTimeout)
	defer cancel()
	policy := d.policy()
	var deleteChats []int64
	if event.ChatID == nil {
		switch event.Type {
		case rpc.EventStatus:
			var data rpc.StatusData
			if json.Unmarshal(event.Data, &data) == nil && data.UserID != 0 {
				event.ChatID = &data.UserID
			}
		case rpc.EventTyping, rpc.EventRead:
			var data struct {
				ChatID int64 `json:"chat_id"`
			}
			if json.Unmarshal(event.Data, &data) == nil && data.ChatID != 0 {
				event.ChatID = &data.ChatID
			}
		case rpc.EventMessageDelete:
			var data rpc.MessageDeleteData
			if json.Unmarshal(event.Data, &data) != nil {
				if policy.Enabled() {
					d.log.Error("invalid delete event")
					return
				}
				break
			}
			find := d.findMessageChats
			if find == nil {
				find = d.store.ChatsOfMessages
			}
			if !d.retryEventWrite(ctx, func() error {
				var err error
				deleteChats, err = find(ctx, data.IDs)
				return err
			}) {
				return
			}
			if policy.Enabled() {
				if len(deleteChats) == 0 {
					return
				}
				for _, id := range deleteChats {
					if !policy.Allowed(id) {
						return
					}
				}
			}
			if policy.Enabled() && len(deleteChats) == 1 {
				event.ChatID = &deleteChats[0]
			}
		}
	}
	if event.ChatID != nil && !policy.Allowed(*event.ChatID) {
		return
	}
	if event.ChatID == nil && policy.Enabled() && event.Type != rpc.EventGap && event.Type != rpc.EventMessageDelete {
		return
	}
	durable := event.Type == rpc.EventMessageNew || event.Type == rpc.EventMessageEdit || event.Type == rpc.EventMessageDelete || event.Type == rpc.EventReaction
	if !durable {
		event.ID = ""
		d.hub.Publish(event)
		return
	}
	when, err := time.Parse(time.RFC3339, event.Time)
	if err != nil {
		when = time.Now().UTC()
	}
	var id int64
	var inserted bool
	add := d.addEvent
	if add == nil {
		add = d.store.AddEvent
	}
	for attempt := 0; attempt < eventSaveAttempts; attempt++ {
		id, inserted, err = add(ctx, store.Event{Type: event.Type, ChatID: event.ChatID, CreatedAt: when, Payload: event.Data, Raw: event.Raw, DedupKey: event.DedupKey})
		if err == nil {
			break
		}
		if attempt+1 < eventSaveAttempts {
			time.Sleep(eventRetryPause)
		}
	}
	if err != nil {
		d.storageGap()
		return
	}
	switch event.Type {
	case rpc.EventMessageNew, rpc.EventMessageEdit:
		var message rpc.Message
		if json.Unmarshal(event.Data, &message) == nil {
			var update struct {
				Message json.RawMessage `json:"message"`
			}
			if json.Unmarshal(event.Raw, &update) == nil {
				message.Raw = update.Message
			}
			// Ask before the save: afterwards the chat always holds a message.
			freshChat := event.Type == rpc.EventMessageNew && message.ChatID != 0 && d.chatIsNew(ctx, message.ChatID)
			if !d.retryEventWrite(ctx, func() error { return d.persistMessages(ctx, event.Type, []rpc.Message{message}) }) {
				return
			}
			if freshChat {
				d.autoSyncNewChat(message.ChatID)
			}
			// A new voice message goes to the transcription queue once it is saved.
			if event.Type == rpc.EventMessageNew {
				d.voiceIncoming(ctx, message)
			}
		}
	case rpc.EventMessageDelete:
		var data rpc.MessageDeleteData
		if json.Unmarshal(event.Data, &data) == nil {
			chats := deleteChats
			if event.ChatID != nil && len(chats) == 0 {
				chats = []int64{*event.ChatID}
			}
			mark := d.markMessagesDeleted
			if mark == nil {
				mark = d.store.MarkMessagesDeleted
			}
			if len(chats) > 0 && !d.retryEventWrite(ctx, func() error {
				for _, chatID := range chats {
					if _, err := mark(ctx, chatID, data.IDs); err != nil {
						return err
					}
				}
				return nil
			}) {
				return
			}
		}
	}
	if !inserted {
		return
	}
	event.ID = d.cursor(ctx, id)
	d.hub.Publish(event)
}

func (d *daemon) retryEventWrite(ctx context.Context, write func() error) bool {
	for attempt := 0; attempt < eventSaveAttempts; attempt++ {
		if write() == nil {
			return true
		}
		if attempt+1 < eventSaveAttempts {
			time.Sleep(eventRetryPause)
		}
		if ctx.Err() != nil {
			break
		}
	}
	d.storageGap()
	return false
}

func (d *daemon) storageGap() {
	d.log.Error("event storage failed")
	data, _ := json.Marshal(rpc.GapData{Reason: "store"})
	d.hub.Publish(rpc.Event{Type: rpc.EventGap, Time: time.Now().UTC().Format(time.RFC3339), Data: data})
}

func (d *daemon) replayEvents(ctx context.Context, params rpc.SubscribeParams) (rpc.ReplayPlan, error) {
	if params.Local {
		if d.getAuthLost() != "" || !d.account.authorized() {
			return rpc.ReplayPlan{}, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
		}
	} else if err := d.requireAccount(ctx); err != nil {
		return rpc.ReplayPlan{}, err
	}
	filter := rpc.EventFilter{Types: params.Types, Topic: params.Topic}
	storeFilter := store.EventFilter{Types: params.Types}
	for _, ref := range params.Chats {
		id, found, err := d.resolveStoredReference(ctx, ref, false, false)
		if err != nil {
			return rpc.ReplayPlan{}, err
		}
		if !found || !d.policy().Allowed(id) {
			return rpc.ReplayPlan{}, rpc.ErrChatNotFound()
		}
		known, err := d.store.ChatKnown(ctx, id)
		if err != nil {
			return rpc.ReplayPlan{}, err
		}
		if !known {
			return rpc.ReplayPlan{}, rpc.ErrChatNotFound()
		}
		filter.ChatIDs = append(filter.ChatIDs, id)
		storeFilter.ChatIDs = append(storeFilter.ChatIDs, id)
	}
	last, err := d.store.EventHighWater(ctx)
	if err != nil {
		return rpc.ReplayPlan{}, err
	}
	plan := rpc.ReplayPlan{Filter: filter}
	if params.After == "" {
		plan.Cursor = d.cursor(ctx, last)
		return plan, nil
	}
	after, err := rpc.CursorID(params.After)
	if err != nil {
		return rpc.ReplayPlan{}, rpc.NewError(rpc.CodeInvalidParams, "invalid event cursor")
	}
	first, err := d.store.FirstEventID(ctx)
	if err != nil {
		return rpc.ReplayPlan{}, err
	}
	if after > last {
		return rpc.ReplayPlan{}, rpc.NewError(rpc.CodeInvalidParams, "event cursor is ahead of the journal")
	}
	if (first > 0 && after < first-1) || (first == 0 && after < last) {
		return rpc.ReplayPlan{}, rpc.NewError(rpc.CodeCursorExpired, "event cursor expired")
	}
	plan.Cursor = params.After
	position := after
	for position < last {
		page, err := d.store.EventsAfter(ctx, position, storeFilter, eventReplayPage)
		if err != nil {
			return rpc.ReplayPlan{}, err
		}
		if len(page) == 0 {
			break
		}
		for _, entry := range page {
			if entry.ID > last {
				break
			}
			position = entry.ID
			if entry.ChatID != nil && !d.policy().Allowed(*entry.ChatID) {
				continue
			}
			event := rpc.Event{ID: d.cursor(ctx, entry.ID), Type: entry.Type, ChatID: entry.ChatID, Time: entry.CreatedAt.Format(time.RFC3339), Data: entry.Payload}
			if rpc.MatchesTopic(event, params.Topic) {
				plan.Events = append(plan.Events, event)
				plan.Cursor = event.ID
			}
		}
		if len(page) < eventReplayPage || page[len(page)-1].ID >= last {
			break
		}
	}
	first, err = d.store.FirstEventID(ctx)
	if err != nil {
		return rpc.ReplayPlan{}, err
	}
	if (first > 0 && after < first-1) || (first == 0 && after < last) {
		return rpc.ReplayPlan{}, rpc.NewError(rpc.CodeCursorExpired, "event cursor expired")
	}
	plan.Cursor = d.cursor(ctx, last)
	return plan, nil
}

func (d *daemon) pruneEvents(ctx context.Context, every, retention time.Duration, keep int) {
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(ctx, eventSaveTimeout)
		defer cancel()
		if _, err := d.store.PruneEvents(pruneCtx, time.Now().Add(-retention), keep); err != nil {
			d.log.Error("event pruning failed")
		}
	}
	prune()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
