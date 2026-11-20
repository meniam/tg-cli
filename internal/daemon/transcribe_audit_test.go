package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func audit06TranscribeCase(t *testing.T) (*daemon, *transcriptionFake, *store.DB) {
	t.Helper()
	d, base, db, _ := accessTestDaemon(t)
	base.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "voice"}}
	f := &transcriptionFake{fakeTelegram: base, answer: rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Pending: true}}
	d.opts.Telegram = f
	return d, f, db
}

// An update for another transcription id must not complete this request.
func TestAudit06_TranscribeIgnoresForeignTranscriptionID(t *testing.T) {
	d, f, _ := audit06TranscribeCase(t)
	f.onCall = func() {
		d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 5, Text: "foreign"})
	}
	v, err := d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1, WaitMS: 1}))
	got := v.(rpc.Transcription)
	if err != nil || !got.Pending || got.Text != "" {
		t.Fatalf("foreign update: %+v %v", got, err)
	}
}

// With no update, a positive WaitMS ends at the deadline with the pending answer.
func TestAudit06_TranscribeWaitExpires(t *testing.T) {
	d, f, _ := audit06TranscribeCase(t)
	v, err := d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1, WaitMS: 1}))
	got := v.(rpc.Transcription)
	if err != nil || !got.Pending || f.calls != 1 {
		t.Fatalf("expired wait: %+v %v calls=%d", got, err, f.calls)
	}
}

// A canceled context ends the wait with its error instead of the full WaitMS.
// The cancel comes once the pending row is stored, that is, when the request is
// about to wait, so the error cannot come from the store write.
func TestAudit06_TranscribeCanceledWhileWaiting(t *testing.T) {
	d, _, db := audit06TranscribeCase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := d.messageTranscribe(ctx, params(t, rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1, WaitMS: 90000}))
		done <- err
	}()
	safety := time.After(10 * time.Second)
	for {
		if _, err := db.GetTranscription(context.Background(), 77, 1); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the request ended before waiting: %v", err)
		case <-safety:
			t.Fatal("the pending transcription was never stored")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait: %v", err)
		}
	case <-safety:
		t.Fatal("the canceled request kept waiting")
	}
}

// The update arrives after the pending row is stored, that is, when the request
// is about to wait or already waits; the request must return the text.
func TestAudit06_TranscribeUpdateAfterPendingStored(t *testing.T) {
	d, _, db := audit06TranscribeCase(t)
	done := make(chan rpc.Transcription, 1)
	errs := make(chan error, 1)
	go func() {
		v, err := d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1, WaitMS: 90000}))
		if err != nil {
			errs <- err
			return
		}
		done <- v.(rpc.Transcription)
	}()
	safety := time.After(10 * time.Second)
	for {
		if _, err := db.GetTranscription(context.Background(), 77, 1); err == nil {
			break
		}
		select {
		case err := <-errs:
			t.Fatalf("the request failed before waiting: %v", err)
		case <-safety:
			t.Fatal("the pending transcription was never stored")
		case <-time.After(time.Millisecond):
		}
	}
	d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Text: "late"})
	select {
	case got := <-done:
		if got.Text != "late" || got.Pending {
			t.Fatalf("late update: %+v", got)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-safety:
		t.Fatal("the request did not see the update")
	}
}
