package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

type exportRun struct {
	code           int
	stdout, stderr string
	params         []rpc.ExportParams
}

func runExportCLI(t *testing.T, args []string, reply func(rpc.ExportParams) (rpc.ExportPage, error), dialErr error) exportRun {
	t.Helper()
	home := audit02Home(t)
	serverSide, clientSide := net.Pipe()
	server := rpc.NewServer(nil)
	calls := make(chan rpc.ExportParams, 10)
	server.Handle(rpc.MethodMessageExportLocal, func(_ context.Context, raw json.RawMessage) (any, error) {
		var p rpc.ExportParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		calls <- p
		if reply == nil {
			return rpc.ExportPage{Messages: []rpc.ExportMessage{}}, nil
		}
		return reply(p)
	})
	done := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(done) }()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), args, Deps{
		Stdout: &stdout, Stderr: &stderr,
		ConfigPath: audit02ConfigPath(home), CallTimeout: audit02CallTimeout,
		Getenv: func(key string) string {
			if key == "TG_CLI_HOME" {
				return home
			}
			return ""
		},
		DialRPC: func(context.Context, string, string) (*rpc.Client, error) {
			if dialErr != nil {
				return nil, dialErr
			}
			return rpc.NewClient(clientSide), nil
		},
	})
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fake daemon did not stop")
	}
	server.Close()
	result := exportRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
	for {
		select {
		case p := <-calls:
			result.params = append(result.params, p)
		default:
			return result
		}
	}
}

func exportMessage(id int, deleted bool) rpc.ExportMessage {
	return rpc.ExportMessage{Message: rpc.Message{
		ID: id, ChatID: 42, Date: "2026-11-14T10:00:00Z", Text: "foreign text",
		Sender: &rpc.Sender{ID: 9, Name: "Ann", Username: "ann"},
	}, Deleted: deleted}
}

func exportLines(t *testing.T, text string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		lines = append(lines, item)
	}
	return lines
}

func TestMessageExportRejectsOptionsBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing local", []string{"message", "export", "42"}, "--local"},
		{"json", []string{"message", "export", "42", "--local", "--format", "json"}, "jsonl"},
		{"toon", []string{"message", "export", "42", "--local", "--format", "toon"}, "stream"},
		{"negative limit", []string{"message", "export", "42", "--local", "--limit=-1"}, "--limit must be at least 1"},
		{"negative after", []string{"message", "export", "42", "--local", "--after=-1"}, "--after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runExportCLI(t, tc.args, nil, os.ErrNotExist)
			if r.code != exitcode.Usage || len(r.params) != 0 || !strings.Contains(r.stdout, tc.want) {
				t.Fatalf("option validation: %+v", r)
			}
		})
	}
}

func TestMessageExportStreamsPagesAndForwardsFilters(t *testing.T) {
	reply := func(p rpc.ExportParams) (rpc.ExportPage, error) {
		switch p.After {
		case 0:
			return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(1, false), exportMessage(2, false)}, NextAfter: 2}, nil
		case 2:
			return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(3, false), exportMessage(4, true)}, NextAfter: 4}, nil
		case 4:
			return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(5, false)}}, nil
		}
		return rpc.ExportPage{}, rpc.NewError(rpc.CodeInternal, "unexpected cursor")
	}
	r := runExportCLI(t, []string{"message", "export", "42", "--local", "--since", "2026-11-14T00:00:00Z", "--until", "2026-11-15T00:00:00Z", "--include-deleted"}, reply, nil)
	if r.code != exitcode.OK || len(r.params) != 3 || !strings.Contains(r.stderr, "exported 5") || !strings.Contains(r.stderr, "tg chat sync status") {
		t.Fatalf("stream: %+v", r)
	}
	for i, p := range r.params {
		if p.Chat != "42" || p.After != []int{0, 2, 4}[i] || p.Since != "2026-11-14T00:00:00Z" || p.Until != "2026-11-15T00:00:00Z" || !p.IncludeDeleted {
			t.Fatalf("request %d: %+v", i, p)
		}
	}
	lines := exportLines(t, r.stdout)
	if len(lines) != 5 {
		t.Fatalf("lines=%d output=%q", len(lines), r.stdout)
	}
	for i, item := range lines {
		if item["id"] != float64(i+1) || item["chat_id"] != float64(42) || item["text"] != "foreign text" || item["ok"] != nil || item["Message"] != nil {
			t.Fatalf("line %d: %+v", i, item)
		}
		if _, present := item["deleted"]; present != (i == 3) {
			t.Fatalf("deleted field on line %d: %+v", i, item)
		}
	}
}

func TestMessageExportLimitStopsInsidePage(t *testing.T) {
	reply := func(p rpc.ExportParams) (rpc.ExportPage, error) {
		if p.After == 0 {
			return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(1, false), exportMessage(2, false), exportMessage(3, false)}, NextAfter: 3}, nil
		}
		return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(4, false), exportMessage(5, false)}, NextAfter: 5}, nil
	}
	for _, tc := range []struct {
		limit, calls int
	}{
		{2, 1}, {4, 2},
	} {
		r := runExportCLI(t, []string{"message", "export", "42", "--local", "--limit", strconv.Itoa(tc.limit)}, reply, nil)
		if r.code != exitcode.OK || len(r.params) != tc.calls || len(exportLines(t, r.stdout)) != tc.limit || !strings.Contains(r.stderr, "exported "+strconv.Itoa(tc.limit)) {
			t.Fatalf("limit %d: %+v", tc.limit, r)
		}
	}
}

