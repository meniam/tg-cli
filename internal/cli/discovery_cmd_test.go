package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type discoveryRun struct {
	code                   int
	stdout, stderr, method string
	params                 json.RawMessage
}

func runDiscoveryCLI(t *testing.T, args []string, reply func(string, json.RawMessage) (any, error), dialErr error) discoveryRun {
	t.Helper()
	home := audit02Home(t)
	serverSide, clientSide := net.Pipe()
	server := rpc.NewServer(nil)
	calls := make(chan struct {
		method string
		params json.RawMessage
	}, 100)
	for _, method := range []string{rpc.MethodMessageTranslate, rpc.MethodMessageTranscribe, rpc.MethodChatStats, rpc.MethodChatBoosts, rpc.MethodChatArchive, rpc.MethodChatUnarchive, rpc.MethodChatPinDialog, rpc.MethodChatUnpinDialog, rpc.MethodChatMute, rpc.MethodChatUnmute, rpc.MethodChatNotify, rpc.MethodFolderCreate, rpc.MethodFolderEdit, rpc.MethodFolderDelete, rpc.MethodFolderOrder, rpc.MethodFolderInviteInfo, rpc.MethodSavedList, rpc.MethodSavedHistory, rpc.MethodSavedSearch, rpc.MethodSavedTags, rpc.MethodSavedTagRename, rpc.MethodAccountTerminate, rpc.MethodFolderList, rpc.MethodChatState, rpc.MethodAccountSessions, rpc.MethodAccountBlocked, rpc.MethodAccountNotify, rpc.MethodMessageSearch, rpc.MethodMessageSearchPublic, rpc.MethodMessageContext, rpc.MethodMessageComments, rpc.MethodMessageLink, rpc.MethodMessageGet, rpc.MethodMessageDownload, rpc.MethodMessageExportMonth, rpc.MethodMessageExportVerify, rpc.MethodChatMarkRead, rpc.MethodChatMembers, rpc.MethodChatTopics, rpc.MethodChatCommon} {
		method := method
		server.Handle(method, func(_ context.Context, raw json.RawMessage) (any, error) {
			calls <- struct {
				method string
				params json.RawMessage
			}{method, raw}
			if reply != nil {
				return reply(method, raw)
			}
			return rpc.MessagesResult{Messages: []rpc.Message{}}, nil
		})
	}
	done := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(done) }()
	var stdout, stderr bytes.Buffer
	deps := Deps{Stdout: &stdout, Stderr: &stderr, ConfigPath: audit02ConfigPath(home), CallTimeout: audit02CallTimeout, Getenv: func(name string) string {
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
	server.Close()
	result := discoveryRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
	select {
	case call := <-calls:
		result.method, result.params = call.method, call.params
	default:
	}
	return result
}
func discoveryFixtureMessage() rpc.Message {
	return rpc.Message{ChatID: 42, ID: 7, Date: "2026-11-14T10:00:00Z", Text: "foreign\ntext", Sender: &rpc.Sender{ID: 8, Name: "Ann"}}
}

func TestDiscoveryCLICommands(t *testing.T) {
	message := discoveryFixtureMessage()
	cases := []struct {
		name   string
		args   []string
		method string
		result any
		check  func(*testing.T, json.RawMessage)
	}{
		{"chat search", []string{"message", "search", "42", "needle", "--media", "photo", "--limit", "2", "--cursor", "CUR"}, rpc.MethodMessageSearch, rpc.MessagesResult{Messages: []rpc.Message{message}, NextCursor: "NEXT"}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.SearchMessagesParams
			json.Unmarshal(raw, &p)
			if p.Chat != "42" || p.Query != "needle" || p.Media != "photo" || p.Limit != 2 || p.Cursor != "CUR" {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"public search", []string{"message", "search", "--public", "#tag", "--limit", "3"}, rpc.MethodMessageSearchPublic, rpc.PublicSearchResult{Hits: []rpc.PublicHit{{ChatID: 42, Chat: &rpc.Chat{ID: 42, Type: "user", Title: "Ann"}, Message: message}}}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.PublicSearchParams
			json.Unmarshal(raw, &p)
			if p.Query != "#tag" || p.Limit != 3 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"context", []string{"message", "context", "42", "7", "--before", "0", "--after", "2", "--local"}, rpc.MethodMessageContext, rpc.MessagesResult{Messages: []rpc.Message{message}}, func(t *testing.T, raw json.RawMessage) {
			var p map[string]any
			json.Unmarshal(raw, &p)
			if p["before"] != float64(0) || p["after"] != float64(2) || p["local"] != true {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"context link", []string{"message", "context", "t.me/c/12345/3/7"}, rpc.MethodMessageContext, rpc.MessagesResult{Messages: []rpc.Message{message}}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.ContextParams
			json.Unmarshal(raw, &p)
			if p.Chat != "-1000000012345" || p.ID != 7 || p.Before != 5 || p.After != 5 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"comments", []string{"message", "comments", "42", "7", "--limit", "5", "--cursor", "CUR"}, rpc.MethodMessageComments, rpc.MessagesResult{Messages: []rpc.Message{message}}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.CommentsParams
			json.Unmarshal(raw, &p)
			if p.Chat != "42" || p.ID != 7 || p.Limit != 5 || p.Cursor != "CUR" {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"comments link", []string{"message", "comments", "https://t.me/newsfeed/7"}, rpc.MethodMessageComments, rpc.MessagesResult{Messages: []rpc.Message{message}}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.CommentsParams
			json.Unmarshal(raw, &p)
			if p.Chat != "@newsfeed" || p.ID != 7 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"link", []string{"message", "link", "42", "7", "--topic", "3"}, rpc.MethodMessageLink, rpc.LinkResult{Link: "https://t.me/c/123/3/7"}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.LinkParams
			json.Unmarshal(raw, &p)
			if p.Chat != "42" || p.ID != 7 || p.Topic != 3 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"get link", []string{"message", "get", "https://t.me/c/12345/3/7"}, rpc.MethodMessageGet, message, func(t *testing.T, raw json.RawMessage) {
			var p rpc.MessageGetParams
			json.Unmarshal(raw, &p)
			if p.Chat != "-1000000012345" || p.ID != 7 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"get pair", []string{"message", "get", "42", "7"}, rpc.MethodMessageGet, message, func(t *testing.T, raw json.RawMessage) {
			var p rpc.MessageGetParams
			json.Unmarshal(raw, &p)
			if p.Chat != "42" || p.ID != 7 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"get negative chat", []string{"message", "get", "--", "-1000000012345", "7"}, rpc.MethodMessageGet, message, func(t *testing.T, raw json.RawMessage) {
			var p rpc.MessageGetParams
			json.Unmarshal(raw, &p)
			if p.Chat != "-1000000012345" || p.ID != 7 {
				t.Fatalf("params: %+v", p)
			}
		}},
		{"mark read", []string{"chat", "mark-read", "42", "--dry-run"}, rpc.MethodChatMarkRead, rpc.MarkReadResult{ChatID: 42, DryRun: true}, func(t *testing.T, raw json.RawMessage) {
			var p rpc.MarkReadParams
			json.Unmarshal(raw, &p)
			if p.Chat != "42" || !p.DryRun {
				t.Fatalf("params: %+v", p)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runDiscoveryCLI(t, tc.args, func(string, json.RawMessage) (any, error) { return tc.result, nil }, nil)
			if r.code != 0 || r.method != tc.method || !strings.Contains(r.stdout, `"ok":true`) {
				t.Fatalf("run: %+v", r)
			}
			tc.check(t, r.params)
		})
	}
}

func TestDiscoveryCLIValidationAndCodes(t *testing.T) {
	for _, args := range [][]string{
		{"message", "search", "42"}, {"message", "search", "--public", "42", "query"},
		{"message", "search", "42", "needle", "--media", "unknown"}, {"message", "search", "--public", "needle", "--media", "photo"},
		{"message", "search", "42", "needle", "--limit", "101"}, {"message", "context", "42", "7", "--before", "51"},
		{"message", "context", "42", "7", "--after", "-1"}, {"message", "comments", "42", "7", "--limit", "0"},
		{"message", "link", "42", "0"}, {"message", "link", "42", "7", "--topic", "-1"},
		{"message", "get", "https://t.me/telegram/nope"}, {"message", "context", "https://t.me/telegram/nope"},
	} {
		r := runDiscoveryCLI(t, args, nil, nil)
		if r.code != exitcode.Usage || r.method != "" {
			t.Errorf("invalid %v: %+v", args, r)
		}
	}
	invalid := runDiscoveryCLI(t, []string{"message", "get", "t.me/not-a-message"}, nil, nil)
	if invalid.code != exitcode.Usage || !strings.Contains(invalid.stdout, "https://t.me/channel/123") {
		t.Fatalf("link hint: %+v", invalid)
	}
	for _, tc := range []struct {
		err  error
		code int
	}{
		{rpc.NewError(rpc.CodeInvalidParams, "unsupported chat"), exitcode.Usage},
		{rpc.ErrChatNotFound(), exitcode.NotFound},
	} {
		r := runDiscoveryCLI(t, []string{"message", "link", "42", "7"}, func(string, json.RawMessage) (any, error) { return nil, tc.err }, nil)
		if r.code != tc.code {
			t.Fatalf("link code: %+v", r)
		}
	}
	denied := runDiscoveryCLI(t, []string{"chat", "mark-read", "42"}, func(string, json.RawMessage) (any, error) { return nil, rpc.ErrWriteDisabled() }, nil)
	if denied.code != exitcode.WriteDisabled || !strings.Contains(denied.stderr, "write list") {
		t.Fatalf("mark-read denied: %+v", denied)
	}
	absent := runDiscoveryCLI(t, []string{"message", "search", "42", "needle"}, nil, os.ErrNotExist)
	if absent.code != exitcode.DaemonNotRunning {
		t.Fatalf("no daemon: %+v", absent)
	}
}

func TestDiscoveryCLIFormatsFieldsAndSchema(t *testing.T) {
	message := discoveryFixtureMessage()
	fixture := func(method string, _ json.RawMessage) (any, error) {
		if method == rpc.MethodMessageSearchPublic {
			return rpc.PublicSearchResult{Hits: []rpc.PublicHit{{ChatID: 42, Chat: &rpc.Chat{ID: 42, Type: "user", Title: "Foreign title"}, Message: message}}, NextCursor: "NEXT"}, nil
		}
		return rpc.MessagesResult{Messages: []rpc.Message{message}, NextCursor: "NEXT"}, nil
	}
	jsonl := runDiscoveryCLI(t, []string{"message", "search", "42", "needle", "--format", "jsonl", "--fields", "id,text"}, fixture, nil)
	if jsonl.code != 0 || !strings.Contains(jsonl.stdout, `"id":7`) || strings.Contains(jsonl.stdout, `"chat_id"`) || !strings.Contains(jsonl.stdout, `"next_cursor":"NEXT"`) {
		t.Fatalf("jsonl: %+v", jsonl)
	}
	public := runDiscoveryCLI(t, []string{"message", "search", "--public", "needle", "--format", "jsonl", "--fields", "chat_id,message.text"}, fixture, nil)
	if public.code != 0 || !strings.Contains(public.stdout, `"chat_id":42`) || !strings.Contains(public.stdout, `"text":"foreign\ntext"`) {
		t.Fatalf("public fields: %+v", public)
	}
	human := runDiscoveryCLI(t, []string{"message", "context", "42", "7", "--human"}, fixture, nil)
	if human.code != 0 || !strings.Contains(human.stdout, "| foreign\n| text\n") || !strings.Contains(human.stdout, "note: lines starting") {
		t.Fatalf("human: %+v", human)
	}
	publicHuman := runDiscoveryCLI(t, []string{"message", "search", "--public", "needle", "--human"}, fixture, nil)
	if publicHuman.code != 0 || !strings.Contains(publicHuman.stdout, "| foreign\n| text\n") || !strings.Contains(publicHuman.stdout, "| Foreign title\n") {
		t.Fatalf("public human: %+v", publicHuman)
	}
	commands := schemaCommands(t)
	for _, name := range []string{"message search", "message context", "message comments", "message link", "chat mark-read"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("missing schema %s", name)
		}
	}
	search := commands["message search"]
	if search.Safety != "read" || search.Item == nil || search.Item.Path != "messages" || search.PublicItem == nil || search.PublicItem.Path != "hits" {
		t.Fatalf("search schema: %+v", search)
	}
	mark := commands["chat mark-read"]
	if mark.Safety != "account_write" || mark.Item != nil {
		t.Fatalf("mark-read schema: %+v", mark)
	}
	if !reflect.DeepEqual(search.Formats, []string{"json", "jsonl", "toon", "human"}) {
		t.Fatalf("search formats: %v", search.Formats)
	}
}
