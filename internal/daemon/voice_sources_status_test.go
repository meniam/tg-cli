package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// voiceStatusFixture is the daemon of the dashboard tests with a voice worker
// that never runs its goroutine: the tests look at the block, not at the work.
func voiceStatusFixture(t *testing.T, cfg VoiceTranscriptionConfig, missing ...string) (*daemon, *voiceFakeEngine, *schedClock) {
	t.Helper()
	d, tg, _, _ := dashboardFixture(t)
	d.login = newLoginManager(context.Background(), tg)
	d.opts.Paths.DatabaseDir = t.TempDir()
	eng := &voiceFakeEngine{missing: missing}
	clock := newSchedClock(voiceT0)
	d.voice = newVoiceWorker(d, context.Background(), cfg, eng, clock)
	return d, eng, clock
}

// statusVoice returns the voice block of the daemon status as raw JSON and as a
// value.
func statusVoice(t *testing.T, d *daemon) (string, rpc.VoiceStatus) {
	t.Helper()
	result, err := d.status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["voice"]
	if !ok {
		t.Fatalf("the status has no voice block: %s", data)
	}
	var block rpc.VoiceStatus
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatal(err)
	}
	return string(raw), block
}

// TestVoiceSourceStatusOff: the block is in the status and in the dashboard
// while the feature is off; it says what is missing and starts nothing.
func TestVoiceSourceStatusOff(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.Enabled = false
	d, _, _ := voiceStatusFixture(t, cfg, "ffmpeg", "runtime", "model", "vad")
	const want = `{"enabled":false,"installed":false,"missing":["ffmpeg","runtime","model","vad"],"state":"disabled","queue":{"queued":0,"running":0,"done":0,"failed":0,"skipped":0},"done_today":0,"files_today":0,"bytes_today":0,"daily_files":200,"daily_bytes":2097152000}`
	raw, block := statusVoice(t, d)
	if raw != want {
		t.Errorf("daemon status voice = %s\nwant %s", raw, want)
	}
	if dash := callDashboard(t, d); !reflect.DeepEqual(dash.Voice, block) {
		t.Errorf("dashboard voice = %+v, status voice = %+v", dash.Voice, block)
	}
	if d.voice.started {
		t.Fatal("building the block started the worker")
	}
}

// TestVoiceSourceStatusOn: the queue counters, today's work, the pause and the
// last failure reach the status and the dashboard, and nothing in the block
// names a message, a text or a path.
func TestVoiceSourceStatusOn(t *testing.T) {
	d, _, clock := voiceStatusFixture(t, voiceTestConfig())
	ctx := context.Background()
	w := d.voice
	for id, state := range map[int]string{1: store.VoiceStateQueued, 2: store.VoiceStateQueued, 3: store.VoiceStateQueued, 4: store.VoiceStateDone} {
		job := store.VoiceJob{ChatID: 1, MessageID: id, Kind: store.VoiceKindVoice, Source: store.VoiceSourceIncoming, Priority: store.VoicePriorityIncoming}
		if _, err := d.store.EnqueueVoice(ctx, job, false); err != nil {
			t.Fatal(err)
		}
		if state == store.VoiceStateDone {
			j, ok, err := d.store.ClaimNextVoice(ctx, voiceT0)
			if err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			if err := d.store.FinishVoice(ctx, j.ChatID, j.MessageID, store.VoiceStateDone, "", 10, voiceT0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := d.store.AddVoiceUsage(ctx, localDay(voiceT0), 3, 48<<20); err != nil {
		t.Fatal(err)
	}
	until := clock.Now().Add(time.Hour)
	w.mu.Lock()
	w.started, w.paused, w.pausedUntil = true, rpc.VoicePausedBudget, until
	w.lastFailure = "too_large"
	w.doneDay, w.doneToday = localDay(voiceT0), 7
	w.mu.Unlock()

	raw, block := statusVoice(t, d)
	untilUnix := until.Unix()
	want := rpc.VoiceStatus{
		Enabled: true, Installed: true, State: rpc.VoiceStatePaused, Paused: rpc.VoicePausedBudget, PausedUntil: &untilUnix,
		Queue:     rpc.VoiceQueueCounts{Queued: 3, Done: 1},
		DoneToday: 7, FilesToday: 3, BytesToday: 48 << 20, DailyFiles: 200, DailyBytes: 2000 << 20, LastFailure: "too_large",
	}
	if !reflect.DeepEqual(block, want) {
		t.Errorf("daemon status voice = %+v\nwant %+v", block, want)
	}
	if dash := callDashboard(t, d); !reflect.DeepEqual(dash.Voice, want) {
		t.Errorf("dashboard voice = %+v\nwant %+v", dash.Voice, want)
	}
	for _, leak := range []string{d.opts.Paths.DatabaseDir, voiceTempDirName, "/", ".media"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the voice block carries %q: %s", leak, raw)
		}
	}
	// A running job is named by ids and kind only.
	j, ok, err := d.store.ClaimNextVoice(ctx, voiceT0)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w.mu.Lock()
	w.paused, w.pausedUntil = "", time.Time{}
	w.current = &voiceRun{job: j, cancel: func() {}}
	w.mu.Unlock()
	_, block = statusVoice(t, d)
	if block.State != rpc.VoiceStateWorking || block.Current == nil || block.Current.ChatID != 1 || block.Queue.Running != 1 || block.Queue.Queued != 2 {
		t.Errorf("working block = %+v", block)
	}
}

// TestVoiceSourceStatusWithoutAWorker: a daemon built without a worker still
// answers, with the block of a feature that is off.
func TestVoiceSourceStatusWithoutAWorker(t *testing.T) {
	d, _, _ := voiceStatusFixture(t, voiceTestConfig())
	d.voice = nil
	if _, block := statusVoice(t, d); block.State != rpc.VoiceStateDisabled || block.Enabled {
		t.Errorf("block = %+v", block)
	}
	if dash := callDashboard(t, d); dash.Voice.State != rpc.VoiceStateDisabled {
		t.Errorf("dashboard voice = %+v", dash.Voice)
	}
}
