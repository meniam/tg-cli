package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type writeCLIResult struct {
	code           int
	stdout, stderr string
	method         string
	params         json.RawMessage
}

func runWriteCLI(t *testing.T, args []string, stdin string, reply func(string, json.RawMessage) (any, error), dialErr error) writeCLIResult {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "tgw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	serverSide, clientSide := net.Pipe()
	srv := rpc.NewServer(nil)
	called := make(chan struct {
		method string
		params json.RawMessage
	}, 1)
	for _, method := range []string{rpc.MethodMessageSend, rpc.MethodMessageSendMedia, rpc.MethodMessageEdit, rpc.MethodMessageDelete, rpc.MethodMessageForward, rpc.MethodMessageReact, rpc.MethodMessagePin, rpc.MethodMessageUnpin, rpc.MethodMessagePoll, rpc.MethodChatTyping, rpc.MethodMessageScheduledList, rpc.MethodMessageScheduledDelete, rpc.MethodMessageScheduledSendNow, rpc.MethodChatDraftGet, rpc.MethodChatDraftList, rpc.MethodChatDraftSet, rpc.MethodChatDraftClear} {
		method := method
		srv.Handle(method, func(_ context.Context, raw json.RawMessage) (any, error) {
			called <- struct {
				method string
				params json.RawMessage
			}{method, raw}
			if reply != nil {
				return reply(method, raw)
			}
			if method == rpc.MethodMessageSendMedia {
				return rpc.SendMediaResult{ChatID: 42, Messages: []rpc.MessageRef{{ID: 11, Date: "2026-11-14T10:00:00Z"}}}, nil
			}
			return rpc.SendResult{ChatID: 42, ID: 11, Date: "2026-11-14T10:00:00Z"}, nil
		})
	}
	done := make(chan struct{})
	go func() { srv.ServeConn(serverSide); close(done) }()
	var out, errOut bytes.Buffer
	deps := Deps{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader(stdin), Getenv: func(name string) string {
		if name == "TG_CLI_HOME" {
			return home
		}
		return ""
	}, DialRPC: func(context.Context, string, string) (*rpc.Client, error) {
		if dialErr != nil {
			return nil, dialErr
		}
		return rpc.NewClient(clientSide), nil
	}}
	code := Execute(context.Background(), args, deps)
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	srv.Close()
	result := writeCLIResult{code: code, stdout: out.String(), stderr: errOut.String()}
	select {
	case call := <-called:
		result.method, result.params = call.method, call.params
	default:
	}
	return result
}

func TestMessageWriteCLIFlagsAndInput(t *testing.T) {
	send := runWriteCLI(t, []string{"message", "send", "42", "--reply-to", "7", "--topic", "8", "--silent", "--dry-run"}, "from stdin", func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.SendParams
		json.Unmarshal(raw, &p)
		return rpc.SendResult{ChatID: 42, DryRun: p.DryRun, Action: "send", TextLength: len(p.Text)}, nil
	}, nil)
	if send.code != 0 || send.method != rpc.MethodMessageSend || !strings.Contains(send.stdout, `"dry_run":true`) {
		t.Fatalf("send: %+v", send)
	}
	var p rpc.SendParams
	if err := json.Unmarshal(send.params, &p); err != nil || p.Text != "from stdin" || p.Chat != "42" || p.ReplyTo != 7 || p.Topic != 8 || !p.Silent || !p.DryRun {
		t.Fatalf("send params: %+v %v", p, err)
	}
	edit := runWriteCLI(t, []string{"message", "edit", "42", "11", "--yes", "--text", "replaced"}, "", nil, nil)
	var e rpc.EditParams
	json.Unmarshal(edit.params, &e)
	if edit.code != 0 || edit.method != rpc.MethodMessageEdit || e.ID != 11 || e.Text != "replaced" || e.DryRun {
		t.Fatalf("edit: %+v %+v", edit, e)
	}
	editDry := runWriteCLI(t, []string{"message", "edit", "42", "11", "--dry-run", "--text", "preview"}, "", func(string, json.RawMessage) (any, error) {
		return rpc.SendResult{ChatID: 42, DryRun: true, Action: "edit", TextLength: 7}, nil
	}, nil)
	if editDry.code != 0 || !strings.Contains(editDry.stdout, `"dry_run":true`) {
		t.Fatalf("edit dry run: %+v", editDry)
	}
	del := runWriteCLI(t, []string{"message", "delete", "42", "11", "12", "--for-me", "--yes"}, "", func(string, json.RawMessage) (any, error) {
		return rpc.DeleteResult{ChatID: 42, Deleted: []int{11, 12}}, nil
	}, nil)
	var d rpc.DeleteParams
	json.Unmarshal(del.params, &d)
	if del.code != 0 || del.method != rpc.MethodMessageDelete || d.Revoke || !d.ForMe || !reflect.DeepEqual(d.IDs, []int{11, 12}) {
		t.Fatalf("delete: %+v %+v", del, d)
	}
	delDry := runWriteCLI(t, []string{"message", "delete", "42", "11", "--revoke", "--dry-run"}, "", func(string, json.RawMessage) (any, error) {
		return rpc.DeleteResult{ChatID: 42, DryRun: true, Action: "delete", Count: 1}, nil
	}, nil)
	if delDry.code != 0 || !strings.Contains(delDry.stdout, `"dry_run":true`) {
		t.Fatalf("delete dry run: %+v", delDry)
	}
	forward := runWriteCLI(t, []string{"message", "forward", "42", "77", "11", "--silent", "--dry-run"}, "", func(string, json.RawMessage) (any, error) {
		return rpc.ForwardResult{ChatID: 77, DryRun: true, Action: "forward", Count: 1}, nil
	}, nil)
	var f rpc.ForwardParams
	json.Unmarshal(forward.params, &f)
	if forward.code != 0 || forward.method != rpc.MethodMessageForward || f.From != "42" || f.To != "77" || len(f.IDs) != 1 || !f.Silent || !f.DryRun {
		t.Fatalf("forward: %+v %+v", forward, f)
	}
	negative := runWriteCLI(t, []string{"message", "send", "--text", "hi", "--", "-1001234567890"}, "", nil, nil)
	var n rpc.SendParams
	json.Unmarshal(negative.params, &n)
	if negative.code != 0 || n.Chat != "-1001234567890" {
		t.Fatalf("negative chat: %+v %+v", negative, n)
	}
}

