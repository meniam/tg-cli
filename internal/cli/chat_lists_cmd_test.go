package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func chatListsFixture(method string, _ json.RawMessage) (any, error) {
	switch method {
	case rpc.MethodChatMembers:
		return rpc.MembersResult{Members: []rpc.Member{{ID: 5, Name: "Foreign\nname", Username: "foreign", Role: "admin"}}, NextOffset: 12}, nil
	case rpc.MethodChatTopics:
		return rpc.TopicsResult{Topics: []rpc.Topic{{ID: 7, Title: "Foreign topic", Created: "2026-11-14T10:00:00Z"}}, NextCursor: "TOPIC-CURSOR"}, nil
	case rpc.MethodChatCommon:
		return rpc.CommonChatsResult{Chats: []rpc.CommonChat{{ID: -77, Type: "group", Title: "Foreign chat", Username: "foreign_chat"}}, NextCursor: "COMMON-CURSOR"}, nil
	}
	return nil, nil
}

func TestChatListsCLIRequests(t *testing.T) {
	audit01Isolate(t)
	for _, tc := range []struct {
		name   string
		args   []string
		method string
		check  func(*testing.T, json.RawMessage)
	}{
		{"members negative id", []string{"chat", "members", "--search", "Foreign", "--filter", "admins", "--offset", "10", "--limit", "2", "--", "-1001234567890"}, rpc.MethodChatMembers, func(t *testing.T, raw json.RawMessage) {
			var p rpc.MembersParams
			if err := json.Unmarshal(raw, &p); err != nil || p.Chat != "-1001234567890" || p.Search != "Foreign" || p.Filter != "admins" || p.Offset != 10 || p.Limit != 2 {
				t.Fatalf("params %+v %v", p, err)
			}
		}},
		{"topics", []string{"chat", "topics", "@forumchat", "--limit", "3", "--cursor", "OLD"}, rpc.MethodChatTopics, func(t *testing.T, raw json.RawMessage) {
			var p rpc.TopicsParams
			if err := json.Unmarshal(raw, &p); err != nil || p.Chat != "@forumchat" || p.Limit != 3 || p.Cursor != "OLD" {
				t.Fatalf("params %+v %v", p, err)
			}
		}},
		{"topics negative id", []string{"chat", "topics", "--limit", "3", "--", "-1001234567890"}, rpc.MethodChatTopics, func(t *testing.T, raw json.RawMessage) {
			var p rpc.TopicsParams
			if err := json.Unmarshal(raw, &p); err != nil || p.Chat != "-1001234567890" || p.Limit != 3 {
				t.Fatalf("params %+v %v", p, err)
			}
		}},
		{"common", []string{"chat", "common", "@aliceuser", "--limit", "4", "--cursor", "OLD"}, rpc.MethodChatCommon, func(t *testing.T, raw json.RawMessage) {
			var p rpc.CommonChatsParams
			if err := json.Unmarshal(raw, &p); err != nil || p.User != "@aliceuser" || p.Limit != 4 || p.Cursor != "OLD" {
				t.Fatalf("params %+v %v", p, err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runDiscoveryCLI(t, tc.args, chatListsFixture, nil)
			if run.code != exitcode.OK || run.method != tc.method || !strings.Contains(run.stdout, `"ok":true`) {
				t.Fatalf("run %+v", run)
			}
			tc.check(t, run.params)
			if !strings.Contains(run.stderr, "continue with --") {
				t.Fatalf("missing continuation hint: %q", run.stderr)
			}
		})
	}
}

func TestChatListsCLIFormatsFieldsAndHints(t *testing.T) {
	audit01Isolate(t)
	members := runDiscoveryCLI(t, []string{"chat", "members", "@teamchat", "--format", "jsonl", "--fields", "id,name"}, chatListsFixture, nil)
	if members.code != exitcode.OK || members.stdout != "{\"id\":5,\"name\":\"Foreign\\nname\"}\n{\"next_offset\":12}\n" || !strings.Contains(members.stderr, "--offset 12") {
		t.Fatalf("members JSONL %+v", members)
	}
	topics := runDiscoveryCLI(t, []string{"chat", "topics", "@forumchat", "--format", "jsonl", "--fields", "id,title"}, chatListsFixture, nil)
	if topics.code != exitcode.OK || topics.stdout != "{\"id\":7,\"title\":\"Foreign topic\"}\n{\"next_cursor\":\"TOPIC-CURSOR\"}\n" || !strings.Contains(topics.stderr, "--cursor") {
		t.Fatalf("topics JSONL %+v", topics)
	}
	common := runDiscoveryCLI(t, []string{"chat", "common", "@aliceuser", "--format", "jsonl", "--fields", "id,title"}, chatListsFixture, nil)
	if common.code != exitcode.OK || common.stdout != "{\"id\":-77,\"title\":\"Foreign chat\"}\n{\"next_cursor\":\"COMMON-CURSOR\"}\n" {
		t.Fatalf("common JSONL %+v", common)
	}
	selected := runDiscoveryCLI(t, []string{"chat", "members", "@teamchat", "--fields", "id,next_offset"}, chatListsFixture, nil)
	if selected.code != exitcode.OK || selected.stdout != "{\"ok\":true,\"data\":{\"members\":[{\"id\":5}],\"next_offset\":12}}\n" {
		t.Fatalf("selected JSON %+v", selected)
	}
	for _, name := range []string{"members", "topics", "common"} {
		run := runDiscoveryCLI(t, []string{"chat", name, "@teamchat", "--human", "--quiet"}, chatListsFixture, nil)
		if run.code != exitcode.OK || run.stderr != "" || !strings.Contains(run.stdout, "note: lines starting") || !strings.Contains(run.stdout, "| Foreign") {
			t.Fatalf("%s human %+v", name, run)
		}
		toon := runDiscoveryCLI(t, []string{"chat", name, "@teamchat", "--format", "toon", "--fields", "id"}, chatListsFixture, nil)
		wantToon := map[string]string{
			"members": "ok: true\ndata:\n  members[1]{id}:\n    5\n",
			"topics":  "ok: true\ndata:\n  topics[1]{id}:\n    7\n",
			"common":  "ok: true\ndata:\n  chats[1]{id}:\n    -77\n",
		}[name]
		if toon.code != exitcode.OK || toon.stdout != wantToon {
			t.Fatalf("%s toon %+v, want stdout %q", name, toon, wantToon)
		}
	}
	empty := runDiscoveryCLI(t, []string{"chat", "common", "@aliceuser"}, func(string, json.RawMessage) (any, error) {
		return rpc.CommonChatsResult{Chats: []rpc.CommonChat{}, NextCursor: "NEXT"}, nil
	}, nil)
	if empty.code != exitcode.OK || !strings.Contains(empty.stderr, "closed chats are hidden") || !strings.Contains(empty.stderr, "--cursor") {
		t.Fatalf("empty common %+v", empty)
	}
	quiet := runDiscoveryCLI(t, []string{"chat", "members", "@teamchat", "--quiet"}, chatListsFixture, nil)
	if quiet.code != exitcode.OK || quiet.stderr != "" {
		t.Fatalf("quiet %+v", quiet)
	}
}

func TestChatListsCLIErrorCodes(t *testing.T) {
	audit01Isolate(t)
	for _, tc := range []struct {
		command, method string
		code, exit      int
	}{
		{"members", rpc.MethodChatMembers, rpc.CodeTelegram, exitcode.Telegram},
		{"topics", rpc.MethodChatTopics, rpc.CodeInvalidParams, exitcode.Usage},
		{"common", rpc.MethodChatCommon, rpc.CodeNotFound, exitcode.NotFound},
	} {
		run := runDiscoveryCLI(t, []string{"chat", tc.command, "@teamchat"}, func(method string, _ json.RawMessage) (any, error) {
			if method != tc.method {
				t.Errorf("method %q", method)
			}
			return nil, rpc.NewError(tc.code, "test rejection")
		}, nil)
		if run.code != tc.exit || !strings.Contains(run.stdout, `"ok":false`) {
			t.Fatalf("%s: %+v", tc.command, run)
		}
	}
}

func TestChatListsCLIRejectsInvalidFlagsBeforeRPC(t *testing.T) {
	audit01Isolate(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"chat", "members", "@teamchat", "--limit", "0"}, "--limit must be at least 1"},
		{[]string{"chat", "members", "@teamchat", "--limit", "201"}, "--limit must be between 1 and 200"},
		{[]string{"chat", "members", "@teamchat", "--offset", "-1"}, "--offset must not be negative"},
		{[]string{"chat", "members", "@teamchat", "--filter", "unknown"}, "--filter must be recent, admins, or bots"},
		{[]string{"chat", "topics", "@forumchat", "--limit", "0"}, "--limit must be at least 1"},
		{[]string{"chat", "topics", "@forumchat", "--limit", "101"}, "--limit must be between 1 and 100"},
		{[]string{"chat", "common", "@aliceuser", "--limit", "0"}, "--limit must be at least 1"},
		{[]string{"chat", "common", "@aliceuser", "--limit", "101"}, "--limit must be between 1 and 100"},
		{[]string{"chat", "members", "-1001234567890"}, "put -- before negative chat ids"},
	} {
		run := runDiscoveryCLI(t, tc.args, chatListsFixture, nil)
		if run.method != "" {
			t.Fatalf("args %v reached the daemon: %+v", tc.args, run)
		}
		audit01WantUsage(t, run.code, run.stdout, tc.want)
	}
}

