package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func (d *daemon) registerVoice() {
	d.handle(rpc.MethodChatVoiceOn, d.chatVoiceOn)
	d.handle(rpc.MethodChatVoiceOff, d.chatVoiceOff)
	d.handle(rpc.MethodChatVoiceList, d.chatVoiceList)
	d.handle(rpc.MethodVoiceStatus, d.voiceStatus)
	d.handle(rpc.MethodVoiceCancel, d.voiceCancel)
	d.handle(rpc.MethodVoiceRetry, d.voiceRetry)
	d.handle(rpc.MethodVoiceBackfill, d.voiceBackfill)
}

// voiceQueue returns the worker, or an error in a daemon built without one.
func (d *daemon) voiceQueue() (*voiceWorker, error) {
	if d.voice == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "the voice queue is unavailable")
	}
	return d.voice, nil
}

// voiceInstallError is the error of a command that needs the engine when it is
// not installed.
func (w *voiceWorker) voiceInstallError() error {
	missing := w.engine.Missing(w.cfg.FFmpeg)
	if len(missing) == 0 {
		return nil
	}
	hint := "run `tg voice install`"
	for _, m := range missing {
		if m == "ffmpeg" {
			hint = "install ffmpeg or set [voice_transcription] ffmpeg, then run `tg voice install`"
			break
		}
	}
	return &rpc.Error{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf("the local speech engine is not installed (missing: %s): %s", strings.Join(missing, ", "), hint), Data: map[string]any{"reason": "engine_missing"}}
}

func (d *daemon) voiceStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.VoiceStatusParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	return w.status(ctx)
}

// voiceTarget resolves the chat of a cancel or retry command from the local
// database, without Telegram: a numeric id, or a username the database knows. A
// closed chat is not found.
func (d *daemon) voiceTarget(ctx context.Context, value string) (int64, error) {
	ref, err := parseChatNoInvite(value)
	if err != nil {
		return 0, err
	}
	policy := d.policy()
	switch ref.Kind {
	case chatref.ID:
		if !policy.Allowed(ref.ID) {
			return 0, rpc.ErrUnknownChatID()
		}
		known, err := d.store.ChatKnown(ctx, ref.ID)
		if err != nil {
			return 0, err
		}
		if !known {
			return 0, rpc.ErrUnknownChatID()
		}
		return ref.ID, nil
	case chatref.Username:
		chat, err := d.store.FindChatByUsername(ctx, ref.Username)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !policy.Allowed(chat.ID)) {
			return 0, rpc.ErrChatNotFound()
		}
		if err != nil {
			return 0, err
		}
		return chat.ID, nil
	}
	return 0, rpc.NewError(rpc.CodeInvalidParams, "give a numeric chat id or a @username the local database knows")
}

func (d *daemon) voiceCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.VoiceCancelParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" && p.ID == 0 {
		w, err := d.voiceQueue()
		if err != nil {
			return nil, err
		}
		skipped, err := d.store.CancelAllVoice(ctx)
		if err != nil {
			return nil, err
		}
		return rpc.VoiceCancelResult{Skipped: skipped, Stopped: w.cancel(0, 0, true)}, nil
	}
	if p.Chat == "" || p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "give a chat and a positive message id, or neither to cancel everything")
	}
	chatID, err := d.voiceTarget(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	if _, err := d.store.GetVoiceJob(ctx, chatID, p.ID); errors.Is(err, store.ErrNotFound) {
		return nil, rpc.NewError(rpc.CodeNotFound, "there is no voice job for this message")
	} else if err != nil {
		return nil, err
	}
	cancelled, err := d.store.CancelVoice(ctx, chatID, p.ID)
	if err != nil {
		return nil, err
	}
	res := rpc.VoiceCancelResult{Stopped: w.cancel(chatID, p.ID, false)}
	if cancelled {
		res.Skipped = 1
	}
	return res, nil
}

// voiceKind is the queue kind of a message, or an error when it carries no voice
// message, video note or video.
func voiceKind(mediaType *string, deleted *int64) (string, error) {
	if deleted == nil && mediaType != nil {
		switch *mediaType {
		case store.VoiceKindVoice, store.VoiceKindVideoNote, store.VoiceKindVideo:
			return *mediaType, nil
		}
	}
	return "", rpc.NewError(rpc.CodeInvalidParams, "message has no voice message, round video or video")
}

func (d *daemon) voiceRetry(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.VoiceRetryParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" || p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "give a chat and a positive message id")
	}
	chatID, err := d.voiceTarget(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	msg, err := d.store.GetMessage(ctx, chatID, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, rpc.NewError(rpc.CodeNotFound, "the message is not in the local database")
	}
	if err != nil {
		return nil, err
	}
	kind, err := voiceKind(msg.MediaType, msg.DeletedAt)
	if err != nil {
		return nil, err
	}
	if err := w.voiceInstallError(); err != nil {
		return nil, err
	}
	outcome, err := w.enqueue(ctx, store.VoiceJob{ChatID: chatID, MessageID: p.ID, Kind: kind, Source: store.VoiceSourceManual, Priority: store.VoicePriorityManual}, true)
	if err != nil {
		return nil, err
	}
	job, err := d.store.GetVoiceJob(ctx, chatID, p.ID)
	if err != nil {
		return nil, err
	}
	return rpc.VoiceRetryResult{ChatID: chatID, ID: p.ID, Kind: kind, Queued: outcome != store.VoiceUnchanged, State: job.State}, nil
}