func TestMessageWriteCLIValidationBeforeDaemon(t *testing.T) {
	cases := [][]string{
		{"message", "send", "42"},
		{"message", "send", "42", "--text", "   "},
		{"message", "send", "42", "--text", strings.Repeat("x", 4097)},
		{"message", "send", "42", "--text", "hi", "--reply-to", "-1"},
		{"message", "edit", "42", "11", "--text", "hi"},
		{"message", "edit", "42", "0", "--yes", "--text", "hi"},
		{"message", "delete", "42", "11", "--revoke"},
		{"message", "delete", "42", "11", "--yes"},
		{"message", "delete", "42", "11", "--revoke", "--for-me", "--yes"},
		{"message", "forward", "42", "77", "0"},
	}
	for i, args := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			r := runWriteCLI(t, args, "", nil, nil)
			if r.code != exitcode.Usage || r.method != "" {
				t.Fatalf("validation: %+v", r)
			}
		})
	}
}

func TestMessageWriteCLIExitCodesAndFormats(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"not authorized", rpc.NewError(rpc.CodeNotAuthorized, "login"), 4},
		{"flood", rpc.NewError(rpc.CodeFloodWait, "FLOOD_WAIT"), 6},
		{"not found", rpc.ErrChatNotFound(), 7},
		{"write disabled", rpc.ErrWriteDisabled(), 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, []string{"message", "send", "42", "--text", "hello"}, "", func(string, json.RawMessage) (any, error) { return nil, tc.err }, nil)
			if r.code != tc.code {
				t.Fatalf("code: %+v", r)
			}
			if tc.code == 9 && !strings.Contains(r.stderr, "write list") {
				t.Fatalf("write hint: %+v", r)
			}
		})
	}
	absent := runWriteCLI(t, []string{"message", "send", "42", "--text", "hello"}, "", nil, os.ErrNotExist)
	if absent.code != 3 {
		t.Fatalf("missing daemon: %+v", absent)
	}
	quiet := runWriteCLI(t, []string{"message", "send", "42", "--text", "hello", "--quiet"}, "", func(string, json.RawMessage) (any, error) { return nil, rpc.ErrWriteDisabled() }, nil)
	if quiet.code != exitcode.WriteDisabled || strings.Contains(quiet.stderr, "write list") || !strings.Contains(quiet.stdout, `"write_disabled"`) {
		t.Fatalf("quiet write error: %+v", quiet)
	}
	human := runWriteCLI(t, []string{"message", "send", "42", "--text", "hello", "--format", "human"}, "", nil, nil)
	if human.code != 0 || !strings.Contains(human.stdout, "chat_id") || strings.Contains(human.stdout, `"ok"`) {
		t.Fatalf("human: %+v", human)
	}
	jsonRun := runWriteCLI(t, []string{"message", "send", "42", "--text", "hello", "--format", "json"}, "", nil, nil)
	if jsonRun.code != 0 || !strings.Contains(jsonRun.stdout, `"ok":true`) {
		t.Fatalf("json: %+v", jsonRun)
	}
}

func TestMessageWriteSchema(t *testing.T) {
	commands := schemaCommands(t)
	for _, name := range []string{"message send", "message edit", "message delete", "message forward"} {
		command, ok := commands[name]
		if !ok || command.Safety != "account_write" || command.Result == "" {
			t.Fatalf("schema %s: %+v", name, command)
		}
		if (name == "message edit" || name == "message delete") && !command.Dangerous {
			t.Errorf("not dangerous: %s", name)
		}
	}
}

type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("stdin must not be read when --text is given")
	return 0, io.EOF
}

func TestWriteTextIgnoresStdinWhenTextIsGiven(t *testing.T) {
	cmd := &cobra.Command{Use: "send"}
	cmd.Flags().String("text", "", "")
	if err := cmd.Flags().Set("text", "hello"); err != nil {
		t.Fatal(err)
	}
	a := &app{deps: Deps{Stdin: failingReader{t}}}
	got, err := a.writeText(cmd)
	if err != nil || got != "hello" {
		t.Fatalf("writeText = %q, %v", got, err)
	}
}
