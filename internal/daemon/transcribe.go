package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type transcriptionTelegram interface {
	Transcribe(context.Context, chatref.Ref, int) (rpc.Transcription, error)
	SetTranscriptionSink(func(rpc.Transcription))
}

type transcriptionKey struct {
	chatID    int64
	messageID int
}
type transcriptionWaiter struct {
	updates []rpc.Transcription
	wake    chan struct{}
}

func (d *daemon) handleTranscription(v rpc.Transcription) {
	if !d.policy().Allowed(v.ChatID) {
		return
	}
	d.transcriptionMu.Lock()
	defer d.transcriptionMu.Unlock()
	k := transcriptionKey{v.ChatID, v.ID}
	if w := d.transcriptionWaiters[k]; w != nil {
		w.updates = append(w.updates, v)
		select {
		case w.wake <- struct{}{}:
		default:
		}
		return
	}
	d.applyTranscriptionUpdate(context.Background(), store.Transcription{Raw: v.Raw, ChatID: v.ChatID, MessageID: v.ID, TranscriptionID: v.TranscriptionID, Text: v.Text, Pending: v.Pending})
}

// applyTranscriptionUpdate stores an update that arrived without a waiting
// command. The text of the voice message changed, so the semantic indexer is
// asked for a run (a no-op while it is off).
func (d *daemon) applyTranscriptionUpdate(ctx context.Context, t store.Transcription) {
	if updated, err := d.store.UpdateTranscription(ctx, t); err == nil && updated {
		d.triggerSemanticIndex()
	}
}

// messageTranscribe serves `message transcribe`. By default it goes through the
// local voice queue; only an explicit Telegram request asks Telegram to
// transcribe (messages.transcribeAudio), the single path that does.
func (d *daemon) messageTranscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.TranscribeParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Telegram {
		return d.messageTranscribeTelegram(ctx, p)
	}
	if p.ID <= 0 || p.WaitMS < 0 || p.WaitMS > rpc.MaxTranscribeWaitMS {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive and wait must be between 0 and 600 seconds")
	}
	return d.messageTranscribeLocal(ctx, p)
}

func (d *daemon) messageTranscribeTelegram(ctx context.Context, p rpc.TranscribeParams) (any, error) {
	if p.ID <= 0 || p.WaitMS < 0 || p.WaitMS > rpc.MaxTelegramTranscribeWaitMS {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive and wait must be between 0 and 90 seconds")
	}
	ref, chat, err := d.listChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	ref = chatref.Ref{Kind: chatref.ID, ID: chat.ID}
	release, err := d.transcriptionLocks.lock(ctx, chat.ID, strconv.Itoa(p.ID))
	if err != nil {
		return nil, err
	}
	defer release()
	client, ok := d.telegram(ctx).(transcriptionTelegram)
	if !ok {
		return nil, rpc.NewError(rpc.CodeInternal, "transcription unavailable")
	}
	k := transcriptionKey{chat.ID, p.ID}
	w := &transcriptionWaiter{wake: make(chan struct{}, 1)}
	d.transcriptionMu.Lock()
	if d.transcriptionWaiters == nil {
		d.transcriptionWaiters = make(map[transcriptionKey]*transcriptionWaiter)
	}
	d.transcriptionWaiters[k] = w
	d.transcriptionMu.Unlock()
	defer func() {
		d.transcriptionMu.Lock()
		for _, u := range w.updates {
			if d.policy().Allowed(u.ChatID) {
				d.applyTranscriptionUpdate(context.Background(), store.Transcription{Raw: u.Raw, ChatID: u.ChatID, MessageID: u.ID, TranscriptionID: u.TranscriptionID, Text: u.Text, Pending: u.Pending})
			}
		}
		delete(d.transcriptionWaiters, k)
		d.transcriptionMu.Unlock()
	}()

	stored, err := d.store.GetTranscription(ctx, chat.ID, p.ID)
	// A text the local engine wrote is not Telegram's: an explicit Telegram
	// request asks Telegram and replaces it.
	if err == nil && stored.Source == store.TranscriptionSourceLocal {
		err = store.ErrNotFound
	}
	var result rpc.Transcription
	if err == nil {
		result = rpc.Transcription{ChatID: chat.ID, ID: p.ID, TranscriptionID: stored.TranscriptionID, Text: stored.Text, Pending: stored.Pending, Cached: !stored.Pending, Source: rpc.TranscriptionSourceTelegram}
		if stored.Pending {
			result, err = client.Transcribe(ctx, ref, p.ID)
			if err != nil {
				return nil, err
			}
			result.Source = rpc.TranscriptionSourceTelegram
			if result.ChatID != chat.ID || result.ID != p.ID {
				return nil, rpc.ErrChatNotFound()
			}
			if err = d.store.PutTranscription(ctx, store.Transcription{Raw: result.Raw, ChatID: chat.ID, MessageID: p.ID, TranscriptionID: result.TranscriptionID, Text: result.Text, Pending: result.Pending}); err != nil {
				return nil, err
			}
			if !result.Pending {
				d.triggerSemanticIndex()
			}
		}
	} else if errors.Is(err, store.ErrNotFound) {
		msg, e := d.telegram(ctx).Message(ctx, ref, p.ID)
		if e != nil {
			return nil, e
		}
		if msg.ChatID != chat.ID || !d.policy().Allowed(chat.ID) {
			return nil, rpc.ErrChatNotFound()
		}
		if msg.MediaType != "voice" && msg.MediaType != "video_note" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "message has no voice or round video")
		}
		result, err = client.Transcribe(ctx, ref, p.ID)
		if err != nil {
			return nil, err
		}
		result.Source = rpc.TranscriptionSourceTelegram
		if result.ChatID != chat.ID || result.ID != p.ID {
			return nil, rpc.ErrChatNotFound()
		}
		if err = d.store.PutTranscription(ctx, store.Transcription{Raw: result.Raw, ChatID: chat.ID, MessageID: p.ID, TranscriptionID: result.TranscriptionID, Text: result.Text, Pending: result.Pending}); err != nil {
			return nil, err
		}
		if !result.Pending {
			d.triggerSemanticIndex()
		}
	} else {
		return nil, err
	}
	deadline := time.NewTimer(time.Duration(p.WaitMS) * time.Millisecond)
	defer deadline.Stop()
	for {
		d.transcriptionMu.Lock()
		updates := w.updates
		w.updates = nil
		for _, u := range updates {
			if !d.policy().Allowed(chat.ID) {
				err = rpc.ErrChatNotFound()
				break
			}
			if u.TranscriptionID != result.TranscriptionID {
				continue
			}
			var updated bool
			if updated, err = d.store.UpdateTranscription(ctx, store.Transcription{Raw: u.Raw, ChatID: chat.ID, MessageID: p.ID, TranscriptionID: u.TranscriptionID, Text: u.Text, Pending: u.Pending}); err != nil {
				break
			}
			if updated {
				result.Text, result.Pending = u.Text, u.Pending
				d.triggerSemanticIndex()
			}
		}
		d.transcriptionMu.Unlock()
		if err != nil {
			return nil, err
		}
		if !result.Pending {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			if !d.policy().Allowed(chat.ID) {
				return nil, rpc.ErrChatNotFound()
			}
			return result, nil
		case <-w.wake:
		}
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	return result, nil
}