func (d *daemon) voiceBackfill(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.VoiceBackfillParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	res := rpc.VoiceBackfillResult{Enabled: w.cfg.Enabled, DryRun: p.DryRun}
	if !p.DryRun {
		if !w.cfg.Enabled {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "voice transcription is off: set [voice_transcription] enabled = true")
		}
		if err := w.voiceInstallError(); err != nil {
			return nil, err
		}
	}
	if res.Chats, err = d.store.VoicePrivateChats(ctx, chatFilter(d.policy())); err != nil {
		return nil, err
	}
	// One query selects the candidates of every personal chat the access lists
	// leave open: a message with no finished text and no job row (a failed one is
	// queued again by `voice retry`), not the owner's, not deleted. Videos are
	// left out.
	res.Candidates, res.Queued, err = d.backfillVoice(ctx, w, store.VoiceCandidateQuery{Private: true, Kinds: voiceBackfillKinds(false)}, p.DryRun)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// messageTranscribeLocal serves `message transcribe` through the voice queue:
// the stored text when there is one, otherwise a manual job at the front of the
// queue, awaited up to the requested time. It never asks Telegram to transcribe;
// it reads the message from Telegram only when the database does not hold it.
func (d *daemon) messageTranscribeLocal(ctx context.Context, p rpc.TranscribeParams) (any, error) {
	ref, chat, err := d.listChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	ref = chatref.Ref{Kind: chatref.ID, ID: chat.ID}
	stored, err := d.store.GetTranscription(ctx, chat.ID, p.ID)
	switch {
	case err == nil && !stored.Pending:
		return voiceTranscription(stored, true, false), nil
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	w, err := d.voiceQueue()
	if err != nil {
		return nil, err
	}
	if err := w.voiceInstallError(); err != nil {
		return nil, err
	}
	kind, err := d.messageKind(ctx, ref, chat.ID, p.ID)
	if err != nil {
		return nil, err
	}
	if _, err := w.enqueue(ctx, store.VoiceJob{ChatID: chat.ID, MessageID: p.ID, Kind: kind, Source: store.VoiceSourceManual, Priority: store.VoicePriorityManual}, true); err != nil {
		return nil, err
	}
	return d.awaitVoiceJob(ctx, w, chat.ID, p.ID, time.Duration(p.WaitMS)*time.Millisecond)
}

// messageKind finds the queue kind of a message in the database, and reads the
// message from Telegram, saving it, when the database does not have it yet.
func (d *daemon) messageKind(ctx context.Context, ref chatref.Ref, chatID int64, id int) (string, error) {
	msg, err := d.store.GetMessage(ctx, chatID, id)
	if err == nil {
		return voiceKind(msg.MediaType, msg.DeletedAt)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	m, err := d.telegram(ctx).Message(ctx, ref, id)
	if err != nil {
		return "", err
	}
	if m.ChatID != chatID || !d.policy().Allowed(chatID) {
		return "", rpc.ErrChatNotFound()
	}
	d.saveMessages(ctx, rpc.MethodMessageTranscribe, []rpc.Message{m})
	if _, err := d.store.GetMessage(ctx, chatID, id); err != nil {
		return "", rpc.NewError(rpc.CodeInternal, "the message could not be saved")
	}
	var media *string
	if m.MediaType != "" {
		media = &m.MediaType
	}
	return voiceKind(media, nil)
}

func voiceTranscription(t store.Transcription, cached, queued bool) rpc.Transcription {
	return rpc.Transcription{
		ChatID: t.ChatID, ID: t.MessageID, TranscriptionID: t.TranscriptionID, Text: t.Text,
		Pending: t.Pending, Cached: cached, Source: t.Source, Engine: t.Engine, Queued: queued,
	}
}

// awaitVoiceJob waits for the job of a message to end, up to wait, and answers
// with the stored text, an error that names the reason, or pending.
func (d *daemon) awaitVoiceJob(ctx context.Context, w *voiceWorker, chatID int64, id int, wait time.Duration) (any, error) {
	timer := w.clock.NewTimer(wait)
	defer timer.Stop()
	for {
		changed := w.changes()
		job, err := d.store.GetVoiceJob(ctx, chatID, id)
		if err != nil {
			return nil, err
		}
		if !d.policy().Allowed(chatID) {
			return nil, rpc.ErrChatNotFound()
		}
		switch job.State {
		case store.VoiceStateDone:
			t, err := d.store.GetTranscription(ctx, chatID, id)
			if err != nil {
				return nil, rpc.NewError(rpc.CodeInternal, "the transcription was not saved")
			}
			return voiceTranscription(t, false, true), nil
		case store.VoiceStateFailed, store.VoiceStateSkipped:
			return nil, voiceJobError(job)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C():
			return rpc.Transcription{ChatID: chatID, ID: id, Pending: true, Queued: true, Source: rpc.TranscriptionSourceLocal, Engine: voiceEngineName}, nil
		case <-changed:
		}
	}
}

// voiceJobError is the error a command reports for a job that did not produce
// a text. The reason is the machine word of the queue row.
func voiceJobError(job store.VoiceJob) error {
	data := map[string]any{"reason": job.Reason}
	switch {
	case job.State == store.VoiceStateSkipped && job.Reason == "closed":
		return rpc.ErrChatNotFound()
	case job.State == store.VoiceStateSkipped && job.Reason == store.VoiceReasonCancelled:
		return &rpc.Error{Code: rpc.CodeInternal, Message: "the transcription was cancelled", Data: data}
	case job.State == store.VoiceStateSkipped:
		return &rpc.Error{Code: rpc.CodeInvalidParams, Message: "the message was skipped: " + job.Reason, Data: data}
	}
	return &rpc.Error{Code: rpc.CodeInternal, Message: "the transcription failed: " + job.Reason, Data: data}
}
