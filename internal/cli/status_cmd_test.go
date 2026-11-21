package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// statusZone is not the zone of the machine running the test.
var statusZone = time.FixedZone("msk", 3*3600)

// statusNow is the moment the golden files are written for: 12:00 on 2026-11-20.
func statusNow() time.Time { return time.Date(2026, time.November, 20, 12, 0, 0, 0, statusZone) }

func statusAt(day, hour, minute int) *int64 {
	unix := time.Date(2026, time.November, day, hour, minute, 0, 0, statusZone).Unix()
	return &unix
}

// statusBase is a healthy daemon with the background load on; each case changes
// what it tests.
func statusBase() rpc.Dashboard {
	user := &rpc.User{ID: 114954417, Username: "name"}
	return rpc.Dashboard{
		Profile:    "default",
		PID:        80307,
		StartedAt:  statusNow().Add(-(3*24*time.Hour + 4*time.Hour + 5*time.Minute)).Unix(),
		Account:    rpc.DashboardAccount{User: user, Authorized: true},
		Connection: rpc.DashboardConnection{Connection: "ready", Transport: "tcp", DC: 2},
		Access:     rpc.AccessStatus{Write: 1},
		Database:   rpc.DashboardDatabase{SizeBytes: 107_300_000, SchemaVersion: 9, Chats: 245, Messages: 67583},
		History: rpc.DashboardHistory{
			Complete: 4, Total: 245,
			People:  rpc.DashboardGroup{Complete: 3, Total: 27},
			Folders: rpc.DashboardGroup{Complete: 0, Total: 12},
			Others:  rpc.DashboardGroup{Complete: 1, Total: 206},
		},
		Background: rpc.BackgroundSyncStatus{
			Enabled: true, Window: rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"},
			State: rpc.BackgroundSyncWaitingInterval, NextRunAt: statusAt(20, 14, 35), LastRunAt: statusAt(20, 11, 2), LastResult: rpc.BackgroundSyncDone, LastChatID: func() *int64 { id := int64(123); return &id }(),
		},
		SemanticSearch: rpc.SemanticIndexStatus{
			Enabled: true, State: rpc.SemanticIndexIdle, Model: "bge-m3", Indexed: 6500, Total: 6500, FinishedAt: statusAt(20, 18, 20),
		},
		Voice: rpc.VoiceStatus{
			Enabled: true, Installed: true, State: rpc.VoiceStatePaused, Paused: rpc.VoicePausedBudget, PausedUntil: statusAt(21, 0, 0),
			Queue: rpc.VoiceQueueCounts{Queued: 3, Done: 40}, DoneToday: 12, FilesToday: 12, BytesToday: 48 << 20, DailyFiles: 200, DailyBytes: 2000 << 20,
		},
		Jobs:   rpc.DashboardJobs{Sync: []rpc.DashboardSyncJob{}, Full: rpc.DashboardFullJob{State: rpc.ChatFullIdle}},
		Events: rpc.DashboardEvents{LastHour: 12, Total: 71052},
	}
}

