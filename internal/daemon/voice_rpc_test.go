package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// transcribeLocal calls the handler the way the server would: with the default
// local queue.
func (f *voiceFixture) transcribeLocal(chat string, id int, waitMS int64) (rpc.Transcription, error) {
	f.t.Helper()
	v, err := f.d.messageTranscribe(context.Background(), params(f.t, rpc.TranscribeParams{Chat: chat, ID: id, WaitMS: waitMS}))
	if err != nil {
		return rpc.Transcription{}, err
	}
	return v.(rpc.Transcription), nil
}

func errorReason(t *testing.T, err error) string {
	t.Helper()
	var e *rpc.Error
	if !errors.As(err, &e) {
		t.Fatalf("not an RPC error: %v", err)
	}
	data, _ := e.Data.(map[string]any)
	reason, _ := data["reason"].(string)
	return reason
}

// TestVoiceWorkerTranscribeThroughTheQueue: by default the command queues a
// manual job, waits for the local text and never asks Telegram to transcribe.
func TestVoiceWorkerTranscribeThroughTheQueue(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(77, 1, store.VoiceKindVoice, 2048, 12)
	got, err := f.transcribeLocal("77", 1, 60000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "привет как дела" || got.Source != "local" || got.Engine != "gigaam-v3" || got.Pending || got.Cached || !got.Queued || got.ChatID != 77 || got.ID != 1 {
		t.Fatalf("result = %+v", got)
	}
	j := f.job(77, 1)
	if j.Source != store.VoiceSourceManual || j.Priority != store.VoicePriorityManual || j.State != store.VoiceStateDone {
		t.Fatalf("job = %+v", j)
	}
	// The stored text answers the next call without work.
	again, err := f.transcribeLocal("77", 1, 60000)
	if err != nil || !again.Cached || again.Queued || again.Text != got.Text || again.Source != "local" || f.tg.downloadCount() != 1 {
		t.Fatalf("cached = %+v %v downloads=%d", again, err, f.tg.downloadCount())
	}
	if f.tg.transcribed() != 0 {
		t.Fatalf("messages.transcribeAudio was called %d times", f.tg.transcribed())
	}
	f.noFiles()
}

// TestVoiceWorkerTranscribeAnswersPendingWhenTheWaitEnds: a job that does not
// finish within the wait gives pending, and goes on.
func TestVoiceWorkerTranscribeAnswersPendingWhenTheWaitEnds(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	release := make(chan struct{})
	started := make(chan struct{})
	f.eng.onTranscribe = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.seed(77, 1, store.VoiceKindVoice, 100, 3)
	got, err := f.transcribeLocal("77", 1, 0)
	if err != nil || !got.Pending || !got.Queued || got.Source != "local" || got.Engine != "gigaam-v3" || got.Text != "" {
		t.Fatalf("pending = %+v %v", got, err)
	}
	<-started
	current := f.status().Current
	close(release)
	if current == nil || current.Stage != "transcribing" {
		t.Fatalf("voice job stage during recognition: %+v", current)
	}
	f.awaitState(77, 1, store.VoiceStateDone)
	done, err := f.transcribeLocal("77", 1, 0)
	if err != nil || done.Pending || !done.Cached || done.Text != "привет как дела" {
		t.Fatalf("after the job = %+v %v", done, err)
	}
}

// TestVoiceWorkerTranscribeWaitsForTheJobAlreadyRunning: a second command for the
// same message does not start a second job; it waits for the first.
func TestVoiceWorkerTranscribeWaitsForTheJob(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	started := make(chan struct{})
	release := make(chan struct{})
	f.eng.onTranscribe = func(context.Context) error {
		close(started)
		<-release
		return nil
	}
	f.seed(77, 1, store.VoiceKindVoice, 100, 3)
	first := make(chan rpc.Transcription, 1)
	go func() {
		got, _ := f.transcribeLocal("77", 1, 60000)
		first <- got
	}()
	<-started
	second := make(chan rpc.Transcription, 1)
	go func() {
		got, _ := f.transcribeLocal("77", 1, 60000)
		second <- got
	}()
	// The second call is a queue call too; it cannot return until the job ends.
	select {
	case got := <-second:
		t.Fatalf("the second call returned before the job ended: %+v", got)
	default:
	}
	close(release)
	a, b := <-first, <-second
	if a.Text != "привет как дела" || b.Text != a.Text || f.tg.downloadCount() != 1 {
		t.Fatalf("results %+v %+v downloads=%d", a, b, f.tg.downloadCount())
	}
}

func TestVoiceWorkerTranscribeNeedsTheEngine(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.eng.setMissing("ffmpeg", "model")
	f.seed(77, 1, store.VoiceKindVoice, 100, 3)
	_, err := f.transcribeLocal("77", 1, 1000)
	if writeCode(t, err) != rpc.CodeInvalidParams || errorReason(t, err) != "engine_missing" ||
		!strings.Contains(err.Error(), "tg voice install") || !strings.Contains(err.Error(), "ffmpeg, model") {
		t.Fatalf("error = %v", err)
	}
	if _, err := f.db.GetVoiceJob(context.Background(), 77, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a job was queued without an engine: %v", err)
	}
	f.w.mu.Lock()
	started := f.w.started
	f.w.mu.Unlock()
	if started || f.tg.downloadCount() != 0 {
		t.Fatalf("the worker started or downloaded without an engine")
	}
	// A stored text needs no engine.
	if err := f.db.PutTranscription(context.Background(), store.Transcription{ChatID: 77, MessageID: 1, Text: "old", Source: store.TranscriptionSourceTelegram}); err != nil {
		t.Fatal(err)
	}
	got, err := f.transcribeLocal("77", 1, 1000)
	if err != nil || !got.Cached || got.Text != "old" || got.Source != "telegram" {
		t.Fatalf("cached = %+v %v", got, err)
	}
}

func TestVoiceWorkerTranscribeReadsAMessageTheDatabaseLacks(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.tg.messages = []rpc.Message{{ChatID: 77, ID: 9, Date: "2026-11-20T09:00:00Z", MediaType: "video_note"}, {ChatID: 77, ID: 10, Date: "2026-11-20T09:00:00Z", MediaType: "photo"}}
	got, err := f.transcribeLocal("77", 9, 60000)
	if err != nil || got.Text == "" || got.Source != "local" {
		t.Fatalf("result = %+v %v", got, err)
	}
	if j := f.job(77, 9); j.Kind != store.VoiceKindVideoNote || j.Source != store.VoiceSourceManual {
		t.Fatalf("job = %+v", j)
	}
	if _, err := f.db.GetMessage(context.Background(), 77, 9); err != nil {
		t.Fatalf("the message was not saved: %v", err)
	}
	// A photo is refused, and nothing is queued for it.
	if _, err := f.transcribeLocal("77", 10, 1000); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("photo: %v", err)
	}
	if _, err := f.transcribeLocal("77", 11, 1000); err == nil {
		t.Fatal("a message Telegram does not have must fail")
	}
	for _, id := range []int{10, 11} {
		if _, err := f.db.GetVoiceJob(context.Background(), 77, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a job was queued for message %d: %v", id, err)
		}
	}
}

