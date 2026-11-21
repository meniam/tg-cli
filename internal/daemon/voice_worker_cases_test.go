package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/voice/exec"
)

// TestVoiceWorkerCleansUpOnEveryOutcome: whatever ends a job, the working
// directory is empty afterwards and the queue row says how it ended.
func TestVoiceWorkerCleansUpOnEveryOutcome(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arrange   func(f *voiceFixture)
		state     string
		reason    string
		downloads int
		programs  string
	}{
		{"success", func(*voiceFixture) {}, store.VoiceStateDone, "", 1, "convert,transcribe"},
		{"convert fails", func(f *voiceFixture) { f.eng.convertErr = errors.New("ffmpeg failed") }, store.VoiceStateFailed, "convert", 1, "convert"},
		{"engine fails", func(f *voiceFixture) { f.eng.transcribeErr = errors.New("sherpa failed") }, store.VoiceStateFailed, "engine", 1, "convert,transcribe"},
		{"engine panics", func(f *voiceFixture) {
			f.eng.onTranscribe = func(context.Context) error { panic("engine bug") }
		}, store.VoiceStateFailed, "internal", 1, "convert,transcribe"},
		{"disk is full", func(f *voiceFixture) { f.eng.diskErr = errors.New("no space") }, store.VoiceStateFailed, "disk", 0, ""},
		{"download refused", func(f *voiceFixture) { f.tg.downloadErrs = []error{errors.New("refused")} }, store.VoiceStateFailed, "download", 1, ""},
		{"downloaded size differs", func(f *voiceFixture) { f.tg.downloadErrs = []error{ErrVoiceSizeMismatch} }, store.VoiceStateFailed, "download", 1, ""},
		{"message gone from Telegram", func(f *voiceFixture) { f.tg.downloadErrs = []error{rpc.NewError(rpc.CodeNotFound, "gone")} }, store.VoiceStateFailed, "no_media", 1, ""},
		{"Telegram refuses the file", func(f *voiceFixture) {
			f.tg.downloadErrs = []error{rpc.NewError(rpc.CodeTelegram, "PEER_ID_INVALID")}
		}, store.VoiceStateFailed, "download", 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVoiceFixture(t, voiceTestConfig())
			tc.arrange(f)
			f.seed(1001, 7, store.VoiceKindVoice, 2048, 12)
			f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, store.VoicePriorityIncoming)
			f.w.recover(f.root)
			f.drain()
			j := f.job(1001, 7)
			if j.State != tc.state || j.Reason != tc.reason || j.Attempts != 0 {
				t.Fatalf("job = %+v, want %s/%q with no retry", j, tc.state, tc.reason)
			}
			if f.tg.downloadCount() != tc.downloads || strings.Join(f.eng.callList(), ",") != tc.programs {
				t.Fatalf("downloads=%d programs=%v", f.tg.downloadCount(), f.eng.callList())
			}
			f.noFiles()
			if tc.state == store.VoiceStateFailed {
				if got := f.status().LastFailure; got != tc.reason {
					t.Fatalf("last failure = %q, want %q", got, tc.reason)
				}
				if _, err := f.db.GetTranscription(context.Background(), 1001, 7); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("a failed job stored a text: %v", err)
				}
			}
			if f.tg.transcribed() != 0 {
				t.Fatal("messages.transcribeAudio was called")
			}
		})
	}
}

// TestVoiceWorkerRetriesNetworkFailuresWithPauses: three retries after 10, 60
// and 300 seconds on the clock the worker owns, then the job fails.
func TestVoiceWorkerRetriesNetworkFailuresWithPauses(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	netErr := &rpc.Error{Code: rpc.CodeTelegram, Message: "telegram request failed: network error"}
	f.tg.downloadErrs = []error{netErr, netErr, netErr, netErr, netErr}
	f.seed(1001, 7, store.VoiceKindVoice, 2048, 12)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, store.VoicePriorityIncoming)
	f.start()
	for attempt, delay := range []time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second} {
		now := f.clock.Now()
		awaitCond(t, fmt.Sprintf("the pause of %s", delay), func() bool { return f.clock.hasTimerAt(now.Add(delay)) })
		j := f.job(1001, 7)
		if j.State != store.VoiceStateQueued || j.Attempts != attempt+1 || f.tg.downloadCount() != attempt+1 {
			t.Fatalf("during pause %d: job=%+v downloads=%d", attempt, j, f.tg.downloadCount())
		}
		// Time has not moved, so the worker must still be waiting.
		if got := f.tg.downloadCount(); got != attempt+1 {
			t.Fatalf("downloaded again before the pause ended: %d", got)
		}
		f.clock.AdvanceTo(now.Add(delay))
	}
	j := f.awaitState(1001, 7, store.VoiceStateFailed)
	if j.Reason != "download" || j.Attempts != 3 || f.tg.downloadCount() != 4 {
		t.Fatalf("after the retries: job=%+v downloads=%d", j, f.tg.downloadCount())
	}
	if f.status().LastFailure != "download" {
		t.Fatalf("status = %+v", f.status())
	}
	f.noFiles()
}

