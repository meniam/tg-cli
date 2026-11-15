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
	_, _ = d.store.UpdateTranscription(context.Background(), store.Transcription{ChatID: v.ChatID, MessageID: v.ID, TranscriptionID: v.TranscriptionID, Text: v.Text, Pending: v.Pending})
}

func (d *daemon) messageTranscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.TranscribeParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 || p.WaitMS < 0 || p.WaitMS > 90000 {
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
	client, ok := d.opts.Telegram.(transcriptionTelegram)
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
				_, _ = d.store.UpdateTranscription(context.Background(), store.Transcription{ChatID: u.ChatID, MessageID: u.ID, TranscriptionID: u.TranscriptionID, Text: u.Text, Pending: u.Pending})
			}
		}
		delete(d.transcriptionWaiters, k)
		d.transcriptionMu.Unlock()
	}()

	stored, err := d.store.GetTranscription(ctx, chat.ID, p.ID)
	var result rpc.Transcription
	if err == nil {
		result = rpc.Transcription{ChatID: chat.ID, ID: p.ID, TranscriptionID: stored.TranscriptionID, Text: stored.Text, Pending: stored.Pending, Cached: !stored.Pending}
		if stored.Pending {
			result, err = client.Transcribe(ctx, ref, p.ID)
			if err != nil {
				return nil, err
			}
			if result.ChatID != chat.ID || result.ID != p.ID {
				return nil, rpc.ErrChatNotFound()
			}
			if err = d.store.PutTranscription(ctx, store.Transcription{ChatID: chat.ID, MessageID: p.ID, TranscriptionID: result.TranscriptionID, Text: result.Text, Pending: result.Pending}); err != nil {
				return nil, err
			}
		}
	} else if errors.Is(err, store.ErrNotFound) {
		msg, e := d.opts.Telegram.Message(ctx, ref, p.ID)
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
		if result.ChatID != chat.ID || result.ID != p.ID {
			return nil, rpc.ErrChatNotFound()
		}
		if err = d.store.PutTranscription(ctx, store.Transcription{ChatID: chat.ID, MessageID: p.ID, TranscriptionID: result.TranscriptionID, Text: result.Text, Pending: result.Pending}); err != nil {
			return nil, err
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
			if updated, err = d.store.UpdateTranscription(ctx, store.Transcription{ChatID: chat.ID, MessageID: p.ID, TranscriptionID: u.TranscriptionID, Text: u.Text, Pending: u.Pending}); err != nil {
				break
			}
			if updated {
				result.Text, result.Pending = u.Text, u.Pending
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