func TestVoiceWorkerTranscribeAcceptsAVideo(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(77, 3, store.VoiceKindVideo, 10<<20, 60)
	got, err := f.transcribeLocal("77", 3, 60000)
	if err != nil || got.Text == "" {
		t.Fatalf("video = %+v %v", got, err)
	}
	if j := f.job(77, 3); j.Kind != store.VoiceKindVideo || j.Source != store.VoiceSourceManual {
		t.Fatalf("job = %+v", j)
	}
}

func TestVoiceWorkerTranscribeKeepsAClosedChatClosed(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(77, 1, store.VoiceKindVoice, 100, 3)
	writeAccess(t, f.accessPath, `{"block":[77]}`)
	_, err := f.transcribeLocal("77", 1, 1000)
	if writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("error = %v", err)
	}
	if _, err := f.db.GetVoiceJob(context.Background(), 77, 1); !errors.Is(err, store.ErrNotFound) || f.tg.downloadCount() != 0 {
		t.Fatalf("a closed chat was queued or downloaded: %v", err)
	}
	// Closed after the job was queued: the answer is the same and nothing is stored.
	writeAccess(t, f.accessPath, `{}`)
	started := make(chan struct{})
	release := make(chan struct{})
	f.eng.onTranscribe = func(context.Context) error {
		close(started)
		<-release
		return nil
	}
	errs := make(chan error, 1)
	go func() {
		_, err := f.transcribeLocal("77", 1, 60000)
		errs <- err
	}()
	<-started
	writeAccess(t, f.accessPath, `{"block":[77]}`)
	close(release)
	if err := <-errs; writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("error after closing = %v", err)
	}
	if _, err := f.db.GetTranscription(context.Background(), 77, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a closed chat got a text: %v", err)
	}
}