// TestVoiceWorkerRetrySucceedsAfterAPause: the second try works, and the job is
// done with the attempt counted.
func TestVoiceWorkerRetrySucceedsAfterAPause(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.tg.downloadErrs = []error{&rpc.Error{Code: rpc.CodeFloodWait, Message: "FLOOD_WAIT"}}
	f.seed(1001, 7, store.VoiceKindVoice, 2048, 12)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, store.VoicePriorityIncoming)
	f.start()
	awaitCond(t, "the first pause", func() bool { return f.clock.hasTimerAt(voiceT0.Add(10 * time.Second)) })
	f.clock.AdvanceTo(voiceT0.Add(10 * time.Second))
	j := f.awaitState(1001, 7, store.VoiceStateDone)
	if j.Attempts != 1 || f.tg.downloadCount() != 2 {
		t.Fatalf("job=%+v downloads=%d", j, f.tg.downloadCount())
	}
}

func TestVoiceNetworkError(t *testing.T) {
	timeout := &rpc.Error{Code: rpc.CodeTelegram, Message: "telegram request failed: timeout"}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"client network error": {&rpc.Error{Code: rpc.CodeTelegram, Message: "telegram request failed: network error"}, true},
		"client timeout":       {fmt.Errorf("wrapped: %w", timeout), true},
		"flood wait":           {&rpc.Error{Code: rpc.CodeFloodWait, Message: "FLOOD_WAIT"}, true},
		"a net error hidden in the cause is not unwrapped": {&rpc.Error{Code: rpc.CodeTelegram, Message: "x", Cause: timeoutNetError{}}, false},
		"an unrelated error text":                          {fmt.Errorf("read: %w", errors.New("unexpected EOF")), false},
		"Telegram error":                                   {rpc.NewError(rpc.CodeTelegram, "PEER_ID_INVALID"), false},
		"not found":                                        {rpc.NewError(rpc.CodeNotFound, "gone"), false},
		"invalid params":                                   {rpc.NewError(rpc.CodeInvalidParams, "bad"), false},
		"plain error":                                      {errors.New("boom"), false},
		"size mismatch":                                    {ErrVoiceSizeMismatch, false},
		"too large":                                        {ErrVoiceTooLarge, false},
		"plain net error":                                  {timeoutNetError{}, true},
		"wrapped plain net error":                          {fmt.Errorf("download: %w", timeoutNetError{}), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := voiceNetworkError(tc.err); got != tc.want {
				t.Fatalf("voiceNetworkError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

// TestVoiceWorkerLimits: the limits of the kinds act on the size and the
// duration the message carries, before anything is downloaded, and again on
// what the download and the conversion show.
func TestVoiceWorkerLimits(t *testing.T) {
	const mb = 1 << 20
	for _, tc := range []struct {
		name      string
		kind      string
		size      int64
		seconds   float64
		reason    string // empty: the job runs
		downloads int
		arrange   func(f *voiceFixture)
	}{
		{"voice over 30 MB", store.VoiceKindVoice, 30*mb + 1, 10, "too_large", 0, nil},
		{"voice exactly 30 MB", store.VoiceKindVoice, 30 * mb, 10, "", 1, nil},
		{"video note over 50 MB", store.VoiceKindVideoNote, 50*mb + 1, 10, "too_large", 0, nil},
		{"video note 50 MB", store.VoiceKindVideoNote, 50 * mb, 10, "", 1, nil},
		{"video over 500 MB", store.VoiceKindVideo, 500*mb + 1, 60, "too_large", 0, nil},
		{"video exactly 500 MB", store.VoiceKindVideo, 500 * mb, 60, "", 1, nil},
		{"video over 120 minutes", store.VoiceKindVideo, 10 * mb, 7201, "too_long", 0, nil},
		{"video exactly 120 minutes", store.VoiceKindVideo, 10 * mb, 7200, "", 1, nil},
		{"voice has no size and no duration", store.VoiceKindVoice, 0, 0, "", 1, nil},
		{"the downloader sees a larger file", store.VoiceKindVoice, 0, 0, "too_large", 1, func(f *voiceFixture) {
			f.tg.downloadErrs = []error{fmt.Errorf("telegram says 40 MB: %w", ErrVoiceTooLarge)}
		}},
		{"video of unknown length is long once converted", store.VoiceKindVideo, 10 * mb, 0, "too_long", 1, func(f *voiceFixture) { f.eng.seconds = 7300 }},
		{"video of unknown length within the limit", store.VoiceKindVideo, 10 * mb, 0, "", 1, func(f *voiceFixture) { f.eng.seconds = 600 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVoiceFixture(t, voiceTestConfig())
			if tc.arrange != nil {
				tc.arrange(f)
			}
			f.seed(1001, 7, tc.kind, tc.size, tc.seconds)
			f.queue(1001, 7, tc.kind, store.VoiceSourceBackfill, store.VoicePriorityBackfill)
			f.w.recover(f.root)
			f.drain()
			j := f.job(1001, 7)
			if tc.reason == "" {
				if j.State != store.VoiceStateDone {
					t.Fatalf("job = %+v, want done", j)
				}
			} else if j.State != store.VoiceStateSkipped || j.Reason != tc.reason {
				t.Fatalf("job = %+v, want skipped/%s", j, tc.reason)
			}
			if f.tg.downloadCount() != tc.downloads {
				t.Fatalf("downloads = %d, want %d", f.tg.downloadCount(), tc.downloads)
			}
			if tc.downloads == 1 && tc.size == 0 {
				if req := f.tg.downloads[0]; req.ExpectedSize != 0 || req.MaxBytes != f.w.limitBytes(tc.kind) {
					t.Fatalf("an unknown size must not be asked for: %+v", req)
				}
			}
			f.noFiles()
		})
	}
}

func TestVoiceWorkerLimitsFollowTheConfiguration(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.MaxVoiceMB, cfg.MaxVideoMinutes = 1, 1
	f := newVoiceFixture(t, cfg)
	f.seed(1001, 1, store.VoiceKindVoice, 2<<20, 5)
	f.seed(1001, 2, store.VoiceKindVideo, 1<<20, 61)
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 2, store.VoiceKindVideo, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	if a, b := f.job(1001, 1), f.job(1001, 2); a.Reason != "too_large" || b.Reason != "too_long" || f.tg.downloadCount() != 0 {
		t.Fatalf("jobs = %+v %+v downloads=%d", a, b, f.tg.downloadCount())
	}
}

// TestVoiceWorkerSkipsWhatIsNotAMedia covers the checks that read the message.
func TestVoiceWorkerSkipsWhatIsNotAMedia(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	photo := "photo"
	own := false
	if err := f.db.UpsertMessages(context.Background(), []store.Message{{ChatID: 1001, ID: 1, Date: voiceT0.Unix(), MediaType: &photo, Out: &own, Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	f.seed(1001, 2, store.VoiceKindVideoNote, 100, 3)
	f.seed(1001, 3, store.VoiceKindVoice, 100, 3)
	if _, err := f.db.MarkMessagesDeleted(context.Background(), 1001, []int{3}); err != nil {
		t.Fatal(err)
	}
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceSync, 20) // a photo
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceSync, 20) // a video note queued as a voice message
	f.queue(1001, 3, store.VoiceKindVoice, store.VoiceSourceSync, 20) // deleted
	f.queue(1001, 4, store.VoiceKindVoice, store.VoiceSourceSync, 20) // not saved
	f.w.recover(f.root)
	f.drain()
	for id, want := range map[int][2]string{1: {store.VoiceStateSkipped, "no_media"}, 2: {store.VoiceStateSkipped, "no_media"}, 3: {store.VoiceStateSkipped, "no_media"}, 4: {store.VoiceStateFailed, "no_message"}} {
		if j := f.job(1001, id); j.State != want[0] || j.Reason != want[1] {
			t.Errorf("message %d: job = %+v, want %v", id, j, want)
		}
	}
	if f.tg.downloadCount() != 0 {
		t.Fatalf("downloads = %d", f.tg.downloadCount())
	}
}

// TestVoiceWorkerNoSpeech: silence is a finished job with an empty text, and a
// text that exists stops a source from queueing the work again.
func TestVoiceWorkerNoSpeech(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.eng.segments = nil
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	j := f.job(1001, 7)
	if j.State != store.VoiceStateDone || j.Reason != "no_speech" {
		t.Fatalf("job = %+v", j)
	}
	got, err := f.db.GetTranscription(context.Background(), 1001, 7)
	if err != nil || got.Text != "" || got.Pending || got.Source != store.TranscriptionSourceLocal {
		t.Fatalf("transcription = %+v %v", got, err)
	}
	if f.status().LastFailure != "" {
		t.Fatalf("silence is not a failure: %+v", f.status())
	}
	if raw := string(f.rawTranscription(1001, 7)); !strings.Contains(raw, `"segments":[]`) {
		t.Fatalf("raw = %s", raw)
	}
}

// TestVoiceWorkerVideoWithoutAudio: a video or a round video with no sound is a
// finished job with the reason no_audio, not a failure: the engine never runs,
// an empty text without an engine is stored, nothing stays on the disk, the
// download counts in the budget, and other conversion failures stay failures.
func TestVoiceWorkerVideoWithoutAudio(t *testing.T) {
	noAudio := fmt.Errorf("ffmpeg: %w: %w: exit status 1: Output file does not contain any stream", exec.ErrExit, exec.ErrNoAudio)
	for _, kind := range []string{store.VoiceKindVideo, store.VoiceKindVideoNote} {
		t.Run(kind, func(t *testing.T) {
			f := newVoiceFixture(t, voiceTestConfig())
			f.eng.convertErr = noAudio
			f.seed(1001, 7, kind, 4096, 12)
			f.queue(1001, 7, kind, store.VoiceSourceSync, store.VoicePrioritySync)
			f.w.recover(f.root)
			f.drain()
			j := f.job(1001, 7)
			if j.State != store.VoiceStateDone || j.Reason != "no_audio" || j.Attempts != 0 || j.Bytes != 1000 {
				t.Fatalf("job = %+v, want done/no_audio with the download counted", j)
			}
			if got := strings.Join(f.eng.callList(), ","); got != "convert" {
				t.Fatalf("programs = %q: the speech engine must not run for a file without audio", got)
			}
			got, err := f.db.GetTranscription(context.Background(), 1001, 7)
			if err != nil || got.Text != "" || got.Pending || got.Source != store.TranscriptionSourceLocal || got.Engine != "" {
				t.Fatalf("transcription = %+v %v", got, err)
			}
			if raw := string(f.rawTranscription(1001, 7)); !strings.Contains(raw, `"no_audio":true`) {
				t.Fatalf("raw = %s", raw)
			}
			if failure := f.status().LastFailure; failure != "" {
				t.Fatalf("a video without sound is not a failure: %q", failure)
			}
			if usage, err := f.db.VoiceUsage(context.Background(), localDay(f.clock.Now())); err != nil || usage.Files != 1 {
				t.Fatalf("usage = %+v %v", usage, err)
			}
			f.noFiles()
			if f.tg.transcribed() != 0 {
				t.Fatal("messages.transcribeAudio was called")
			}
		})
	}
	// A conversion that fails for another reason is still a failure.
	f := newVoiceFixture(t, voiceTestConfig())
	f.eng.convertErr = fmt.Errorf("ffmpeg: %w: exit status 1: Invalid data found", exec.ErrExit)
	f.seed(1001, 8, store.VoiceKindVideo, 4096, 12)
	f.queue(1001, 8, store.VoiceKindVideo, store.VoiceSourceSync, store.VoicePrioritySync)
	f.w.recover(f.root)
	f.drain()
	if j := f.job(1001, 8); j.State != store.VoiceStateFailed || j.Reason != "convert" {
		t.Fatalf("broken file: job = %+v", j)
	}
}

// TestVoiceWorkerNoAudioIsAnAnswerForMessageTranscribe: the manual command gets
// an empty finished text for a silent video, not an error.
func TestVoiceWorkerNoAudioIsAnAnswerForMessageTranscribe(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.eng.convertErr = fmt.Errorf("ffmpeg: %w: %w: exit status 1: x", exec.ErrExit, exec.ErrNoAudio)
	f.seed(77, 1, store.VoiceKindVideo, 2048, 12)
	got, err := f.transcribeLocal("77", 1, 60000)
	if err != nil || got.Pending || got.Text != "" || got.Source != "local" || !got.Queued || got.ChatID != 77 || got.ID != 1 {
		t.Fatalf("answer = %+v %v", got, err)
	}
	if j := f.job(77, 1); j.State != store.VoiceStateDone || j.Reason != "no_audio" {
		t.Fatalf("job = %+v", j)
	}
}

// TestVoiceWorkerSkipsClosedChats: a chat that access.json closes is never
// downloaded, whether it was closed before the job started or while it ran.
func TestVoiceWorkerSkipsClosedChats(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	writeAccess(t, f.accessPath, `{"block":[1001]}`)
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateSkipped || j.Reason != "closed" || f.tg.downloadCount() != 0 {
		t.Fatalf("job = %+v downloads=%d", j, f.tg.downloadCount())
	}

	// Closed while the job runs: nothing is stored.
	writeAccess(t, f.accessPath, `{}`)
	f.seed(1002, 8, store.VoiceKindVoice, 100, 3)
	f.queue(1002, 8, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.eng.onTranscribe = func(context.Context) error {
		writeAccess(t, f.accessPath, `{"block":[1002]}`)
		return nil
	}
	f.drain()
	if j := f.job(1002, 8); j.State != store.VoiceStateSkipped || j.Reason != "closed" {
		t.Fatalf("job closed during the run = %+v", j)
	}
	if _, err := f.db.GetTranscription(context.Background(), 1002, 8); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a closed chat got a text: %v", err)
	}
	f.noFiles()
}

// TestVoiceWorkerBudgetPausesUntilTomorrow: when the files of the day are used
// the queue pauses until local midnight; a manual job still runs and counts.
func TestVoiceWorkerBudgetPausesUntilTomorrow(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.DailyFiles = 2
	f := newVoiceFixture(t, cfg)
	for id := 1; id <= 5; id++ {
		f.seed(1001, id, store.VoiceKindVoice, 100, 3)
	}
	for id := 1; id <= 3; id++ {
		f.queue(1001, id, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	}
	f.w.recover(f.root)
	f.drain()
	for id := 1; id <= 2; id++ {
		if j := f.job(1001, id); j.State != store.VoiceStateDone {
			t.Fatalf("job %d = %+v", id, j)
		}
	}
	if j := f.job(1001, 3); j.State != store.VoiceStateQueued || j.Attempts != 0 {
		t.Fatalf("the job over the budget = %+v, want queued with no attempt spent", j)
	}
	if f.w.paused != rpc.VoicePausedBudget || !f.w.pausedUntil.Equal(f.daysAfter(1)) {
		t.Fatalf("pause = %q until %s, want budget until %s", f.w.paused, f.w.pausedUntil, f.daysAfter(1))
	}
	if f.tg.downloadCount() != 2 {
		t.Fatalf("downloads = %d", f.tg.downloadCount())
	}

	// A manual job ignores the budget but counts in it.
	f.queue(1001, 4, store.VoiceKindVoice, store.VoiceSourceManual, 0)
	f.queue(1001, 5, store.VoiceKindVoice, store.VoiceSourceBackfill, 30)
	f.drain()
	if j := f.job(1001, 4); j.State != store.VoiceStateDone {
		t.Fatalf("the manual job = %+v", j)
	}
	if usage, _ := f.db.VoiceUsage(context.Background(), "2026-11-20"); usage.Files != 3 {
		t.Fatalf("usage = %+v, want the manual file counted", usage)
	}
	for _, id := range []int{3, 5} {
		if j := f.job(1001, id); j.State != store.VoiceStateQueued {
			t.Fatalf("job %d = %+v, still paused", id, j)
		}
	}

	// The next local day opens a new budget.
	f.clock.AdvanceTo(f.daysAfter(1))
	f.drain()
	for _, id := range []int{3, 5} {
		if j := f.job(1001, id); j.State != store.VoiceStateDone {
			t.Fatalf("job %d after midnight = %+v", id, j)
		}
	}
	if usage, _ := f.db.VoiceUsage(context.Background(), "2026-11-21"); usage.Files != 2 {
		t.Fatalf("usage of the next day = %+v", usage)
	}
	if f.w.paused != "" {
		t.Fatalf("pause = %q after midnight", f.w.paused)
	}
}

func TestVoiceWorkerBudgetInBytes(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.DailyDownloadMB = 1
	f := newVoiceFixture(t, cfg)
	f.tg.size = 1 << 20
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	if a, b := f.job(1001, 1), f.job(1001, 2); a.State != store.VoiceStateDone || b.State != store.VoiceStateQueued || f.w.paused != rpc.VoicePausedBudget {
		t.Fatalf("jobs = %+v %+v pause=%q", a, b, f.w.paused)
	}
}

// TestVoiceWorkerMissingEngineSpendsNoAttempts: the queue waits for the engine,
// the job stays queued with no attempt spent and runs once the engine is there.
func TestVoiceWorkerMissingEngineSpendsNoAttempts(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.eng.setMissing("model", "vad")
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateQueued || j.Attempts != 0 || f.tg.downloadCount() != 0 || f.w.paused != rpc.VoicePausedEngineMissing {
		t.Fatalf("job=%+v downloads=%d pause=%q", j, f.tg.downloadCount(), f.w.paused)
	}
	st := f.status()
	if st.Installed || strings.Join(st.Missing, ",") != "model,vad" || st.Queue.Queued != 1 {
		t.Fatalf("status = %+v", st)
	}
	// Still missing: the pre-check keeps the queue untouched, the job is not
	// even claimed.
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateQueued || j.Attempts != 0 {
		t.Fatalf("job = %+v", j)
	}
	f.eng.setMissing()
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateDone || j.Attempts != 0 {
		t.Fatalf("job after the install = %+v", j)
	}
	if f.w.paused != "" {
		t.Fatalf("pause = %q", f.w.paused)
	}
}

// TestVoiceWorkerOfflinePausesTheQueue: an absent connection gives the job back
// without an attempt, and the queue goes on when Telegram is back.
func TestVoiceWorkerOfflinePausesTheQueue(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.tg.downloadErrs = []error{rpc.NewError(rpc.CodeOffline, "telegram is offline")}
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateQueued || j.Attempts != 0 || f.w.paused != rpc.VoicePausedOffline {
		t.Fatalf("job=%+v pause=%q", j, f.w.paused)
	}
	f.noFiles()
	f.drain()
	if j := f.job(1001, 7); j.State != store.VoiceStateDone || f.w.paused != "" {
		t.Fatalf("job=%+v pause=%q", j, f.w.paused)
	}
}

// TestVoiceWorkerPausesBetweenDownloads: the second download waits for the
// pause after the first, on the clock of the worker.
func TestVoiceWorkerPausesBetweenDownloads(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.Pause = 3 * time.Second
	f := newVoiceFixture(t, cfg)
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.start()
	awaitCond(t, "the pause after the first download", func() bool { return f.clock.hasTimerAt(voiceT0.Add(3 * time.Second)) })
	if j := f.job(1001, 1); j.State != store.VoiceStateDone || f.tg.downloadCount() != 1 {
		t.Fatalf("first job = %+v downloads=%d", j, f.tg.downloadCount())
	}
	f.clock.AdvanceTo(voiceT0.Add(3 * time.Second))
	f.awaitState(1001, 2, store.VoiceStateDone)
}

// TestVoiceWorkerPriorityOrder: the manual job goes first, then incoming, sync
// and backfill, each oldest first.
func TestVoiceWorkerPriorityOrder(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	var order []int
	f.eng.onTranscribe = func(context.Context) error { return nil }
	f.tg.downloadFn = func(context.Context, VoiceDownload) error { return nil }
	for id := 1; id <= 4; id++ {
		f.seed(1001, id, store.VoiceKindVoice, 100, 3)
	}
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceBackfill, 30)
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceSync, 20)
	f.queue(1001, 3, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 4, store.VoiceKindVoice, store.VoiceSourceManual, 0)
	f.w.recover(f.root)
	for range 4 {
		before := f.tg.downloadCount()
		f.w.pass(f.root)
		if f.tg.downloadCount() != before+1 {
			t.Fatal("a pass must work exactly one job")
		}
		for id := 1; id <= 4; id++ {
			if f.job(1001, id).State == store.VoiceStateDone && !containsInt(order, id) {
				order = append(order, id)
			}
		}
	}
	if fmt.Sprint(order) != "[4 3 2 1]" {
		t.Fatalf("order = %v, want [4 3 2 1]", order)
	}
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// TestVoiceWorkerCancelStopsTheRunningJob: the cancel command stops the job in
// progress, which leaves nothing behind and ends skipped; queued jobs are
// skipped at the same time.
func TestVoiceWorkerCancelStopsTheRunningJob(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	started := make(chan struct{})
	f.eng.onTranscribe = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceSync, 20)
	f.start()
	f.w.poke()
	<-started
	st := f.status()
	if st.State != rpc.VoiceStateWorking || st.Current == nil || st.Current.ChatID != 1001 || st.Current.MessageID != 1 || st.Queue.Running != 1 {
		t.Fatalf("status while running = %+v", st)
	}
	v, err := f.d.voiceCancel(context.Background(), params(t, rpc.VoiceCancelParams{}))
	if err != nil || v.(rpc.VoiceCancelResult) != (rpc.VoiceCancelResult{Skipped: 1, Stopped: true}) {
		t.Fatalf("cancel = %+v %v", v, err)
	}
	j := f.awaitState(1001, 1, store.VoiceStateSkipped)
	if j.Reason != store.VoiceReasonCancelled {
		t.Fatalf("job = %+v", j)
	}
	if j2 := f.job(1001, 2); j2.State != store.VoiceStateSkipped || j2.Reason != store.VoiceReasonCancelled {
		t.Fatalf("the queued job = %+v", j2)
	}
	awaitCond(t, "an idle worker", func() bool { return f.status().State == rpc.VoiceStateIdle })
	f.noFiles()
	if got := f.status().LastFailure; got != "" {
		t.Fatalf("a cancel is not a failure: %q", got)
	}
	if _, err := f.db.GetTranscription(context.Background(), 1001, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a cancelled job stored a text: %v", err)
	}
}

// TestVoiceWorkerCancelOneMessage: with a chat and an id only that job is
// cancelled, the running one or a queued one.
func TestVoiceWorkerCancelOneMessage(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	v, err := f.d.voiceCancel(context.Background(), params(t, rpc.VoiceCancelParams{Chat: "1001", ID: 2}))
	if err != nil || v.(rpc.VoiceCancelResult) != (rpc.VoiceCancelResult{Skipped: 1}) {
		t.Fatalf("cancel one = %+v %v", v, err)
	}
	if f.job(1001, 1).State != store.VoiceStateQueued || f.job(1001, 2).State != store.VoiceStateSkipped {
		t.Fatalf("jobs = %+v %+v", f.job(1001, 1), f.job(1001, 2))
	}
	if _, err := f.d.voiceCancel(context.Background(), params(t, rpc.VoiceCancelParams{Chat: "1001", ID: 99})); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("a message without a job: %v", err)
	}
	if _, err := f.d.voiceCancel(context.Background(), params(t, rpc.VoiceCancelParams{Chat: "1001"})); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("a chat without an id: %v", err)
	}
	writeAccess(t, f.accessPath, `{"block":[1001]}`)
	if _, err := f.d.voiceCancel(context.Background(), params(t, rpc.VoiceCancelParams{Chat: "1001", ID: 1})); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("a closed chat: %v", err)
	}
}

// TestVoiceWorkerRecoversAfterARestart: a job a dead daemon left running goes
// back to the queue and is done, and the files that daemon left are removed.
func TestVoiceWorkerRecoversAfterARestart(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	if _, ok, err := f.db.ClaimNextVoice(context.Background(), voiceT0); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if f.job(1001, 7).State != store.VoiceStateRunning {
		t.Fatal("the job should be running")
	}
	if err := os.MkdirAll(f.w.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1001_7.media", ".1001_7.media.part", "1001_7.wav"} {
		if err := os.WriteFile(filepath.Join(f.w.dir, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.start()
	f.awaitState(1001, 7, store.VoiceStateDone)
	awaitCond(t, "an idle worker", func() bool { return f.status().State == rpc.VoiceStateIdle })
	f.noFiles()
	f.tg.mu.Lock()
	cleans := f.tg.cleans
	f.tg.mu.Unlock()
	if cleans < 1 {
		t.Fatalf("the working directory was not cleaned at start: %d", cleans)
	}
	info, err := os.Stat(f.w.dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the working directory must stay, mode 0700: %v %v", info, err)
	}
}

// TestVoiceWorkerStopGivesTheJobBack: the stop of the daemon during a job leaves
// the job queued, not skipped, and the working directory empty.
func TestVoiceWorkerStopGivesTheJobBack(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	started := make(chan struct{})
	f.eng.onTranscribe = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	f.seed(1001, 7, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.start()
	<-started
	f.cancel()
	f.w.wait()
	if j := f.job(1001, 7); j.State != store.VoiceStateQueued || j.Attempts != 0 {
		t.Fatalf("job after the stop = %+v", j)
	}
	f.noFiles()
}

// TestVoiceWorkerOffUntilAManualJob: with the setting off the worker does not
// run, no source can queue a job, and a manual job starts a worker that leaves
// every other queued job alone.
func TestVoiceWorkerOffUntilAManualJob(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.Enabled = false
	f := newVoiceFixture(t, cfg)
	f.d.opts.VoiceTranscription = cfg
	f.d.opts.VoiceEngine = f.eng
	f.d.startVoice(f.root)
	f.w = f.d.voice
	if st := f.status(); st.State != rpc.VoiceStateDisabled || st.Enabled {
		t.Fatalf("status = %+v, want a disabled worker", st)
	}
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	for _, source := range []string{store.VoiceSourceIncoming, store.VoiceSourceSync, store.VoiceSourceBackfill} {
		if _, err := f.w.enqueue(context.Background(), store.VoiceJob{ChatID: 1001, MessageID: 1, Kind: store.VoiceKindVoice, Source: source, Priority: 10}, false); !errors.Is(err, errVoiceDisabled) {
			t.Fatalf("source %s queued a job while off: %v", source, err)
		}
	}
	if _, err := f.db.GetVoiceJob(context.Background(), 1001, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a job was queued: %v", err)
	}
	// A job a former run left behind stays queued; a manual job runs.
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	if _, err := f.w.enqueue(context.Background(), store.VoiceJob{ChatID: 1001, MessageID: 2, Kind: store.VoiceKindVoice, Source: store.VoiceSourceManual, Priority: 0}, true); err != nil {
		t.Fatal(err)
	}
	f.awaitState(1001, 2, store.VoiceStateDone)
	awaitCond(t, "an idle worker", func() bool { return f.status().State == rpc.VoiceStateIdle })
	// The worker claims the incoming job and gives it back at once; wait for
	// the moment it is queued again.
	awaitCond(t, "the incoming job to be left queued", func() bool { return f.job(1001, 1).State == store.VoiceStateQueued })
	if j := f.job(1001, 1); j.Attempts != 0 {
		t.Fatalf("the incoming job = %+v, want no attempt spent", j)
	}
	if f.tg.downloadCount() != 1 {
		t.Fatalf("downloads = %d, want only the manual one", f.tg.downloadCount())
	}
	if f.tg.transcribed() != 0 {
		t.Fatal("messages.transcribeAudio was called")
	}
}

// TestVoiceWorkerStartsOnlyWhenEnabled: the daemon starts the goroutine when the
// setting is on and not before.
func TestVoiceWorkerStartsOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			cfg := voiceTestConfig()
			cfg.Enabled = enabled
			f := newVoiceFixture(t, cfg)
			f.d.opts.VoiceTranscription = cfg
			f.d.opts.VoiceEngine = f.eng
			f.d.startVoice(f.root)
			f.w = f.d.voice
			f.w.mu.Lock()
			started := f.w.started
			f.w.mu.Unlock()
			if started != enabled {
				t.Fatalf("started = %v, want %v", started, enabled)
			}
			if enabled {
				awaitCond(t, "an idle worker", func() bool { return f.status().State == rpc.VoiceStateIdle })
				// The start prepared a private working directory.
				awaitCond(t, "the working directory", func() bool {
					info, err := os.Stat(f.w.dir)
					return err == nil && info.Mode().Perm() == 0o700
				})
			} else if _, err := os.Stat(f.w.dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the working directory exists while off: %v", err)
			}
			if f.tg.downloadCount() != 0 || f.tg.transcribed() != 0 {
				t.Fatal("the daemon asked Telegram for something on its own")
			}
		})
	}
}

// TestVoiceWorkerStatusStates walks the states a status shows.
func TestVoiceWorkerStatusStates(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.DailyFiles = 1
	f := newVoiceFixture(t, cfg)
	if st := f.status(); st.State != rpc.VoiceStateDisabled || !st.Enabled || !st.Installed || st.DailyFiles != 1 || st.DailyBytes != 2000<<20 {
		t.Fatalf("before the start = %+v", st)
	}
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)
	f.seed(1001, 2, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 1, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.queue(1001, 2, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.start()
	awaitCond(t, "the budget pause", func() bool {
		st := f.status()
		return st.State == rpc.VoiceStatePaused && st.Queue.Queued == 1
	})
	st := f.status()
	if st.Paused != rpc.VoicePausedBudget || st.PausedUntil == nil || *st.PausedUntil != f.daysAfter(1).Unix() || st.Queue.Queued != 1 || st.Queue.Done != 1 || st.DoneToday != 1 || st.FilesToday != 1 {
		t.Fatalf("paused status = %+v", st)
	}
	f.clock.AdvanceTo(f.daysAfter(1))
	f.awaitState(1001, 2, store.VoiceStateDone)
	awaitCond(t, "an idle worker", func() bool { return f.status().State == rpc.VoiceStateIdle })
	if st := f.status(); st.Paused != "" || st.PausedUntil != nil || st.DoneToday != 1 {
		t.Fatalf("status the next day = %+v", st)
	}
	f.eng.setMissing("runtime")
	f.seed(1001, 3, store.VoiceKindVoice, 100, 3)
	f.queue(1001, 3, store.VoiceKindVoice, store.VoiceSourceManual, 0)
	f.w.poke()
	awaitCond(t, "the engine pause", func() bool { return f.status().Paused == rpc.VoicePausedEngineMissing })
	if st := f.status(); st.State != rpc.VoiceStatePaused || st.Installed || len(st.Missing) != 1 || st.Missing[0] != "runtime" {
		t.Fatalf("engine status = %+v", st)
	}
}

// TestVoiceWorkerStatusHoldsNoTextAndNoPath: what a status and a failure carry
// are machine words. The file name of the message and the recognised text never
// reach them.
func TestVoiceWorkerStatusHoldsNoTextAndNoPath(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.eng.transcribeErr = fmt.Errorf("sherpa failed on %s with text секретный текст", f.dir)
	f.seed(1001, 7, store.VoiceKindVoice, 100, 0)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, 10)
	f.w.recover(f.root)
	f.drain()
	j := f.job(1001, 7)
	st := f.status()
	for _, text := range []string{fmt.Sprint(j), fmt.Sprint(st)} {
		for _, secret := range []string{f.dir, "секретный", "secret-name", ".media", ".wav"} {
			if strings.Contains(text, secret) {
				t.Fatalf("%q leaked into %s", secret, text)
			}
		}
	}
	if j.Reason != "engine" || st.LastFailure != "engine" {
		t.Fatalf("job = %+v status = %+v", j, st)
	}
}

func TestVoiceMediaInfo(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want voiceMedia
	}{
		"voice":                {string(voiceRaw(store.VoiceKindVoice, 2048, 12)), voiceMedia{Size: 2048, Seconds: 12}},
		"video note":           {string(voiceRaw(store.VoiceKindVideoNote, 4096, 5.5)), voiceMedia{Size: 4096, Seconds: 5.5}},
		"video":                {string(voiceRaw(store.VoiceKindVideo, 1<<30, 3600.25)), voiceMedia{Size: 1 << 30, Seconds: 3600.25}},
		"no size":              {string(voiceRaw(store.VoiceKindVoice, 0, 12)), voiceMedia{Seconds: 12}},
		"no duration":          {string(voiceRaw(store.VoiceKindVoice, 2048, 0)), voiceMedia{Size: 2048}},
		"old format, no media": {`{"_":"message","id":4}`, voiceMedia{}},
		"empty object":         {`{}`, voiceMedia{}},
		"empty":                {``, voiceMedia{}},
		"not json":             {`oops`, voiceMedia{}},
		"photo":                {`{"media":{"_":"messageMediaPhoto","photo":{"_":"photo"}}}`, voiceMedia{}},
		"negative size":        {`{"media":{"document":{"size":-5}}}`, voiceMedia{}},
		"other attributes":     {`{"media":{"document":{"size":7,"attributes":[{"_":"documentAttributeImageSize","duration":99}]}}}`, voiceMedia{Size: 7}},
		"longest duration":     {`{"media":{"document":{"attributes":[{"_":"documentAttributeAudio","duration":3},{"_":"documentAttributeVideo","duration":9.5}]}}}`, voiceMedia{Seconds: 9.5}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := voiceMediaInfo([]byte(tc.raw)); got != tc.want {
				t.Fatalf("voiceMediaInfo = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestVoiceText(t *testing.T) {
	got := voiceText([]VoiceSegment{{Text: " раз "}, {Text: ""}, {Text: "  "}, {Text: "два\nтри"}})
	if got != "раз два\nтри" {
		t.Fatalf("voiceText = %q", got)
	}
	if voiceText(nil) != "" {
		t.Fatal("no segments, no text")
	}
}

func TestVoiceWorkerConfigDefaults(t *testing.T) {
	got := (VoiceTranscriptionConfig{}).withDefaults()
	want := VoiceTranscriptionConfig{FFmpeg: "ffmpeg", MaxVoiceMB: 30, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120, DailyDownloadMB: 5000, DailyFiles: 500, Threads: 4}
	if got != want {
		t.Fatalf("withDefaults = %+v, want %+v", got, want)
	}
	set := VoiceTranscriptionConfig{FFmpeg: "/x/ffmpeg", MaxVoiceMB: 1, Threads: 16, Pause: time.Second}
	if got := set.withDefaults(); got.FFmpeg != "/x/ffmpeg" || got.MaxVoiceMB != 1 || got.Threads != 16 || got.Pause != time.Second {
		t.Fatalf("withDefaults changed set values: %+v", got)
	}
}