func TestChatListsSchemaAndResponseFields(t *testing.T) {
	audit01Isolate(t)
	commands := schemaCommands(t)
	for _, tc := range []struct {
		name, item string
		result     any
	}{
		{"chat members", "members", rpc.MembersResult{Members: []rpc.Member{{ID: 5, Name: "Ann", Role: "admin"}}, NextOffset: 6}},
		{"chat topics", "topics", rpc.TopicsResult{Topics: []rpc.Topic{{ID: 7, Title: "Topic", Created: "2026-11-14T10:00:00Z"}}, NextCursor: "NEXT"}},
		{"chat common", "chats", rpc.CommonChatsResult{Chats: []rpc.CommonChat{{ID: -77, Type: "group", Title: "Shared"}}, NextCursor: "NEXT"}},
	} {
		cmd, ok := commands[tc.name]
		if !ok || cmd.Safety != "read" || cmd.Dangerous || cmd.Item == nil || cmd.Item.Path != tc.item || !reflect.DeepEqual(cmd.Formats, []string{"json", "jsonl", "toon", "human"}) {
			t.Fatalf("schema %s: %+v", tc.name, cmd)
		}
		data, err := json.Marshal(tc.result)
		if err != nil {
			t.Fatal(err)
		}
		paths, err := schema.Paths(data)
		if err != nil {
			t.Fatal(err)
		}
		if missing := schema.Missing(paths, cmd.Fields); len(missing) != 0 {
			t.Errorf("%s: response fields missing from schema: %v", tc.name, missing)
		}
	}
}