// TestVoiceWorkerTranscribeNamesWhyAJobFailed: a job that ends without a text is
// an error that carries the machine reason.
func TestVoiceWorkerTranscribeNamesWhyAJobFailed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(f *voiceFixture)
		code    int
		reason  string
	}{
		{"download refused", func(f *voiceFixture) { f.tg.downloadErrs = []error{errors.New("refused")} }, rpc.CodeInternal, "download"},
		{"engine failed", func(f *voiceFixture) { f.eng.transcribeErr = errors.New("sherpa") }, rpc.CodeInternal, "engine"},
		{"too large", func(f *voiceFixture) { f.tg.downloadErrs = []error{ErrVoiceTooLarge} }, rpc.CodeInvalidParams, "too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVoiceFixture(t, voiceTestConfig())
			tc.arrange(f)
			f.seed(77, 1, store.VoiceKindVoice, 0, 0)
			_, err := f.transcribeLocal("77", 1, 60000)
			if writeCode(t, err) != tc.code || errorReason(t, err) != tc.reason {
				t.Fatalf("error = %v", err)
			}
			f.noFiles()
			// The command is a retry: the second call queues the job again and works.
			f.eng.transcribeErr = nil
			got, err := f.transcribeLocal("77", 1, 60000)
			if err != nil || got.Text != "привет как дела" {
				t.Fatalf("second call = %+v %v", got, err)
			}
		})
	}
}

// TestVoiceWorkerTelegramFlagIsTheOnlyPathToTelegram: --telegram asks Telegram,
// replaces a local text and says whose text it is.
func TestVoiceWorkerTelegramFlagIsTheOnlyPathToTelegram(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.tg.messages = []rpc.Message{{ChatID: 77, ID: 1, MediaType: "voice"}}
	f.tg.transcribeAnswer = &rpc.Transcription{ChatID: 77, ID: 1, TranscriptionID: 5, Text: "от телеграма"}
	f.seed(77, 1, store.VoiceKindVoice, 100, 3)
	local, err := f.transcribeLocal("77", 1, 60000)
	if err != nil || local.Source != "local" || f.tg.transcribed() != 0 {
		t.Fatalf("local = %+v %v calls=%d", local, err, f.tg.transcribed())
	}
	v, err := f.d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Chat: "77", ID: 1, WaitMS: 1000, Telegram: true}))
	if err != nil {
		t.Fatal(err)
	}
	got := v.(rpc.Transcription)
	if got.Text != "от телеграма" || got.Source != "telegram" || got.Cached || f.tg.transcribed() != 1 {
		t.Fatalf("telegram = %+v calls=%d", got, f.tg.transcribed())
	}
	stored, err := f.db.GetTranscription(context.Background(), 77, 1)
	if err != nil || stored.Source != store.TranscriptionSourceTelegram || stored.Text != "от телеграма" {
		t.Fatalf("stored = %+v %v", stored, err)
	}
	// A repeat is the cached Telegram text, with no new request.
	v, err = f.d.messageTranscribe(context.Background(), params(t, rpc.TranscribeParams{Chat: "77", ID: 1, WaitMS: 1000, Telegram: true}))
	if err != nil || !v.(rpc.Transcription).Cached || v.(rpc.Transcription).Source != "telegram" || f.tg.transcribed() != 1 {
		t.Fatalf("repeat = %+v %v calls=%d", v, err, f.tg.transcribed())
	}
}

