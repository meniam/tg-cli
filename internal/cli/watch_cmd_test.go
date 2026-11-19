package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type watchRun struct {
	code           int
	stdout, stderr string
	params         rpc.SubscribeParams
}

func runWatchScript(t *testing.T, ctx context.Context, args []string, events []rpc.Event, rpcErr *rpc.Error, closeAfter bool, stdout io.Writer) watchRun {
	t.Helper()
	return audit03RunWatch(t, ctx, args, events, rpcErr, closeAfter, stdout, nil)
}

// audit03RunWatch is runWatchScript with an optional writer for stderr.
func audit03RunWatch(t *testing.T, ctx context.Context, args []string, events []rpc.Event, rpcErr *rpc.Error, closeAfter bool, stdout, stderr io.Writer) watchRun {
	t.Helper()
	home := audit03ShortDir(t)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	params := make(chan rpc.SubscribeParams, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverSide.Close()
		line, err := bufio.NewReader(serverSide).ReadBytes('\n')
		if err != nil {
			return
		}
		var req rpc.Request
		if json.Unmarshal(line, &req) != nil {
			return
		}
		var p rpc.SubscribeParams
		if json.Unmarshal(req.Params, &p) != nil {
			return
		}
		params <- p
		response := rpc.Response{JSONRPC: rpc.Version, ID: req.ID}
		if rpcErr != nil {
			response.Error = rpcErr
		} else {
			response.Result = json.RawMessage(`{"subscription_id":"1","cursor":"5.0.0.0"}`)
		}
		raw, _ := json.Marshal(response)
		if _, err := serverSide.Write(append(raw, '\n')); err != nil {
			return
		}
		for _, event := range events {
			body, _ := json.Marshal(event)
			notice, _ := json.Marshal(rpc.Request{JSONRPC: rpc.Version, Method: rpc.MethodEvent, Params: body})
			if _, err := serverSide.Write(append(notice, '\n')); err != nil {
				return
			}
		}
		if !closeAfter {
			var one [1]byte
			serverSide.Read(one[:])
		}
	}()
	var out, errOut bytes.Buffer
	if stdout == nil {
		stdout = &out
	}
	if stderr == nil {
		stderr = &errOut
	}
	deps := Deps{Stdout: stdout, Stderr: stderr, ConfigPath: audit03ConfigPath(t), Getenv: func(name string) string {
		if name == "TG_CLI_HOME" {
			return home
		}
		return ""
	}, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientSide), nil }}
	code := Execute(ctx, args, deps)
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fake daemon did not stop")
	}
	var p rpc.SubscribeParams
	select {
	case p = <-params:
	default:
	}
	return watchRun{code: code, stdout: out.String(), stderr: errOut.String(), params: p}
}

func watchMessageEvent(t *testing.T) rpc.Event {
	t.Helper()
	body, _ := json.Marshal(rpc.Message{ID: 7, ChatID: 42, Date: "2026-11-14T00:00:00Z", Text: "foreign\ntext"})
	chat := int64(42)
	return rpc.Event{ID: "6.1.2.3", Type: rpc.EventMessageNew, ChatID: &chat, Time: "2026-11-14T00:00:00Z", Data: body}
}

func TestWatchFormatsFlagsAndFields(t *testing.T) {
	event := watchMessageEvent(t)
	result := runWatchScript(t, context.Background(), []string{"message", "watch", "--max-events", "1", "--chat", "42", "--chat", "@team", "--topic", "3", "--types", "message.new,reaction", "--after", "5.0.0.0"}, []rpc.Event{event}, nil, false, nil)
	if result.code != 0 || !strings.HasPrefix(result.stdout, `{"id":"6.1.2.3","type":"message.new"`) || !strings.Contains(result.stderr, "watching from cursor 5.0.0.0") {
		t.Fatalf("jsonl: %+v", result)
	}
	if !reflect.DeepEqual(result.params.Chats, []string{"42", "@team"}) || result.params.Topic != 3 || result.params.After != "5.0.0.0" || !reflect.DeepEqual(result.params.Types, []string{rpc.EventMessageNew, rpc.EventReaction}) {
		t.Fatalf("params: %+v", result.params)
	}
	result = runWatchScript(t, context.Background(), []string{"message", "watch", "--format", "json", "--max-events", "1"}, []rpc.Event{event}, nil, false, nil)
	if result.code != 0 || !strings.Contains(result.stdout, `"ok":true,"data":{"events":[`) {
		t.Fatalf("json: %+v", result)
	}
	second := event
	second.ID = "7.1.2.3"
	result = runWatchScript(t, context.Background(), []string{"message", "watch", "--max-events", "2"}, []rpc.Event{event, second}, nil, false, nil)
	if result.code != 0 || len(strings.Split(strings.TrimSpace(result.stdout), "\n")) != 2 {
		t.Fatalf("two events: %+v", result)
	}
	result = runWatchScript(t, context.Background(), []string{"message", "watch", "--max-events", "1", "--fields", "type,data.text"}, []rpc.Event{event}, nil, false, nil)
	if result.code != 0 || strings.Contains(result.stdout, `"id"`) || !strings.Contains(result.stdout, `"text":"foreign\ntext"`) {
		t.Fatalf("fields: %+v", result)
	}
	result = runWatchScript(t, context.Background(), []string{"message", "watch", "--format", "json", "--max-events", "1", "--fields", "type,data.text"}, []rpc.Event{event}, nil, false, nil)
	if result.code != 0 || strings.Contains(result.stdout, `"chat_id"`) || !strings.Contains(result.stdout, `"events":[{"type":"message.new"`) {
		t.Fatalf("json fields: %+v", result)
	}
	result = runWatchScript(t, context.Background(), []string{"message", "watch", "--human", "--quiet", "--max-events", "1"}, []rpc.Event{event}, nil, false, nil)
	if result.code != 0 || strings.Contains(result.stderr, "watching") || !strings.Contains(result.stdout, "| foreign\n| text\n") || !strings.Contains(result.stdout, "note: lines starting") {
		t.Fatalf("human: %+v", result)
	}
	if lines := strings.Split(strings.TrimSpace(result.stdout), "\n"); len(lines) != 4 || !strings.Contains(lines[1], "message.new") {
		t.Fatalf("human event layout: %q", result.stdout)
	}
}

