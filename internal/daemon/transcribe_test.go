package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type transcriptionFake struct {
	*fakeTelegram
	answer rpc.Transcription
	err    error
	calls  int
	onCall func()
}

func (f *transcriptionFake) SetTranscriptionSink(func(rpc.Transcription)) {}
func (f *transcriptionFake) Transcribe(context.Context, chatref.Ref, int) (rpc.Transcription, error) {
	f.calls++
	if f.onCall != nil {
		f.onCall()
	}
	return f.answer, f.err
}

func TestTranscribeCachesAndWaits(t *testing.T) {
	d, base, db, _ := accessTestDaemon(t)
	base.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "voice"}, {ChatID: 77, ID: 2, MediaType: "photo"}}
	f := &transcriptionFake{fakeTelegram: base, answer: rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Pending: true}}
	d.opts.Telegram = f
	p := rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1, WaitMS: 1000}
	f.onCall = func() {
		// The update arrives from another goroutine, before or while the request
		// waits. Either order must give the text, so no sleep is needed.
		go func() {
			d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 4, Text: "hello"})
		}()
	}
	v, err := d.messageTranscribe(context.Background(), params(t, p))
	if err != nil || v.(rpc.Transcription).Text != "hello" {
		t.Fatalf("wait: %+v %v", v, err)
	}
	v, err = d.messageTranscribe(context.Background(), params(t, p))
	if err != nil || !v.(rpc.Transcription).Cached || f.calls != 1 {
		t.Fatalf("cache: %+v %v calls=%d", v, err, f.calls)
	}
	p.ID = 2
	if _, err = d.messageTranscribe(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams || f.calls != 1 {
		t.Fatalf("photo: %v calls=%d", err, f.calls)
	}
	d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 99, TranscriptionID: 8, Text: "foreign"})
	if _, err := db.GetTranscription(context.Background(), 77, 99); err == nil {
		t.Fatal("unsolicited update stored")
	}
}

func TestTranscribePendingAndValidation(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	base.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "video_note"}}
	f := &transcriptionFake{fakeTelegram: base, answer: rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 9, Pending: true}}
	d.opts.Telegram = f
	p := rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1}
	v, err := d.messageTranscribe(context.Background(), params(t, p))
	if err != nil || !v.(rpc.Transcription).Pending {
		t.Fatalf("pending: %+v %v", v, err)
	}
	p.WaitMS = 90001
	if _, err := d.messageTranscribe(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("wait: %v", err)
	}
	writeAccess(t, path, `{"block":[77]}`)
	p.WaitMS = 0
	if _, err := d.messageTranscribe(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed: %v", err)
	}
}

func TestTranscribePendingCanBeRetried(t *testing.T) {
	d, base, _, _ := accessTestDaemon(t)
	base.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "voice"}}
	f := &transcriptionFake{fakeTelegram: base, answer: rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 9, Pending: true}}
	d.opts.Telegram = f
	p := rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1}
	if _, err := d.messageTranscribe(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	f.answer.Pending = false
	f.answer.Text = "completed"
	v, err := d.messageTranscribe(context.Background(), params(t, p))
	if err != nil || v.(rpc.Transcription).Text != "completed" || f.calls != 2 {
		t.Fatalf("retry: %+v %v calls=%d", v, err, f.calls)
	}
}

func TestTranscribeUpdateBeforeResponse(t *testing.T) {
	d, base, _, _ := accessTestDaemon(t)
	base.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "voice"}}
	f := &transcriptionFake{fakeTelegram: base, answer: rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 9, Pending: true}}
	f.onCall = func() { d.handleTranscription(rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 9, Text: "early"}) }
	d.opts.Telegram = f
	v, err := d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Telegram: true, Chat: "77", ID: 1}))
	if err != nil || v.(rpc.Transcription).Text != "early" || v.(rpc.Transcription).Pending {
		t.Fatalf("early update: %+v %v", v, err)
	}
}