func TestVoiceWorkerTranscribeValidatesTheWait(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(77, 1, store.VoiceKindVoice, 100, 3)
	for name, p := range map[string]rpc.TranscribeParams{
		"negative wait":       {Chat: "77", ID: 1, WaitMS: -1},
		"over 600 seconds":    {Chat: "77", ID: 1, WaitMS: 600001},
		"no id":               {Chat: "77", ID: 0},
		"telegram over 90 s":  {Chat: "77", ID: 1, WaitMS: 90001, Telegram: true},
		"telegram negative":   {Chat: "77", ID: 1, WaitMS: -1, Telegram: true},
		"telegram without id": {Chat: "77", ID: 0, Telegram: true},
	} {
		if _, err := f.d.messageTranscribe(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("%s: %v", name, err)
		}
	}
	// 600 seconds are allowed for the queue.
	f.w.cfg.Enabled = true
	if _, err := f.transcribeLocal("77", 1, 600000); err != nil {
		t.Fatalf("600 seconds: %v", err)
	}
}

// TestVoiceWorkerRetryQueuesAgain: retry queues the job of a saved message again
// whatever it ended with, and the new text replaces the old one.
func TestVoiceWorkerRetryQueuesAgain(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.holdWorker()
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	if f.job(1001, 7).State != store.VoiceStateDone {
		t.Fatal("setup: the job should be done")
	}
	// A queued job is raised to the front, and a second retry changes nothing.
	f.queue(1001, 8, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.seed(1001, 8, store.VoiceKindVoice, 100, 3)
	v, err := f.d.voiceRetry(context.Background(), params(t, rpc.VoiceRetryParams{Chat: "1001", ID: 8}))
	if res := v.(rpc.VoiceRetryResult); err != nil || !res.Queued || res.State != store.VoiceStateQueued || res.Kind != "voice" {
		t.Fatalf("retry of a queued job = %+v %v", v, err)
	}
	if got := f.job(1001, 8); got.Priority != store.VoicePriorityManual || got.Source != store.VoiceSourceManual {
		t.Fatalf("retry must raise a queued job to a manual one: %+v", got)
	}
	v, err = f.d.voiceRetry(context.Background(), params(t, rpc.VoiceRetryParams{Chat: "1001", ID: 8}))
	if res := v.(rpc.VoiceRetryResult); err != nil || res.Queued || res.State != store.VoiceStateQueued {
		t.Fatalf("second retry = %+v %v", v, err)
	}
	// The finished job is queued again, runs, and replaces the text.
	f.eng.segments = []VoiceSegment{{Text: "новый текст"}}
	v, err = f.d.voiceRetry(context.Background(), params(t, rpc.VoiceRetryParams{Chat: "1001", ID: 7}))
	if res := v.(rpc.VoiceRetryResult); err != nil || !res.Queued || res.State != store.VoiceStateQueued || res.ChatID != 1001 || res.ID != 7 {
		t.Fatalf("retry = %+v %v", v, err)
	}
	f.w.cfg.Enabled = true
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateDone || j.Source != store.VoiceSourceManual {
		t.Fatalf("job = %+v", j)
	}
	if got, _ := f.db.GetTranscription(context.Background(), 1001, 7); got.Text != "новый текст" {
		t.Fatalf("text = %q, want it replaced", got.Text)
	}
	if f.tg.transcribed() != 0 {
		t.Fatal("messages.transcribeAudio was called")
	}
}

func TestVoiceWorkerRetryChecksItsInput(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	photo := "photo"
	if err := f.db.UpsertMessages(context.Background(), []store.Message{{ChatID: 1001, ID: 1, Date: voiceT0.Unix(), MediaType: &photo, Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	if err := f.db.UpsertChats(context.Background(), []store.Chat{{ID: 1001, Type: "user", Title: "Ann", Username: strPtr("annabel")}}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		p    rpc.VoiceRetryParams
		code int
	}{
		"no chat":          {rpc.VoiceRetryParams{ID: 2}, rpc.CodeInvalidParams},
		"no id":            {rpc.VoiceRetryParams{Chat: "1001"}, rpc.CodeInvalidParams},
		"a photo":          {rpc.VoiceRetryParams{Chat: "1001", ID: 1}, rpc.CodeInvalidParams},
		"not saved":        {rpc.VoiceRetryParams{Chat: "1001", ID: 99}, rpc.CodeNotFound},
		"unknown username": {rpc.VoiceRetryParams{Chat: "@nobody", ID: 2}, rpc.CodeNotFound},
		"phone number":     {rpc.VoiceRetryParams{Chat: "+15551234567", ID: 2}, rpc.CodeInvalidParams},
		"an invite link":   {rpc.VoiceRetryParams{Chat: "https://t.me/+AbCdEfGh1234", ID: 2}, rpc.CodeInvalidParams},
	} {
		if _, err := f.d.voiceRetry(context.Background(), params(t, tc.p)); writeCode(t, err) != tc.code {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A known username resolves without Telegram.
	if v, err := f.d.voiceRetry(context.Background(), params(t, rpc.VoiceRetryParams{Chat: "@annabel", ID: 2})); err != nil || v.(rpc.VoiceRetryResult).ChatID != 1001 {
		t.Fatalf("username = %+v %v", v, err)
	}
	if f.tg.resolveCalls != 0 {
		t.Fatalf("retry called Telegram to resolve the chat")
	}
	f.eng.setMissing("runtime")
	if _, err := f.d.voiceRetry(context.Background(), params(t, rpc.VoiceRetryParams{Chat: "1001", ID: 2})); writeCode(t, err) != rpc.CodeInvalidParams || errorReason(t, err) != "engine_missing" {
		t.Fatalf("missing engine: %v", err)
	}
	f.eng.setMissing()
	writeAccess(t, f.accessPath, `{"block":[1001]}`)
	if _, err := f.d.voiceRetry(context.Background(), params(t, rpc.VoiceRetryParams{Chat: "1001", ID: 2})); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed chat: %v", err)
	}
}

func strPtr(s string) *string { return &s }

// backfillFixture seeds the chats and messages of the backfill tests.
func backfillFixture(t *testing.T, cfg VoiceTranscriptionConfig) *voiceFixture {
	t.Helper()
	f := newVoiceFixture(t, cfg)
	f.holdWorker()
	chats := []store.Chat{
		{ID: 1001, Type: "user", Title: "Ann"},
		{ID: 1002, Type: "supergroup", Title: "Team"},
		{ID: 1003, Type: "user", Title: "Closed"},
		{ID: 1004, Type: "bot", Title: "Bot"},
	}
	if err := f.db.UpsertChats(context.Background(), chats); err != nil {
		t.Fatal(err)
	}
	// Chat 1001: ids 1, 2 and 6 qualify.
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	own := true
	voice := store.VoiceKindVoice
	if err := f.db.UpsertMessages(context.Background(), []store.Message{{ChatID: 1001, ID: 3, Date: voiceT0.Unix(), MediaType: &voice, Out: &own, Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	f.seed(1001, 4, store.VoiceKindVoice, 100, 3)
	if _, err := f.db.MarkMessagesDeleted(context.Background(), 1001, []int{4}); err != nil {
		t.Fatal(err)
	}
	f.seed(1001, 5, store.VoiceKindVideo, 100, 3)
	f.seed(1001, 6, store.VoiceKindVideoNote, 100, 3)
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	if err := f.db.PutTranscription(context.Background(), store.Transcription{ChatID: 1001, MessageID: 7, Text: "already", Source: store.TranscriptionSourceTelegram}); err != nil {
		t.Fatal(err)
	}
	f.seed(1001, 8, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 8, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.seed(1002, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1003, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1004, 1, store.VoiceKindVoice, 100, 3)
	writeAccess(t, f.accessPath, `{"block":[1003]}`)
	return f
}

func TestVoiceWorkerBackfillCountsAndQueues(t *testing.T) {
	f := backfillFixture(t, voiceTestConfig())
	v, err := f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{DryRun: true}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res != (rpc.VoiceBackfillResult{Enabled: true, DryRun: true, Chats: 1, Candidates: 3}) {
		t.Fatalf("dry run = %+v %v", v, err)
	}
	for _, id := range []int{1, 2, 6} {
		if _, err := f.db.GetVoiceJob(context.Background(), 1001, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a dry run queued message %d: %v", id, err)
		}
	}
	v, err = f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res != (rpc.VoiceBackfillResult{Enabled: true, Chats: 1, Candidates: 3, Queued: 3}) {
		t.Fatalf("backfill = %+v %v", v, err)
	}
	for id, kind := range map[int]string{1: "voice", 2: "voice", 6: "video_note"} {
		j := f.job(1001, id)
		if j.Kind != kind || j.Source != store.VoiceSourceBackfill || j.Priority != store.VoicePriorityBackfill || j.State != store.VoiceStateQueued {
			t.Fatalf("job %d = %+v", id, j)
		}
	}
	// Everything left out stays out: own, deleted, video, a text, a job, other chat kinds, a closed chat.
	for _, key := range [][2]int64{{1001, 3}, {1001, 4}, {1001, 5}, {1001, 7}, {1002, 1}, {1003, 1}, {1004, 1}} {
		if _, err := f.db.GetVoiceJob(context.Background(), key[0], int(key[1])); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("message %v was queued: %v", key, err)
		}
	}
	if j := f.job(1001, 8); j.Priority != store.VoicePriorityIncoming {
		t.Fatalf("an earlier job was changed: %+v", j)
	}
	// A second run finds nothing new.
	v, err = f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res.Candidates != 0 || res.Queued != 0 {
		t.Fatalf("second backfill = %+v %v", v, err)
	}
	if f.tg.downloadCount() != 0 || f.tg.resolveCalls != 0 || f.tg.transcribed() != 0 {
		t.Fatal("backfill touched Telegram")
	}
}

func TestVoiceWorkerBackfillNeedsTheSettingAndTheEngine(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.Enabled = false
	f := backfillFixture(t, cfg)
	if _, err := f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{})); writeCode(t, err) != rpc.CodeInvalidParams || !strings.Contains(err.Error(), "tg settings set voice_transcription.enabled true and restart the Daemon") {
		t.Fatalf("off: %v", err)
	}
	if _, err := f.db.GetVoiceJob(context.Background(), 1001, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a job was queued while off: %v", err)
	}
	// The dry run counts anyway and says that the setting is off.
	v, err := f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{DryRun: true}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res.Enabled || res.Candidates != 3 || res.Queued != 0 {
		t.Fatalf("dry run while off = %+v %v", v, err)
	}
	f.w.cfg.Enabled = true
	f.eng.setMissing("model")
	if _, err := f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{})); writeCode(t, err) != rpc.CodeInvalidParams || errorReason(t, err) != "engine_missing" {
		t.Fatalf("missing engine: %v", err)
	}
	// A closed daemon policy shows nothing at all.
	f.eng.setMissing()
	writeAccess(t, f.accessPath, `{"allow":[1002]}`)
	v, err = f.d.voiceBackfill(context.Background(), params(t, rpc.VoiceBackfillParams{DryRun: true}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res.Chats != 0 || res.Candidates != 0 {
		t.Fatalf("with every personal chat closed = %+v %v", v, err)
	}
}

func TestVoiceWorkerStatusRPC(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	v, err := f.d.voiceStatus(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := v.(rpc.VoiceStatus)
	if !st.Enabled || !st.Installed || st.State != rpc.VoiceStateDisabled || st.Queue != (rpc.VoiceQueueCounts{}) {
		t.Fatalf("status = %+v", st)
	}
	if st.Missing != nil && len(st.Missing) != 0 {
		t.Fatalf("missing = %v", st.Missing)
	}
}
