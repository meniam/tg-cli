package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/voice/gigaam"
)

// runVoiceCLI runs one command against a fake daemon that serves the voice
// methods. env is added to the environment of the command.
func runVoiceCLI(t *testing.T, args []string, env map[string]string, reply func(string, json.RawMessage) (any, error)) discoveryRun {
	t.Helper()
	home := audit02Home(t)
	serverSide, clientSide := net.Pipe()
	server := rpc.NewServer(nil)
	calls := make(chan struct {
		method string
		params json.RawMessage
	}, 10)
	for _, method := range []string{rpc.MethodVoiceStatus, rpc.MethodVoiceCancel, rpc.MethodVoiceRetry, rpc.MethodVoiceBackfill} {
		server.Handle(method, func(_ context.Context, raw json.RawMessage) (any, error) {
			calls <- struct {
				method string
				params json.RawMessage
			}{method, raw}
			return reply(method, raw)
		})
	}
	done := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(done) }()
	var stdout, stderr bytes.Buffer
	deps := Deps{Stdout: &stdout, Stderr: &stderr, ConfigPath: audit02ConfigPath(home), CallTimeout: audit02CallTimeout, Getenv: func(name string) string {
		if v, ok := env[name]; ok {
			return v
		}
		if name == "TG_CLI_HOME" {
			return home
		}
		return ""
	}, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientSide), nil }}
	code := Execute(context.Background(), args, deps)
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	server.Close()
	result := discoveryRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
	select {
	case call := <-calls:
		result.method, result.params = call.method, call.params
	default:
	}
	return result
}

func TestVoiceStatusCommand(t *testing.T) {
	r := runVoiceCLI(t, []string{"voice", "status"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.VoiceStatus{Enabled: true, Installed: true, State: rpc.VoiceStatePaused, Paused: rpc.VoicePausedBudget, Queue: rpc.VoiceQueueCounts{Queued: 3, Done: 9}, DailyFiles: 200}, nil
	})
	if r.code != 0 || r.method != rpc.MethodVoiceStatus || !strings.Contains(r.stdout, `"state":"paused"`) || !strings.Contains(r.stdout, `"paused":"budget"`) || !strings.Contains(r.stdout, `"queued":3`) {
		t.Fatalf("status: %+v", r)
	}
}

func TestVoiceCancelCommand(t *testing.T) {
	reply := func(string, json.RawMessage) (any, error) {
		return rpc.VoiceCancelResult{Skipped: 2, Stopped: true}, nil
	}
	r := runVoiceCLI(t, []string{"voice", "cancel"}, nil, reply)
	var p rpc.VoiceCancelParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || p != (rpc.VoiceCancelParams{}) || !strings.Contains(r.stdout, `"skipped":2`) {
		t.Fatalf("cancel all: %+v %+v %v", r, p, err)
	}
	for _, args := range [][]string{{"voice", "cancel", "@alice", "7"}, {"voice", "cancel", "https://t.me/alice/7"}} {
		r = runVoiceCLI(t, args, nil, reply)
		p = rpc.VoiceCancelParams{}
		if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || p.Chat != "@alice" || p.ID != 7 {
			t.Fatalf("%v: %+v %+v %v", args, r, p, err)
		}
	}
	// One argument that is not a message link, and a bad id, are usage errors
	// found before the Daemon is called.
	for _, args := range [][]string{{"voice", "cancel", "@alice"}, {"voice", "cancel", "@alice", "0"}, {"voice", "cancel", "@alice", "x"}, {"voice", "cancel", "a", "1", "2"}} {
		if r = runVoiceCLI(t, args, nil, reply); r.code != exitcode.Usage || r.method != "" {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}

func TestVoiceRetryCommand(t *testing.T) {
	r := runVoiceCLI(t, []string{"voice", "retry", "--", "-1001234567890", "55"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.VoiceRetryResult{ChatID: -1001234567890, ID: 55, Kind: "voice", Queued: true, State: "queued"}, nil
	})
	var p rpc.VoiceRetryParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || p.Chat != "-1001234567890" || p.ID != 55 || !strings.Contains(r.stdout, `"queued":true`) {
		t.Fatalf("retry: %+v %+v %v", r, p, err)
	}
	if r = runVoiceCLI(t, []string{"voice", "retry"}, nil, nil); r.code != exitcode.Usage || r.method != "" {
		t.Fatalf("retry without arguments: %+v", r)
	}
	// The engine is missing: the reason of the daemon reaches the answer.
	r = runVoiceCLI(t, []string{"voice", "retry", "@alice", "7"}, nil, func(string, json.RawMessage) (any, error) {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "the local speech engine is not installed (missing: model): run `tg voice install`", Data: map[string]any{"reason": "engine_missing"}}
	})
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, `"reason":"engine_missing"`) || !strings.Contains(r.stdout, "tg voice install") {
		t.Fatalf("missing engine: %+v", r)
	}
}

