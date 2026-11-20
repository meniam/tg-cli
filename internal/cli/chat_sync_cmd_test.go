package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type syncCLIRun struct {
	code           int
	stdout, stderr string
	method         string
	params         json.RawMessage
}

func runSyncCLI(t *testing.T, args []string, reply func(string, json.RawMessage) (any, error)) syncCLIRun {
	t.Helper()
	home := audit01Isolate(t)
	serverSide, clientSide := net.Pipe()
	server := rpc.NewServer(nil)
	calls := make(chan struct {
		method string
		params json.RawMessage
	}, 1)
	for _, method := range []string{rpc.MethodHistorySyncStart, rpc.MethodHistorySyncStatus, rpc.MethodHistorySyncCancel} {
		method := method
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
	code := Execute(context.Background(), args, Deps{
		Stdout: &stdout, Stderr: &stderr,
		ConfigPath: filepath.Join(home, "config.toml"),
		Getenv: func(key string) string {
			if key == "TG_CLI_HOME" {
				return home
			}
			return ""
		},
		DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientSide), nil },
	})
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC server did not stop")
	}
	server.Close()
	result := syncCLIRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
	select {
	case call := <-calls:
		result.method, result.params = call.method, call.params
	default:
	}
	return result
}

func syncCLIJob(state string) rpc.HistorySyncJob {
	oldest, newest := 7, 12
	return rpc.HistorySyncJob{ChatID: 42, State: state, Pages: 2, Loaded: 6, OldestID: &oldest, NewestID: &newest, StartedAt: "2026-11-14T12:00:00Z", UpdatedAt: "2026-11-14T12:01:00Z"}
}

func TestChatSyncCLIRequests(t *testing.T) {
	for _, tc := range []struct {
		name, method, chat string
		args               []string
	}{
		{"start", rpc.MethodHistorySyncStart, "42", []string{"chat", "sync", "start", "42"}},
		{"status one", rpc.MethodHistorySyncStatus, "42", []string{"chat", "sync", "status", "42"}},
		{"status all", rpc.MethodHistorySyncStatus, "", []string{"chat", "sync", "status"}},
		{"cancel negative id", rpc.MethodHistorySyncCancel, "-1001234567890", []string{"chat", "sync", "cancel", "--", "-1001234567890"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runSyncCLI(t, tc.args, func(_ string, _ json.RawMessage) (any, error) {
				if tc.chat == "" {
					return rpc.HistorySyncJobs{Jobs: []rpc.HistorySyncJob{syncCLIJob(rpc.HistorySyncRunning)}}, nil
				}
				return syncCLIJob(rpc.HistorySyncRunning), nil
			})
			if run.code != exitcode.OK || run.method != tc.method || !strings.Contains(run.stdout, `"ok":true`) {
				t.Fatalf("run=%+v", run)
			}
			var p rpc.HistorySyncParams
			if err := json.Unmarshal(run.params, &p); err != nil || p.Chat != tc.chat {
				t.Fatalf("params=%s err=%v", run.params, err)
			}
			if tc.name == "start" && !strings.Contains(run.stderr, `tg chat sync status -- "42"`) {
				t.Fatalf("missing status hint: %q", run.stderr)
			}
		})
	}
}

func TestChatSyncOutputFormatsFieldsAndQuiet(t *testing.T) {
	reply := func(method string, _ json.RawMessage) (any, error) {
		if method == rpc.MethodHistorySyncStatus {
			return rpc.HistorySyncJobs{Jobs: []rpc.HistorySyncJob{syncCLIJob(rpc.HistorySyncFailed)}}, nil
		}
		return syncCLIJob(rpc.HistorySyncQueued), nil
	}
	jsonl := runSyncCLI(t, []string{"chat", "sync", "status", "--format", "jsonl", "--fields", "chat_id,state"}, reply)
	if jsonl.code != exitcode.OK || jsonl.stdout != "{\"chat_id\":42,\"state\":\"failed\"}\n" {
		t.Fatalf("JSONL status: %+v", jsonl)
	}
	object := runSyncCLI(t, []string{"chat", "sync", "start", "42", "--fields", "chat_id,state", "--quiet"}, reply)
	if object.code != exitcode.OK || object.stdout != "{\"ok\":true,\"data\":{\"chat_id\":42,\"state\":\"queued\"}}\n" || object.stderr != "" {
		t.Fatalf("filtered quiet start: %+v", object)
	}
	statusOne := runSyncCLI(t, []string{"chat", "sync", "status", "42", "--fields", "chat_id,state"}, func(string, json.RawMessage) (any, error) {
		return syncCLIJob(rpc.HistorySyncIdle), nil
	})
	if statusOne.code != exitcode.OK || statusOne.stdout != "{\"ok\":true,\"data\":{\"chat_id\":42,\"state\":\"idle\"}}\n" {
		t.Fatalf("filtered status object: %+v", statusOne)
	}
	toon := runSyncCLI(t, []string{"chat", "sync", "cancel", "42", "--format", "toon"}, reply)
	wantToon := "ok: true\ndata:\n  chat_id: 42\n  state: queued\n  pages: 2\n  loaded: 6\n  oldest_id: 7\n  newest_id: 12\n  complete: false\n  started_at: \"2026-11-14T12:00:00Z\"\n  updated_at: \"2026-11-14T12:01:00Z\"\n"
	if toon.code != exitcode.OK || toon.stdout != wantToon {
		t.Fatalf("TOON cancel: %+v, want stdout %q", toon, wantToon)
	}
}