func TestRenderDashboardMatchesGolden(t *testing.T) {
	t.Parallel()
	off := statusBase()
	off.StartedAt = statusNow().Add(-90 * time.Second).Unix()
	off.Access = rpc.AccessStatus{Enabled: true, Allow: 2, Block: 1}
	off.Database = rpc.DashboardDatabase{SizeBytes: 2_560_000, SchemaVersion: 10, Chats: 1, Messages: 1}
	off.History = rpc.DashboardHistory{Total: 1, People: rpc.DashboardGroup{Total: 1}}
	off.Background = rpc.BackgroundSyncStatus{State: rpc.BackgroundSyncDisabled, Window: rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"}}
	off.SemanticSearch = rpc.SemanticIndexStatus{State: rpc.SemanticIndexDisabled}
	off.Voice = rpc.VoiceStatus{State: rpc.VoiceStateDisabled}
	off.Events = rpc.DashboardEvents{Total: 1}

	// A daemon that lost the login and has no client: what is still known is shown.
	lost := rpc.Dashboard{
		Profile: "work", PID: 12,
		Account:  rpc.DashboardAccount{User: &rpc.User{ID: 5, Username: "gone"}},
		Database: rpc.DashboardDatabase{SchemaVersion: 10},
		Background: rpc.BackgroundSyncStatus{
			Enabled: true, Window: rpc.BackgroundSyncWindow{Start: "09:30", End: "18:00"},
			State: rpc.BackgroundSyncWaitingWindow, NextRunAt: statusAt(21, 10, 5),
		},
		SemanticSearch: rpc.SemanticIndexStatus{
			Enabled: true, State: rpc.SemanticIndexUnavailable, Reason: "ollama is unavailable", Model: "bge-m3", Indexed: 6500, Total: 6500,
		},
		Voice: rpc.VoiceStatus{Enabled: true, Missing: []string{"runtime", "model"}, State: rpc.VoiceStatePaused, Paused: rpc.VoicePausedEngineMissing},
	}

	busy := statusBase()
	busy.StartedAt = statusNow().Add(-(4*time.Hour + 12*time.Minute)).Unix()
	busy.Connection = rpc.DashboardConnection{Connection: "ready", Transport: "auto", ActiveTransport: "wss", DC: 4, Reconnects: 2}
	busy.Access = rpc.AccessStatus{Enabled: true, Closed: true}
	busy.Database = rpc.DashboardDatabase{SizeBytes: 5_000, SchemaVersion: 10}
	busy.History = rpc.DashboardHistory{}
	busy.Background = rpc.BackgroundSyncStatus{
		Enabled: true, Window: rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"}, State: rpc.BackgroundSyncRunning,
		LastRunAt: statusAt(19, 19, 40), LastResult: rpc.BackgroundSyncCancelled, LastChatID: func() *int64 { id := int64(-1001); return &id }(), CurrentChatID: func() *int64 { id := int64(-1001); return &id }(),
	}
	busy.Jobs = rpc.DashboardJobs{
		Sync: []rpc.DashboardSyncJob{
			{ChatID: -1001, State: rpc.HistorySyncRunning, Source: rpc.HistorySyncBackground, Pages: 4, Loaded: 400},
			{ChatID: 77, State: rpc.HistorySyncWaitingFlood, Source: rpc.HistorySyncManual, Pages: 1, Loaded: 100},
		},
		Full: rpc.DashboardFullJob{State: rpc.ChatFullRunning, Done: 12, Total: 80},
	}
	busy.SemanticSearch = rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexIndexing, Model: "bge-m3", Indexed: 6382, Total: 6500, Embedded: 120}
	busy.Voice = rpc.VoiceStatus{
		Enabled: true, Installed: true, State: rpc.VoiceStateWorking, Queue: rpc.VoiceQueueCounts{Queued: 41, Running: 1, Done: 3, Failed: 1},
		DoneToday: 2, FilesToday: 3, BytesToday: 130 << 20, DailyFiles: 200, DailyBytes: 2000 << 20, LastFailure: "download",
	}
	busy.Throttle = rpc.DashboardThrottle{FloodWaitSeconds: 91, Queued: 3}
	busy.Events = rpc.DashboardEvents{}

	for _, tc := range []struct {
		golden string
		dash   rpc.Dashboard
	}{
		{"status_background_on.golden", statusBase()},
		{"status_background_off.golden", off},
		{"status_not_signed_in.golden", lost},
		{"status_job_and_flood_wait.golden", busy},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", tc.golden))
			if err != nil {
				t.Fatal(err)
			}
			if got := renderDashboard(tc.dash, statusNow()); got != string(want) {
				t.Errorf("text differs from %s\n--- got\n%s--- want\n%s", tc.golden, got, want)
			}
		})
	}
}

// The text depends on the time passed in, not on the machine clock or zone.
func TestRenderDashboardUsesTheGivenTimeAndZone(t *testing.T) {
	t.Parallel()
	dash := statusBase()
	utc := statusNow().UTC() // 09:00 on the same day
	got := renderDashboard(dash, utc)
	// 14:35 in the zone +3 is 11:35 UTC; 11:02 is 08:02 UTC; the day is still the 20th.
	if !strings.Contains(got, "next run 11:35, last run 08:02 (chat 123, done)") {
		t.Errorf("times are not shown in the zone of the given time:\n%s", got)
	}
	// A moment past midnight of the given zone is another day: the date is shown.
	dash.Background.NextRunAt = statusAt(21, 0, 30)
	if got := renderDashboard(dash, statusNow()); !strings.Contains(got, "next run 2026-11-21 00:30,") {
		t.Errorf("another day lacks its date:\n%s", got)
	}
}

