package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// voiceSyncMaxAge is how old a message may be for a history sync to queue it;
// older ones wait for an explicit backfill.
const voiceSyncMaxAge = 90 * 24 * time.Hour

// voiceMessage is what a source needs to know about a saved message.
type voiceMessage struct {
	ID        int
	MediaType string
	Out       bool
}

// voiceSourceKind is the single rule of the sources: the queue kind of a message
// the chat's rule lets in, or false. The caller has checked that access.json
// leaves the chat open.
//   - The owner's own messages are never queued.
//   - A voice message or video note needs a personal chat or a mark.
//   - A video needs a mark with the video flag.
func voiceSourceKind(rule store.VoiceChatRule, m voiceMessage) (string, bool) {
	if m.Out {
		return "", false
	}
	switch m.MediaType {
	case store.VoiceKindVoice, store.VoiceKindVideoNote:
		return m.MediaType, rule.Private || rule.Marked
	case store.VoiceKindVideo:
		return m.MediaType, rule.Marked && rule.Video
	}
	return "", false
}

// voiceQueueKind reports whether the media type is one the queue knows.
func voiceQueueKind(mediaType string) bool {
	switch mediaType {
	case store.VoiceKindVoice, store.VoiceKindVideoNote, store.VoiceKindVideo:
		return true
	}
	return false
}

// sourcesOn reports whether the sources may queue jobs by themselves. Without
// the setting no source reads the queue, the chat rules or Telegram.
func (w *voiceWorker) sourcesOn() bool { return w != nil && w.cfg.Enabled }

// enqueueVoiceSource queues the messages of one chat the rule accepts, with the
// given source and priority. It runs after the messages are saved, makes no
// Telegram request and never fails its caller: a storage error is logged without
// message text and the messages stay for a backfill. A duplicate is not queued
// twice (the queue keeps one row per message).
func (d *daemon) enqueueVoiceSource(ctx context.Context, chatID int64, messages []voiceMessage, source string, priority int) {
	w := d.voice
	if !w.sourcesOn() {
		return
	}
	var candidates []voiceMessage
	for _, m := range messages {
		if !m.Out && voiceQueueKind(m.MediaType) {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 || !d.policy().Allowed(chatID) {
		return
	}
	rule, err := d.store.VoiceChatRule(ctx, chatID)
	if err != nil {
		d.log.Warn("voice source could not read the chat rule", "source", source)
		return
	}
	for _, m := range candidates {
		kind, ok := voiceSourceKind(rule, m)
		if !ok {
			continue
		}
		job := store.VoiceJob{ChatID: chatID, MessageID: m.ID, Kind: kind, Source: source, Priority: priority}
		if _, err := w.enqueue(ctx, job, false); err != nil {
			d.log.Warn("voice source could not queue a job", "source", source)
			return
		}
	}
}

// voiceIncoming queues a new message of the event stream. The message is saved
// already: a job for a message without a row would have nothing to work on.
func (d *daemon) voiceIncoming(ctx context.Context, message rpc.Message) {
	d.enqueueVoiceSource(ctx, message.ChatID, []voiceMessage{{ID: message.ID, MediaType: message.MediaType, Out: message.Out}}, store.VoiceSourceIncoming, store.VoicePriorityIncoming)
}

// voiceSyncPage queues the messages a history sync just saved, the ones from the
// last 90 days (by the date of the message). It takes the stored form of the
// page, so nothing is read from Telegram or from the database for the filter.
func (d *daemon) voiceSyncPage(ctx context.Context, chatID int64, saved []store.Message, now time.Time) {
	if !d.voice.sourcesOn() {
		return
	}
	// The sync may be cancelled right after its page is saved; the page is
	// still queued, so the work does not depend on that context.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	cutoff := now.Add(-voiceSyncMaxAge).Unix()
	var messages []voiceMessage
	for _, m := range saved {
		if m.MediaType == nil || m.Date < cutoff {
			continue
		}
		messages = append(messages, voiceMessage{ID: m.ID, MediaType: *m.MediaType, Out: m.Out != nil && *m.Out})
	}
	d.enqueueVoiceSource(ctx, chatID, messages, store.VoiceSourceSync, store.VoicePrioritySync)
}

// voiceBackfillKinds are the kinds a backfill queues: voice messages and video
// notes, and videos only when asked.
func voiceBackfillKinds(video bool) []string {
	kinds := []string{store.VoiceKindVoice, store.VoiceKindVideoNote}
	if video {
		kinds = append(kinds, store.VoiceKindVideo)
	}
	return kinds
}

// backfillVoice walks the saved messages q selects, page by page by key, and
// queues each one with the backfill source. A dry run only counts. It returns
// how many candidates it found and how many jobs it added.
func (d *daemon) backfillVoice(ctx context.Context, w *voiceWorker, q store.VoiceCandidateQuery, dryRun bool) (candidates, queued int, err error) {
	q.Limit = store.MaxExportPageSize
	for {
		q.Filter = chatFilter(d.policy())
		page, err := d.store.VoiceCandidates(ctx, q)
		if err != nil {
			return candidates, queued, err
		}
		for _, c := range page {
			candidates++
			if dryRun {
				continue
			}
			job := store.VoiceJob{ChatID: c.ChatID, MessageID: c.MessageID, Kind: c.Kind, Source: store.VoiceSourceBackfill, Priority: store.VoicePriorityBackfill}
			outcome, err := w.enqueue(ctx, job, false)
			if err != nil {
				return candidates, queued, err
			}
			if outcome == store.VoiceEnqueued {
				queued++
			}
		}
		if len(page) < q.Limit {
			return candidates, queued, nil
		}
		q.After = &page[len(page)-1]
	}
}

func (d *daemon) chatVoiceOn(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatVoiceOnParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "give a chat")
	}
	chatID, err := d.voiceTarget(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	if err := d.store.SetVoiceChat(ctx, chatID, p.Video, w.clock.Now()); err != nil {
		return nil, err
	}
	mark, err := d.store.GetVoiceChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	res := rpc.ChatVoiceOnResult{ChatID: chatID, Video: mark.Video, EnabledAt: mark.EnabledAt, Enabled: w.cfg.Enabled}
	if !p.Backfill {
		return res, nil
	}
	// The mark stays when the backfill is refused: it does not depend on it.
	if !w.cfg.Enabled {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "the chat is marked, but voice transcription is off: set voice_transcription.enabled = true, then repeat with --backfill", Data: map[string]any{"reason": "disabled"}}
	}
	if err := w.voiceInstallError(); err != nil {
		return nil, err
	}
	res.Candidates, res.Queued, err = d.backfillVoice(ctx, w, store.VoiceCandidateQuery{ChatID: chatID, Kinds: voiceBackfillKinds(mark.Video)}, false)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (d *daemon) chatVoiceOff(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatVoiceOffParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "give a chat")
	}
	chatID, err := d.voiceTarget(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	removed, err := d.store.ClearVoiceChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return rpc.ChatVoiceOffResult{ChatID: chatID, Removed: removed}, nil
}