func TestChatSyncFailedStatusIsSuccessfulRead(t *testing.T) {
	job := syncCLIJob(rpc.HistorySyncFailed)
	job.ErrorCode = rpc.CodeTelegram
	job.Error = "history sync failed"
	run := runSyncCLI(t, []string{"chat", "sync", "status", "42", "--human"}, func(string, json.RawMessage) (any, error) {
		return job, nil
	})
	if run.code != exitcode.OK || !strings.Contains(run.stdout, "failed") || !strings.Contains(run.stdout, "history sync failed") || strings.Contains(run.stdout, "error (telegram_error)") {
		t.Fatalf("human failed status: %+v", run)
	}
}

func TestChatSyncRejectsObjectJSONLAndPropagatesRPCError(t *testing.T) {
	invalid := runSyncCLI(t, []string{"chat", "sync", "status", "42", "--format", "jsonl"}, func(string, json.RawMessage) (any, error) {
		return nil, rpc.NewError(rpc.CodeInternal, "unexpected RPC call")
	})
	if invalid.code != exitcode.Usage || invalid.method != "" {
		t.Fatalf("object JSONL: %+v", invalid)
	}
	closed := runSyncCLI(t, []string{"chat", "sync", "start", "42"}, func(string, json.RawMessage) (any, error) {
		return nil, rpc.ErrChatNotFound()
	})
	if closed.code != exitcode.NotFound || closed.method != rpc.MethodHistorySyncStart || !strings.Contains(closed.stdout, `"code":"not_found"`) {
		t.Fatalf("closed chat: %+v", closed)
	}
}

func TestChatSyncSchema(t *testing.T) {
	commands := schemaCommands(t)
	for _, tc := range []struct {
		path, safety, result string
		jsonl                bool
	}{
		{"chat sync start", "local_write", "rpc.HistorySyncJob", false},
		{"chat sync status", "read", "rpc.HistorySyncJobs", true},
		{"chat sync cancel", "local_write", "rpc.HistorySyncJob", false},
	} {
		cmd, ok := commands[tc.path]
		if !ok || cmd.Safety != tc.safety || cmd.Dangerous || cmd.Result != tc.result || slices.Contains(cmd.Formats, "jsonl") != tc.jsonl {
			t.Fatalf("schema %s: %+v", tc.path, cmd)
		}
		if len(cmd.Fields) == 0 || cmd.Args[0].Name != "chat" {
			t.Fatalf("fields/args %s: %+v", tc.path, cmd)
		}
	}
	status := commands["chat sync status"]
	if status.Item == nil || status.Item.Path != "jobs" || status.ChatResult != "rpc.HistorySyncJob" || len(status.ChatFields) == 0 {
		t.Fatalf("status shapes: %+v", status)
	}
	var paths []string
	for _, field := range status.ChatFields {
		paths = append(paths, field.Path)
	}
	for _, path := range []string{"chat_id", "state", "source", "pages", "loaded", "oldest_id", "newest_id", "complete", "wait_seconds", "error_code", "error", "started_at", "updated_at"} {
		if !slices.Contains(paths, path) {
			t.Errorf("chat result is missing %s", path)
		}
	}
	if !reflect.DeepEqual(status.Item.Fields, status.ChatFields) {
		t.Fatalf("list and chat job fields differ: list=%+v chat=%+v", status.Item.Fields, status.ChatFields)
	}
}
