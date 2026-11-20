package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// searchLinkEnv saves one message in each kind of chat. The word forms differ
// from the query, so every message is found through the stemmed leg.
func searchLinkEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.tg.user = &rpc.User{ID: 1, Username: "me"}
	e.tg.chats = []rpc.Chat{
		{ID: -1001000000001, Type: "supergroup", Title: "Public group", Username: "pubgroup"},
		{ID: -1001000000002, Type: "channel", Title: "Private channel"},
		{ID: 55, Type: "user", Title: "Alice"},
		{ID: -4242, Type: "group", Title: "Basic group"},
	}
	e.tg.messages = []rpc.Message{
		{ID: 1, ChatID: -1001000000001, Date: "2026-01-01T10:00:00Z", Text: "Доставка задерживается"},
		{ID: 2, ChatID: -1001000000002, Date: "2026-01-02T10:00:00Z", Text: "Доставка задерживается"},
		{ID: 3, ChatID: 55, Date: "2026-01-03T10:00:00Z", Text: "Доставка задерживается"},
		{ID: 4, ChatID: -4242, Date: "2026-01-04T10:00:00Z", Text: "Доставка задерживается"},
		{ID: 5, ChatID: -1001000000001, TopicID: 9, Date: "2026-01-05T10:00:00Z", Text: "Доставка задерживается"},
	}
	e.startDaemon()
	if r := e.run("chat", "list"); r.code != exitcode.OK {
		t.Fatalf("chat list=%q", r.stdout)
	}
	if r := e.run("message", "history", "@pubgroup"); r.code != exitcode.OK {
		t.Fatalf("history=%q", r.stdout)
	}
	return e
}

func TestSearchLinkOfEveryChatKind(t *testing.T) {
	e := searchLinkEnv(t)
	r := e.run("search", "доставки", "--scope", "messages", "--format", "jsonl")
	if r.code != exitcode.OK {
		t.Fatalf("search: exit %d, %q", r.code, r.stdout)
	}
	type want struct {
		link        string // empty means a null link
		unavailable string
	}
	wants := map[int64]want{
		1: {link: "https://t.me/pubgroup/1"},
		2: {link: "https://t.me/c/1000000002/2"},
		3: {unavailable: "private_chat"},
		4: {unavailable: "basic_group"},
		5: {link: "https://t.me/pubgroup/9/5"},
	}
	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		link, present := fields["link"]
		if !present {
			t.Errorf("message hit has no link field (null expected): %s", line)
			continue
		}
		id := int64(fields["id"].(float64))
		w, ok := wants[id]
		if !ok {
			t.Errorf("unexpected hit %s", line)
			continue
		}
		seen++
		if w.link == "" {
			if link != nil {
				t.Errorf("message %d: link = %v, want null", id, link)
			}
		} else if link != w.link {
			t.Errorf("message %d: link = %v, want %s", id, link, w.link)
		}
		if got, _ := fields["link_unavailable"].(string); got != w.unavailable {
			t.Errorf("message %d: link_unavailable = %q, want %q", id, got, w.unavailable)
		}
	}
	if seen != len(wants) {
		t.Errorf("%d of %d messages found: %q", seen, len(wants), r.stdout)
	}
}

func TestSearchLinkIsMessageOnly(t *testing.T) {
	e := searchLinkEnv(t)
	r := e.run("search", "Public", "--scope", "chats", "--format", "jsonl")
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"scope":"chats"`) {
		t.Fatalf("search chats: exit %d, %q", r.code, r.stdout)
	}
	if strings.Contains(r.stdout, `"link"`) {
		t.Errorf("a chat hit carries a link field: %q", r.stdout)
	}
}

func TestSearchHumanAndSchemaShowLinks(t *testing.T) {
	e := searchLinkEnv(t)
	human := e.run("search", "доставки", "--scope", "messages", "--human")
	if human.code != exitcode.OK || !strings.Contains(human.stdout, "link") || !strings.Contains(human.stdout, "https://t.me/pubgroup/1") || !strings.Contains(human.stdout, "private_chat") {
		t.Errorf("--human lacks the link fields: exit %d, %q", human.code, human.stdout)
	}
	search, ok := schemaCommands(t)["search"]
	if !ok {
		t.Fatal("search is not in the schema")
	}
	if missing := schema.Missing([]string{"hits.link", "hits.link_unavailable"}, search.Fields); len(missing) != 0 {
		t.Errorf("schema lacks %v", missing)
	}
}