// A skipped run picked no chat: last_chat_id may hold the chat of an earlier run,
// and the line must not blame it. A run without any chat on record prints none.
func TestRenderDashboardLastRunChat(t *testing.T) {
	t.Parallel()
	dash := statusBase() // last run done in chat 123
	dash.Background.LastResult = rpc.BackgroundSyncSkipped
	if got := renderDashboard(dash, statusNow()); !strings.Contains(got, "last run 11:02 (skipped)\n") {
		t.Errorf("a skipped run names a chat:\n%s", got)
	}
	dash.Background.LastResult = rpc.BackgroundSyncDone
	dash.Background.LastChatID = nil
	if got := renderDashboard(dash, statusNow()); !strings.Contains(got, "last run 11:02 (done)\n") {
		t.Errorf("a run without a chat on record:\n%s", got)
	}
}

// A name of Telegram is text of someone else: a control character in it must
// not reach the terminal.
func TestRenderDashboardStripsControlCharacters(t *testing.T) {
	t.Parallel()
	dash := statusBase()
	dash.Account.User = &rpc.User{ID: 9, FirstName: "Ann\x1b[2J", LastName: "Lee"}
	got := renderDashboard(dash, statusNow())
	if strings.ContainsRune(got, '\x1b') || !strings.Contains(got, "Ann [2J Lee (id 9), signed in") {
		t.Errorf("account row: %q", got)
	}
}

type statusRun struct {
	code           int
	stdout, stderr string
	called         bool
}

// runStatusCLI runs `tg status` against a daemon that answers the dashboard
// method with reply. A nil reply means no daemon: the dial is refused.
func runStatusCLI(t *testing.T, args []string, reply func() (any, error)) statusRun {
	t.Helper()
	home := audit01Isolate(t)
	var called bool
	server := rpc.NewServer(nil)
	deps := Deps{
		ConfigPath: filepath.Join(home, "config.toml"),
		Getenv: func(key string) string {
			if key == "TG_CLI_HOME" {
				return home
			}
			return ""
		},
	}
	done := make(chan struct{})
	close(done)
	var clientSide net.Conn
	if reply == nil {
		deps.DialRPC = func(context.Context, string, string) (*rpc.Client, error) {
			return nil, fmt.Errorf("dial unix: %w", syscall.ECONNREFUSED)
		}
	} else {
		var serverSide net.Conn
		serverSide, clientSide = net.Pipe()
		server.Handle(rpc.MethodDashboard, func(context.Context, json.RawMessage) (any, error) {
			called = true
			return reply()
		})
		done = make(chan struct{})
		go func() { server.ServeConn(serverSide); close(done) }()
		deps.DialRPC = func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientSide), nil }
	}
	var stdout, stderr bytes.Buffer
	deps.Stdout, deps.Stderr = &stdout, &stderr
	code := Execute(context.Background(), args, deps)
	if clientSide != nil {
		clientSide.Close()
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC server did not stop")
	}
	server.Close()
	return statusRun{code: code, stdout: stdout.String(), stderr: stderr.String(), called: called}
}

func TestStatusPrintsTheDashboardAsText(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"status", "--human"}, {"status", "--format", "human"}} {
		run := runStatusCLI(t, args, func() (any, error) { return statusBase(), nil })
		if run.code != exitcode.OK || !run.called {
			t.Fatalf("%v: exit %d, called %v, stderr %q", args, run.code, run.called, run.stderr)
		}
		for _, want := range []string{"tg status  profile default  daemon pid 80307, up ", "Account      @name (id 114954417), signed in", "Events       12 in the last hour, 71052 stored"} {
			if !strings.Contains(run.stdout, want) {
				t.Errorf("%v: stdout lacks %q:\n%s", args, want, run.stdout)
			}
		}
		if strings.HasPrefix(strings.TrimSpace(run.stdout), "{") {
			t.Errorf("%v: the output is an envelope, not text: %q", args, run.stdout)
		}
	}
}