func TestVoiceBackfillCommand(t *testing.T) {
	r := runVoiceCLI(t, []string{"voice", "backfill", "--dry-run"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.VoiceBackfillResult{Enabled: false, DryRun: true, Chats: 2, Candidates: 40}, nil
	})
	var p rpc.VoiceBackfillParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || !p.DryRun || !strings.Contains(r.stdout, `"candidates":40`) || !strings.Contains(r.stderr, "enabled = true") {
		t.Fatalf("dry run while off: %+v %+v %v", r, p, err)
	}
	r = runVoiceCLI(t, []string{"voice", "backfill"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.VoiceBackfillResult{Enabled: true, Chats: 2, Candidates: 40, Queued: 40}, nil
	})
	p = rpc.VoiceBackfillParams{}
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || p.DryRun || !strings.Contains(r.stdout, `"queued":40`) || r.stderr != "" {
		t.Fatalf("backfill: %+v %+v %v", r, p, err)
	}
	r = runVoiceCLI(t, []string{"voice", "backfill"}, nil, func(string, json.RawMessage) (any, error) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "voice transcription is off: set [voice_transcription] enabled = true")
	})
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, "enabled = true") {
		t.Fatalf("backfill while off: %+v", r)
	}
}

// installEngineFiles writes the files of a ready engine under home, the way the
// installer would leave them.
func installEngineFiles(t *testing.T, home string) {
	t.Helper()
	p := gigaam.NewPaths(home)
	for path, mode := range map[string]os.FileMode{
		p.Binary: 0o700, filepath.Join(p.LibDir, "libonnxruntime.dylib"): 0o600,
		p.Encoder: 0o600, p.Decoder: 0o600, p.Joiner: 0o600, p.Tokens: 0o600, p.VAD: 0o600,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVoiceInstallCheck(t *testing.T) {
	home := t.TempDir()
	missingFFmpeg := map[string]string{"TG_CLI_HOME": home, "TG_CLI_VOICE_TRANSCRIPTION_FFMPEG": filepath.Join(home, "no-ffmpeg")}
	r := runVoiceCLI(t, []string{"voice", "install", "--check"}, missingFFmpeg, nil)
	if r.code != 0 || r.method != "" || !strings.Contains(r.stdout, `"installed":false`) || !strings.Contains(r.stdout, `"checked":true`) ||
		!strings.Contains(r.stdout, `"missing":["ffmpeg","runtime","model","vad"]`) {
		t.Fatalf("empty home: %+v", r)
	}
	if strings.Contains(r.stdout, home) || strings.Contains(r.stderr, home) {
		t.Fatalf("a path leaked: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, "models")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("--check created something: %v", err)
	}

	installEngineFiles(t, home)
	ffmpeg := filepath.Join(home, "fake-ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r = runVoiceCLI(t, []string{"voice", "install", "--check"}, map[string]string{"TG_CLI_HOME": home, "TG_CLI_VOICE_TRANSCRIPTION_FFMPEG": ffmpeg}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"installed":true`) || !strings.Contains(r.stdout, `"missing":[]`) {
		t.Fatalf("ready engine: %+v", r)
	}
	// Only ffmpeg is missing: the engine parts are named apart from it.
	r = runVoiceCLI(t, []string{"voice", "install", "--check"}, missingFFmpeg, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"installed":false`) || !strings.Contains(r.stdout, `"missing":["ffmpeg"]`) {
		t.Fatalf("no ffmpeg: %+v", r)
	}
}

// TestVoiceInstallOfAReadyEngineDownloadsNothing runs the installer on a ready
// engine: it skips every component, so no network is needed.
func TestVoiceInstallOfAReadyEngineDownloadsNothing(t *testing.T) {
	home := t.TempDir()
	installEngineFiles(t, home)
	ffmpeg := filepath.Join(home, "fake-ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := runVoiceCLI(t, []string{"voice", "install"}, map[string]string{"TG_CLI_HOME": home, "TG_CLI_VOICE_TRANSCRIPTION_FFMPEG": ffmpeg}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"installed":true`) || !strings.Contains(r.stdout, `"checked":false`) {
		t.Fatalf("install: %+v", r)
	}
	for _, step := range []string{"runtime:skip", "model:skip", "vad:skip"} {
		if !strings.Contains(r.stderr, "voice install: "+step) {
			t.Fatalf("no %s step on stderr: %q", step, r.stderr)
		}
	}
}

// TestVoiceInstallFailureNamesTheClassOnly: the error of the installer may hold
// a path or a link; the command shows neither.
func TestVoiceInstallFailureNamesTheClassOnly(t *testing.T) {
	secret := "/Users/someone/.config/tg-cli/models https://example.invalid/x"
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"interrupted": {context.Canceled, "interrupted"},
		"network":     {fmt.Errorf("install runtime: %w: %s", gigaam.ErrDownload, secret), "the download failed"},
		"size":        {fmt.Errorf("%w: %s", gigaam.ErrSize, secret), "pinned size or SHA-256"},
		"checksum":    {fmt.Errorf("%w: %s", gigaam.ErrChecksum, secret), "pinned size or SHA-256"},
		"archive":     {fmt.Errorf("%w: %s", gigaam.ErrUnsafeArchive, secret), "refused as unsafe"},
		"incomplete":  {fmt.Errorf("%w: %s", gigaam.ErrIncomplete, secret), "misses required files"},
		"other":       {errors.New("create " + secret), "the installation failed"},
	} {
		t.Run(name, func(t *testing.T) {
			got := voiceInstallFailure(tc.err)
			var ce *cmdError
			if !errors.As(got, &ce) || ce.exit != exitcode.Failure || !strings.Contains(ce.msg, tc.want) || strings.Contains(ce.msg, "/Users") || strings.Contains(ce.msg, "example.invalid") {
				t.Fatalf("got %v, want a failure containing %q without the path", got, tc.want)
			}
		})
	}
}

func TestDaemonVoiceTranscriptionDefaults(t *testing.T) {
	a := backgroundSyncApp(t, "", nil)
	a.config.Home = "/data/tg"
	got, err := a.daemonVoiceTranscription()
	if err != nil {
		t.Fatal(err)
	}
	want := daemon.VoiceTranscriptionConfig{
		Home: "/data/tg", FFmpeg: "ffmpeg", MaxVoiceMB: 30, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120,
		DailyDownloadMB: 2000, DailyFiles: 200, Pause: 3 * time.Second, Threads: 4,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v (disabled)", got, want)
	}
}

func TestDaemonVoiceTranscriptionEnvironmentOverridesFile(t *testing.T) {
	file := "[voice_transcription]\nenabled = false\nffmpeg = \"/opt/ffmpeg\"\nmax_voice_mb = 10\nthreads = 2\npause = \"1s\"\n"
	fromFile := daemon.VoiceTranscriptionConfig{
		Home: "/data/tg", FFmpeg: "/opt/ffmpeg", MaxVoiceMB: 10, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120,
		DailyDownloadMB: 2000, DailyFiles: 200, Pause: time.Second, Threads: 2,
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want daemon.VoiceTranscriptionConfig
	}{
		{"file only", nil, fromFile},
		{"every variable", map[string]string{
			"TG_CLI_VOICE_TRANSCRIPTION_ENABLED":           " true ",
			"TG_CLI_VOICE_TRANSCRIPTION_FFMPEG":            "/env/ffmpeg",
			"TG_CLI_VOICE_TRANSCRIPTION_MAX_VOICE_MB":      "11",
			"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_NOTE_MB": "22",
			"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MB":      "33",
			"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MINUTES": "44",
			"TG_CLI_VOICE_TRANSCRIPTION_DAILY_DOWNLOAD_MB": "55",
			"TG_CLI_VOICE_TRANSCRIPTION_DAILY_FILES":       "66",
			"TG_CLI_VOICE_TRANSCRIPTION_PAUSE":             "0s",
			"TG_CLI_VOICE_TRANSCRIPTION_THREADS":           "16",
		}, daemon.VoiceTranscriptionConfig{
			Enabled: true, Home: "/data/tg", FFmpeg: "/env/ffmpeg", MaxVoiceMB: 11, MaxVideoNoteMB: 22, MaxVideoMB: 33, MaxVideoMinutes: 44,
			DailyDownloadMB: 55, DailyFiles: 66, Pause: 0, Threads: 16,
		}},
		{"one variable keeps the rest of the file", map[string]string{"TG_CLI_VOICE_TRANSCRIPTION_THREADS": "8"},
			daemon.VoiceTranscriptionConfig{Home: "/data/tg", FFmpeg: "/opt/ffmpeg", MaxVoiceMB: 10, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120, DailyDownloadMB: 2000, DailyFiles: 200, Pause: time.Second, Threads: 8}},
		{"blank variable is ignored", map[string]string{"TG_CLI_VOICE_TRANSCRIPTION_ENABLED": "  ", "TG_CLI_VOICE_TRANSCRIPTION_PAUSE": " "}, fromFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := backgroundSyncApp(t, file, tc.env)
			a.config.Home = "/data/tg"
			got, err := a.daemonVoiceTranscription()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDaemonVoiceTranscriptionRejectsBadEnvironment(t *testing.T) {
	for name, tc := range map[string]struct{ variable, value string }{
		"enabled":         {"TG_CLI_VOICE_TRANSCRIPTION_ENABLED", "yes"},
		"ffmpeg control":  {"TG_CLI_VOICE_TRANSCRIPTION_FFMPEG", "ff\x07mpeg"},
		"voice zero":      {"TG_CLI_VOICE_TRANSCRIPTION_MAX_VOICE_MB", "0"},
		"voice negative":  {"TG_CLI_VOICE_TRANSCRIPTION_MAX_VOICE_MB", "-5"},
		"note text":       {"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_NOTE_MB", "big"},
		"video huge":      {"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MB", "1000001"},
		"minutes zero":    {"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MINUTES", "0"},
		"budget zero":     {"TG_CLI_VOICE_TRANSCRIPTION_DAILY_DOWNLOAD_MB", "0"},
		"files negative":  {"TG_CLI_VOICE_TRANSCRIPTION_DAILY_FILES", "-1"},
		"pause bad":       {"TG_CLI_VOICE_TRANSCRIPTION_PAUSE", "soon"},
		"pause negative":  {"TG_CLI_VOICE_TRANSCRIPTION_PAUSE", "-1s"},
		"pause too long":  {"TG_CLI_VOICE_TRANSCRIPTION_PAUSE", "2h"},
		"threads zero":    {"TG_CLI_VOICE_TRANSCRIPTION_THREADS", "0"},
		"threads too big": {"TG_CLI_VOICE_TRANSCRIPTION_THREADS", "17"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := backgroundSyncApp(t, "", map[string]string{tc.variable: tc.value}).daemonVoiceTranscription()
			var ce *cmdError
			if !errors.As(err, &ce) || ce.exit != exitcode.Usage || !strings.Contains(ce.msg, tc.variable) {
				t.Fatalf("got %v, want a usage error naming %s", err, tc.variable)
			}
		})
	}
}