func TestMessageExportEmptyQuietAndAfter(t *testing.T) {
	empty := runExportCLI(t, []string{"message", "export", "42", "--local", "--after", "7"}, nil, nil)
	if empty.code != exitcode.OK || empty.stdout != "" || len(empty.params) != 1 || empty.params[0].After != 7 || !strings.Contains(empty.stderr, "exported 0") {
		t.Fatalf("empty export: %+v", empty)
	}
	quiet := runExportCLI(t, []string{"message", "export", "42", "--local", "--quiet"}, nil, nil)
	if quiet.code != exitcode.OK || quiet.stdout != "" || quiet.stderr != "" {
		t.Fatalf("quiet export: %+v", quiet)
	}
}

func TestMessageExportErrorAfterPrintedLinesGivesResumeID(t *testing.T) {
	r := runExportCLI(t, []string{"message", "export", "42", "--local"}, func(p rpc.ExportParams) (rpc.ExportPage, error) {
		if p.After == 0 {
			return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(1, false), exportMessage(2, false)}, NextAfter: 2}, nil
		}
		return rpc.ExportPage{}, rpc.ErrChatNotFound()
	}, nil)
	if r.code != exitcode.NotFound || len(r.params) != 2 || !strings.Contains(r.stderr, "last exported id: 2") || !strings.Contains(r.stderr, "--after 2") {
		t.Fatalf("midstream failure: %+v", r)
	}
	lines := exportLines(t, r.stdout)
	if len(lines) != 3 || lines[0]["id"] != float64(1) || lines[1]["id"] != float64(2) || lines[2]["ok"] != false {
		t.Fatalf("partial output and error envelope: %+v", lines)
	}
}

func TestMessageExportHumanFramesTextAndMissingDaemon(t *testing.T) {
	message := exportMessage(7, false)
	message.Text = "foreign\ntext"
	r := runExportCLI(t, []string{"message", "export", "42", "--local", "--human"}, func(rpc.ExportParams) (rpc.ExportPage, error) {
		return rpc.ExportPage{Messages: []rpc.ExportMessage{message}}, nil
	}, nil)
	if r.code != exitcode.OK || !strings.Contains(r.stdout, "| foreign\n| text") || strings.Count(r.stdout, "note: lines starting with") != 1 {
		t.Fatalf("human output: %+v", r)
	}
	missing := runExportCLI(t, []string{"message", "export", "42", "--local"}, nil, os.ErrNotExist)
	if missing.code != exitcode.DaemonNotRunning || len(missing.params) != 0 {
		t.Fatalf("missing daemon: %+v", missing)
	}
	closed := runExportCLI(t, []string{"message", "export", "42", "--local"}, func(rpc.ExportParams) (rpc.ExportPage, error) {
		return rpc.ExportPage{}, rpc.ErrChatNotFound()
	}, nil)
	if closed.code != exitcode.NotFound || len(closed.params) != 1 || !strings.Contains(closed.stdout, `"code":"not_found"`) {
		t.Fatalf("closed chat: %+v", closed)
	}
}

func TestMessageExportFieldsAndSchema(t *testing.T) {
	reply := func(rpc.ExportParams) (rpc.ExportPage, error) {
		return rpc.ExportPage{Messages: []rpc.ExportMessage{exportMessage(7, false)}}, nil
	}
	short := runExportCLI(t, []string{"message", "export", "42", "--local", "--fields", "id,text"}, reply, nil)
	full := runExportCLI(t, []string{"message", "export", "42", "--local", "--fields", "messages.id,messages.text"}, reply, nil)
	if short.code != exitcode.OK || full.code != exitcode.OK || short.stdout != full.stdout || short.stdout != "{\"id\":7,\"text\":\"foreign text\"}\n" {
		t.Fatalf("fields: short=%+v full=%+v", short, full)
	}
	command := schemaCommands(t)["message export"]
	if command.Safety != "read" || command.Dangerous || command.Result != "rpc.ExportPage" || !reflect.DeepEqual(command.Formats, []string{"jsonl", "human"}) || command.Item == nil || command.Item.Path != "messages" || command.SubscriptionMethod != "" {
		t.Fatalf("schema: %+v", command)
	}
	marked := map[string]bool{}
	for _, field := range command.Item.Fields {
		if field.Untrusted {
			marked[field.Path] = true
		}
	}
	for _, path := range []string{"text", "sender.name", "sender.username"} {
		if !marked[path] {
			t.Errorf("untrusted field %s missing from schema", path)
		}
	}
	paths, err := schema.Paths([]byte(strings.TrimSpace(short.stdout)))
	if err != nil || len(schema.Missing(paths, command.Item.Fields)) != 0 || !slices.Contains(command.Formats, "jsonl") {
		t.Fatalf("schema item mismatch: paths=%v err=%v", paths, err)
	}
}