// A daemon that lost the login still answers: the command prints what it has,
// says the account is not signed in and exits with 0.
func TestStatusWithoutLoginExitsZero(t *testing.T) {
	dash := statusBase()
	dash.Account = rpc.DashboardAccount{}
	run := runStatusCLI(t, []string{"status"}, func() (any, error) { return dash, nil })
	if run.code != exitcode.OK || !strings.Contains(run.stdout, "Account      not signed in\n") {
		t.Fatalf("exit %d: %q", run.code, run.stdout)
	}
}

func TestStatusRefusesAJSONFormat(t *testing.T) {
	for _, format := range []string{"json", "jsonl", "toon"} {
		run := runStatusCLI(t, []string{"status", "--format", format}, func() (any, error) { return statusBase(), nil })
		if run.code != exitcode.Usage {
			t.Errorf("--format %s: exit %d, want %d (stdout %q)", format, run.code, exitcode.Usage, run.stdout)
		}
		if run.called {
			t.Errorf("--format %s: the daemon was asked before the format was refused", format)
		}
	}
	// The refusal of json names the way out; the text is not printed.
	run := runStatusCLI(t, []string{"status", "--format", "json"}, func() (any, error) { return statusBase(), nil })
	if !strings.Contains(run.stdout, "tg status prints text") || strings.Contains(run.stdout, "Account") {
		t.Errorf("json refusal: %q", run.stdout)
	}
	// The default format is json too, but it was not asked for: the text is printed.
	if ok := runStatusCLI(t, []string{"status"}, func() (any, error) { return statusBase(), nil }); ok.code != exitcode.OK {
		t.Errorf("the default format must not be refused: exit %d", ok.code)
	}
}

func TestStatusWithoutDaemonExitsThree(t *testing.T) {
	run := runStatusCLI(t, []string{"status"}, nil)
	if run.code != exitcode.DaemonNotRunning {
		t.Fatalf("exit %d, want %d", run.code, exitcode.DaemonNotRunning)
	}
	if run.stdout != "" {
		t.Errorf("stdout must stay empty: %q", run.stdout)
	}
	if !strings.Contains(run.stderr, "daemon is not running") || !strings.Contains(run.stderr, "start it with tg daemon start") {
		t.Errorf("stderr: %q", run.stderr)
	}
	named := runStatusCLI(t, []string{"status", "--profile", "work"}, nil)
	if named.code != exitcode.DaemonNotRunning || !strings.Contains(named.stderr, "start it with tg daemon start --profile work") {
		t.Errorf("a named profile: exit %d, stderr %q", named.code, named.stderr)
	}
}

func TestStatusAppearsInTheSchema(t *testing.T) {
	cmd, ok := schemaCommands(t)["status"]
	if !ok {
		t.Fatal("status is missing from the schema")
	}
	if cmd.Safety != "read" || cmd.Dangerous || cmd.Result != "rpc.Dashboard" || !slices.Equal(cmd.Formats, []string{"human"}) {
		t.Fatalf("schema of status: %+v", cmd)
	}
	have := map[string]bool{}
	for _, field := range cmd.Fields {
		have[field.Path] = true
	}
	for _, path := range []string{
		"profile", "pid", "started_at", "account.user.id", "account.authorized", "connection.connection", "connection.dc",
		"access.enabled", "database.size_bytes", "database.schema_version", "database.chats", "database.messages",
		"history.complete", "history.people.total", "history.folders.complete", "history.others.total",
		"background.state", "background.next_run_at", "semantic_search.state", "semantic_search.indexed", "semantic_search.total", "voice.enabled", "voice.installed", "voice.state", "voice.paused", "voice.queue.queued", "voice.done_today", "voice.bytes_today", "voice.last_failure", "jobs.sync", "jobs.full.state", "jobs.full.done",
		"throttle.flood_wait_seconds", "throttle.queued", "events.last_hour", "events.total",
	} {
		if !have[path] {
			t.Errorf("the schema of status lacks the field %s", path)
		}
	}
}

