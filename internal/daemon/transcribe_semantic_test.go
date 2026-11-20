package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// semanticWakeups is the number of pending run requests of the indexer; the
// indexer here has no loop, so the buffered wake channel records the signals.
func semanticWakeups(d *daemon) int { return len(d.semanticIndex.wake) }

func transcriptIndexDaemon(t *testing.T) (*daemon, *transcriptionFake) {
	t.Helper()
	d, base, _, _ := accessTestDaemon(t)
	d.semanticIndex = &semanticIndexer{wake: make(chan struct{}, 1)}
	base.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "voice"}}
	f := &transcriptionFake{fakeTelegram: base, answer: rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Pending: true}}
	d.opts.Telegram = f
	return d, f
}

// A pending result changes no text, so it does not wake the indexer; the update
// that finishes it does.
func TestTranscriptUpdateWakesTheSemanticIndexer(t *testing.T) {
	d, _ := transcriptIndexDaemon(t)
	ctx := context.Background()
	if _, err := d.messageTranscribe(ctx, params(t, rpc.TranscribeParams{Chat: "77", ID: 1})); err != nil {
		t.Fatal(err)
	}
	if n := semanticWakeups(d); n != 0 {
		t.Fatalf("a pending result woke the indexer %d times", n)
	}
	d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Text: "доставка завтра"})
	if n := semanticWakeups(d); n != 1 {
		t.Fatalf("a finished update woke the indexer %d times, want 1", n)
	}
	got, err := d.store.GetTranscription(ctx, 77, 1)
	if err != nil || got.Text != "доставка завтра" || got.Pending {
		t.Fatalf("stored transcription: %+v, %v", got, err)
	}
}

// An update nobody asked for is not stored, so it is no reason to run.
func TestTranscriptForeignUpdateDoesNotWakeTheSemanticIndexer(t *testing.T) {
	d, _ := transcriptIndexDaemon(t)
	d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 99, TranscriptionID: 8, Text: "foreign"})
	if n := semanticWakeups(d); n != 0 {
		t.Fatalf("an unsolicited update woke the indexer %d times", n)
	}
}

// A result that is ready in the first answer is saved by the command itself.
func TestTranscriptFinishedAnswerWakesTheSemanticIndexer(t *testing.T) {
	d, f := transcriptIndexDaemon(t)
	f.answer = rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Text: "оплата прошла"}
	if _, err := d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Chat: "77", ID: 1})); err != nil {
		t.Fatal(err)
	}
	if n := semanticWakeups(d); n != 1 {
		t.Fatalf("a finished answer woke the indexer %d times, want 1", n)
	}
}

// A result that finishes while the command waits is saved by the command and
// wakes the indexer too.
func TestTranscriptUpdateWhileWaitingWakesTheSemanticIndexer(t *testing.T) {
	d, f := transcriptIndexDaemon(t)
	f.onCall = func() {
		go d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Text: "привет"})
	}
	v, err := d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Chat: "77", ID: 1, WaitMS: 1000}))
	if err != nil || v.(rpc.Transcription).Text != "привет" {
		t.Fatalf("wait: %+v, %v", v, err)
	}
	if n := semanticWakeups(d); n != 1 {
		t.Fatalf("an update while waiting woke the indexer %d times, want 1", n)
	}
}

// Without an indexer (the feature is off) saving a transcription is a no-op
// for it and does not fail.
func TestTranscriptWithoutIndexerSavesQuietly(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	ctx := context.Background()
	if err := db.PutTranscription(ctx, store.Transcription{ChatID: 77, MessageID: 1, TranscriptionID: 4, Pending: true}); err != nil {
		t.Fatal(err)
	}
	d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Text: "ok"})
	got, err := db.GetTranscription(ctx, 77, 1)
	if err != nil || got.Text != "ok" {
		t.Fatalf("stored transcription: %+v, %v", got, err)
	}
}