func TestWatchExitCodes(t *testing.T) {
	event := watchMessageEvent(t)
	for _, tc := range []struct {
		name   string
		args   []string
		events []rpc.Event
		rpcErr *rpc.Error
		closed bool
		code   int
		hint   string
	}{
		{"lagged", []string{"message", "watch"}, []rpc.Event{{ID: "5.0.0.0", Type: rpc.EventLagged, Time: event.Time}}, nil, false, exitcode.Failure, "resume with --after 5.0.0.0"},
		{"closed", []string{"message", "watch"}, nil, nil, true, exitcode.DaemonNotRunning, "watching from cursor"},
		{"expired", []string{"message", "watch", "--after", "1.0.0.0"}, nil, rpc.NewError(rpc.CodeCursorExpired, "event cursor expired"), true, exitcode.CursorExpired, "without --after"},
		{"chat not found", []string{"message", "watch", "--chat", "42"}, nil, rpc.ErrChatNotFound(), true, exitcode.NotFound, ""},
		{"no login", []string{"message", "watch"}, nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in"), true, exitcode.NotAuthorized, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWatchScript(t, context.Background(), tc.args, tc.events, tc.rpcErr, tc.closed, nil)
			if r.code != tc.code || (tc.hint != "" && !strings.Contains(r.stderr, tc.hint)) {
				t.Fatalf("result: %+v", r)
			}
		})
	}
}

func TestWatchRejectsInvalidOptionsBeforeDial(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"message", "watch", "--format", "json"}, "--format json requires --max-events"},
		{[]string{"message", "watch", "--format", "toon"}, "--format toon is not supported for a stream"},
		{[]string{"message", "watch", "--types", "unknown"}, "unknown event type"},
		{[]string{"message", "watch", "--max-events", "0"}, "--max-events must be positive"},
		{[]string{"message", "watch", "--topic", "-1"}, "--topic must not be negative"},
		{[]string{"message", "watch", "--after", "broken"}, "invalid event cursor"},
	} {
		var out, stderr bytes.Buffer
		code := Execute(context.Background(), tc.args, Deps{Stdout: &out, Stderr: &stderr, ConfigPath: audit03ConfigPath(t), Getenv: audit03HomeEnv(audit03ShortDir(t)), DialRPC: func(context.Context, string, string) (*rpc.Client, error) {
			t.Error("dialed on invalid option")
			return nil, os.ErrNotExist
		}})
		if code != exitcode.Usage || !strings.Contains(out.String()+stderr.String(), tc.want) {
			t.Errorf("%v: code=%d want message %q, output=%q stderr=%q", tc.args, code, tc.want, out.String(), stderr.String())
		}
	}
}

func TestWatchContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trigger := &cancelWriter{after: cancel}
	r := runWatchScript(t, ctx, []string{"message", "watch"}, []rpc.Event{watchMessageEvent(t)}, nil, false, trigger)
	if r.code != 0 {
		t.Fatalf("cancel: %+v", r)
	}
}

func TestWatchSchemaAndMissingDaemon(t *testing.T) {
	command := schemaCommands(t)["message watch"]
	if command.Safety != "read" || command.SubscriptionMethod != rpc.MethodSubscribe || !reflect.DeepEqual(command.Formats, []string{"jsonl", "json", "human"}) || command.Item == nil || command.Item.Path != "events" {
		t.Fatalf("watch schema: %+v", command)
	}
	marked := false
	for _, field := range command.Item.Fields {
		if field.Path == "data" && field.Untrusted {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("event data not marked: %+v", command.Item.Fields)
	}
	var out, stderr bytes.Buffer
	home := audit03ShortDir(t)
	code := Execute(context.Background(), []string{"message", "watch"}, Deps{Stdout: &out, Stderr: &stderr, ConfigPath: audit03ConfigPath(t), Getenv: func(string) string { return home }, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return nil, os.ErrNotExist }})
	if code != exitcode.DaemonNotRunning {
		t.Fatalf("missing daemon: code=%d output=%q", code, out.String())
	}
}

type cancelWriter struct{ after func() }

func (w *cancelWriter) Write(p []byte) (int, error) { w.after(); return len(p), nil }