// The Semantic line for the states the golden files do not show: an indexer that
// never ran, a cancelled run, a missing model and a failure before the model is known.
func TestRenderDashboardSemanticStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   rpc.SemanticIndexStatus
		want string
	}{
		{"never ran", rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexIdle, Model: "bge-m3"}, "Semantic     on, bge-m3, 0 of 0 fragments indexed, no run yet\n"},
		{"cancelled", rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexIdle, Model: "bge-m3", Indexed: 10, Total: 40, FinishedAt: statusAt(19, 9, 5), Reason: "cancelled"}, "Semantic     on, bge-m3, 10 of 40 fragments indexed, last run 2026-11-19 09:05 (cancelled)\n"},
		{"model missing", rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexModelMissing, Reason: "Ollama does not have the model bge-m3"}, "Semantic     on, model missing (Ollama does not have the model bge-m3)\n"},
		{"error with control characters", rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexError, Reason: "open: \x1b[31mfailed"}, "Semantic     on, error (open:  [31mfailed)\n"},
		{"enabled flag off wins", rpc.SemanticIndexStatus{State: rpc.SemanticIndexIdle, Indexed: 5}, "Semantic     off (enable with tg settings set semantic_search.enabled true; restart required)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dash := statusBase()
			dash.SemanticSearch = tc.st
			if got := renderDashboard(dash, statusNow()); !strings.Contains(got, tc.want) {
				t.Errorf("want the line %q in:\n%s", tc.want, got)
			}
		})
	}
}

// The Voice line for the states the golden files do not show: the setting off,
// the engine missing, a queue that works, the three pauses, a failure, and words
// that carry control characters.
func TestRenderDashboardVoiceStates(t *testing.T) {
	at := statusAt(21, 0, 0)
	ready := rpc.VoiceStatus{Enabled: true, Installed: true, State: rpc.VoiceStateIdle}
	with := func(edit func(*rpc.VoiceStatus)) rpc.VoiceStatus {
		v := ready
		edit(&v)
		return v
	}
	for _, tc := range []struct {
		name string
		st   rpc.VoiceStatus
		want string
	}{
		{"off", rpc.VoiceStatus{State: rpc.VoiceStateDisabled}, "Voice        off (enable with [voice_transcription] enabled = true)\n"},
		{"off wins over a stale queue", rpc.VoiceStatus{Installed: true, Queue: rpc.VoiceQueueCounts{Queued: 4}}, "Voice        off (enable with [voice_transcription] enabled = true)\n"},
		{"engine missing", rpc.VoiceStatus{Enabled: true, Missing: []string{"ffmpeg", "model"}}, "Voice        on, engine not installed (missing ffmpeg, model)\n"},
		{"engine missing without a list", rpc.VoiceStatus{Enabled: true}, "Voice        on, engine not installed\n"},
		{"idle", ready, "Voice        on, 0 queued, 0 done today, 0 MB\n"},
		{"working", with(func(v *rpc.VoiceStatus) {
			v.State, v.Queue, v.DoneToday, v.BytesToday = rpc.VoiceStateWorking, rpc.VoiceQueueCounts{Queued: 3, Running: 1}, 12, 48<<20+1000
		}), "Voice        on, 3 queued, 1 running, 12 done today, 48 MB\n"},
		{"budget", with(func(v *rpc.VoiceStatus) {
			v.Paused, v.PausedUntil, v.Queue.Queued = rpc.VoicePausedBudget, at, 3
		}), "Voice        on, 3 queued, 0 done today, 0 MB, paused: budget until 2026-11-21 00:00\n"},
		{"offline", with(func(v *rpc.VoiceStatus) { v.Paused = rpc.VoicePausedOffline }), "Voice        on, 0 queued, 0 done today, 0 MB, paused: offline\n"},
		{"failure", with(func(v *rpc.VoiceStatus) { v.LastFailure = "too_large" }), "Voice        on, 0 queued, 0 done today, 0 MB, last failure: too_large\n"},
		{"control characters", with(func(v *rpc.VoiceStatus) { v.Paused, v.LastFailure = "x\x1b[31m", "y\x07" }), "Voice        on, 0 queued, 0 done today, 0 MB, paused: x [31m, last failure: y \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dash := statusBase()
			dash.Voice = tc.st
			if got := renderDashboard(dash, statusNow()); !strings.Contains(got, tc.want) {
				t.Errorf("want the line %q in:\n%s", tc.want, got)
			}
		})
	}
}
