package cli

import (
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func searchMediaEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.tg.user = &rpc.User{ID: 1, Username: "me"}
	e.tg.chats = []rpc.Chat{{ID: 55, Type: "user", Title: "Alice"}}
	e.tg.messages = []rpc.Message{
		{ID: 1, ChatID: 55, Date: "2026-01-01T10:00:00Z", Text: "годовой отчёт", MediaType: "voice"},
		{ID: 2, ChatID: 55, Date: "2026-01-02T10:00:00Z", Text: "годовой отчёт", MediaType: "photo"},
		{ID: 3, ChatID: 55, Date: "2026-01-03T10:00:00Z", Text: "годовой отчёт"},
	}
	e.startDaemon()
	if r := e.run("chat", "list"); r.code != exitcode.OK {
		t.Fatalf("chat list=%q", r.stdout)
	}
	if r := e.run("message", "history", "55"); r.code != exitcode.OK {
		t.Fatalf("history=%q", r.stdout)
	}
	return e
}

func TestSearchMediaFlag(t *testing.T) {
	e := searchMediaEnv(t)
	r := e.run("search", "отчёт", "--media", "photo", "--format", "jsonl")
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"media_type":"photo"`) || strings.Contains(r.stdout, `"media_type":"voice"`) || strings.Count(r.stdout, `"scope":"messages"`) != 1 {
		t.Fatalf("--media photo: exit %d, %q", r.code, r.stdout)
	}
	all := e.run("search", "отчёт", "--scope", "messages", "--format", "jsonl")
	if all.code != exitcode.OK || strings.Count(all.stdout, `"scope":"messages"`) != 3 {
		t.Fatalf("search without --media: exit %d, %q", all.code, all.stdout)
	}
	for _, args := range [][]string{
		{"search", "отчёт", "--media", "sticker"},
		{"search", "отчёт", "--media", "Photo"},
		{"search", "отчёт", "--media", "photo", "--scope", "chats"},
		{"search", "отчёт", "--media", "photo", "--scope", "messages,notes"},
		{"search", "отчёт", "--media", "photo", "--semantic"},
	} {
		if r := e.run(args...); r.code != exitcode.Usage {
			t.Errorf("%v: exit %d, want usage; %q", args, r.code, r.stdout)
		}
	}
}

func TestSearchMediaInSchema(t *testing.T) {
	search, ok := schemaCommands(t)["search"]
	if !ok {
		t.Fatal("search is not in the schema")
	}
	if missing := schema.Missing([]string{"hits.media_type"}, search.Fields); len(missing) != 0 {
		t.Errorf("schema lacks %v", missing)
	}
}