func (d *daemon) chatVoiceList(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatVoiceListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	marks, err := d.store.ListVoiceChats(ctx)
	if err != nil {
		return nil, err
	}
	policy := d.policy()
	res := rpc.ChatVoiceListResult{Enabled: w.cfg.Enabled, Chats: []rpc.ChatVoiceEntry{}}
	for _, mark := range marks {
		if !policy.Allowed(mark.ChatID) {
			continue
		}
		entry := rpc.ChatVoiceEntry{ChatID: mark.ChatID, Video: mark.Video, EnabledAt: mark.EnabledAt}
		chat, err := d.store.GetChat(ctx, mark.ChatID)
		switch {
		case err == nil:
			entry.Title = chat.Title
			if chat.Username != nil {
				entry.Username = *chat.Username
			}
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
		res.Chats = append(res.Chats, entry)
	}
	return res, nil
}

// voiceStatusBlock is the voice block of the daemon status and the dashboard.
// It reads memory, the engine files and the queue counters of the database and
// never calls Telegram. A daemon without a worker, or a database that cannot be
// read, shows the block as it would be with the feature off: a status request
// must not fail because of the queue.
func (d *daemon) voiceStatusBlock(ctx context.Context) rpc.VoiceStatus {
	if d.voice == nil {
		return rpc.VoiceStatus{State: rpc.VoiceStateDisabled}
	}
	st, err := d.voice.status(ctx)
	if err != nil {
		d.log.Warn("voice status could not be read")
		return rpc.VoiceStatus{Enabled: d.voice.cfg.Enabled, State: rpc.VoiceStateDisabled}
	}
	return st
}
