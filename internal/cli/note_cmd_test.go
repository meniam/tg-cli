package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestNoteNegativeIDParsing(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"note", "add", "--chat", "-12345", "--", "body"},
		{"note", "add", "--chat=-12345", "--", "body"},
	} {
		r := e.run(args...)
		if r.code != exitcode.DaemonNotRunning {
			t.Errorf("args=%v: exit=%d stdout=%q", args, r.code, r.stdout)
		}
	}
}

func TestNoteAndSearchUsageWithoutDaemon(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"note", "add", "SECRET_BODY"},
		{"note", "add", "--chat", "77", "--person", "5", "SECRET_BODY"},
		{"note", "add", "--message", "1", "SECRET_BODY"},
		{"note", "add", "--chat", "77", "--message", "0", "SECRET_BODY"},
		{"note", "add", "--person", "5", "--message", "1", "SECRET_BODY"},
		{"note", "add", "--chat", "77", "--model", "model-x", "SECRET_BODY"},
		{"note", "add", "--chat", "77", "--author", "bot", "SECRET_BODY"},
		{"note", "add", "--chat", "77"},
		{"note", "delete"},
		{"note", "delete", "1", "--author", "ai"},
		{"note", "delete", "1", "--yes"},
		{"note", "list", "--message", "1"},
		{"note", "list", "--chat", "77", "--message", "0"},
		{"search", "   "},
		{"search", "SECRET_QUERY", "--scope", "unknown"},
		{"search", "SECRET_QUERY", "--author", "bot"},
	} {
		r := e.run(args...)
		if r.code != exitcode.Usage {
			t.Errorf("args=%v exit=%d stdout=%q", args, r.code, r.stdout)
		}
		if strings.Contains(r.stdout+r.stderr, "SECRET_BODY") || strings.Contains(r.stdout+r.stderr, "SECRET_QUERY") {
			t.Errorf("secret leaked for args=%v", args)
		}
	}
	for _, args := range [][]string{{"note", "add", "--chat", "77", "body"}, {"note", "list"}, {"search", "query"}, {"search", "query", "--scope", "chats,people", "--scope", "notes"}} {
		r := e.run(args...)
		if r.code != exitcode.DaemonNotRunning {
			t.Errorf("args=%v exit=%d stdout=%q", args, r.code, r.stdout)
		}
	}
}

func noteFromResult(t *testing.T, r result) rpc.Note {
	t.Helper()
	if r.code != exitcode.OK {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
	var note rpc.Note
	if err := json.Unmarshal(parse(t, r).Data, &note); err != nil {
		t.Fatal(err)
	}
	return note
}

func TestNoteCommandsEndToEnd(t *testing.T) {
	e := loggedInEnv(t)
	chat := noteFromResult(t, e.run("note", "add", "--chat", "77", "Chat note"))
	person := noteFromResult(t, e.run("note", "add", "--person", "5", "Person note"))
	message := noteFromResult(t, e.run("note", "add", "--chat", "77", "--message", "30", "Message note"))
	negative := noteFromResult(t, e.run("note", "add", "--chat", "-12345", "--", "Negative chat"))
	if chat.Kind != "chat" || chat.Author != "owner" || chat.ChatID == nil || *chat.ChatID != 77 || person.Kind != "person" || person.TargetID == nil || *person.TargetID != 5 || message.Kind != "message" || message.TargetID == nil || *message.TargetID != 30 || negative.ChatID == nil || *negative.ChatID != -12345 {
		t.Fatalf("derived notes: %+v %+v %+v %+v", chat, person, message, negative)
	}
	e.stdin = strings.NewReader("first line\nsecond line\n")
	fromStdin := noteFromResult(t, e.run("note", "add", "--chat", "77", "-"))
	if fromStdin.Body != "first line\nsecond line" {
		t.Errorf("stdin body=%q", fromStdin.Body)
	}
	tagged := noteFromResult(t, e.run("note", "add", "--chat", "77", "--tag", " ЁЛКА ", "--tag", "елка", "Tagged note"))
	if len(tagged.Tags) != 1 || tagged.Tags[0] != "елка" {
		t.Errorf("normalized tags=%v", tagged.Tags)
	}
	ai := noteFromResult(t, e.run("note", "add", "--chat", "77", "--author", "ai", "--model", "model-x", "AI note"))
	if ai.Author != "ai" || ai.Model != "model-x" {
		t.Errorf("AI note=%+v", ai)
	}
	e.tg.mu.Lock()
	e.tg.chats = []rpc.Chat{{ID: 77, Type: "supergroup", Title: "Team", Username: "teamchat"}}
	e.tg.mu.Unlock()
	if r := e.run("chat", "list"); r.code != exitcode.OK {
		t.Fatalf("chat list exit=%d stdout=%q", r.code, r.stdout)
	}
	byName := noteFromResult(t, e.run("note", "add", "--chat", "@teamchat", "Username note"))
	if byName.ChatID == nil || *byName.ChatID != 77 {
		t.Errorf("username note=%+v", byName)
	}
	e.tg.mu.Lock()
	calls := e.tg.resolveCalls
	e.tg.mu.Unlock()
	if calls != 0 {
		t.Errorf("known username called Telegram.Resolve %d times", calls)
	}
	cases := []struct {
		args []string
		want int64
	}{
		{[]string{"note", "list", "--person", "5"}, person.ID},
		{[]string{"note", "list", "--chat", "77", "--message", "30"}, message.ID},
		{[]string{"note", "list", "--author", "ai"}, ai.ID},
		{[]string{"note", "list", "--tag", "ЕЛКА"}, tagged.ID},
		{[]string{"note", "list", "--limit", "1"}, byName.ID},
		{[]string{"note", "list", "--before-id", strconv.FormatInt(byName.ID, 10), "--limit", "1"}, ai.ID},
	}
	for _, tc := range cases {
		r := e.run(tc.args...)
		if r.code != exitcode.OK {
			t.Fatalf("%v: %q", tc.args, r.stdout)
		}
		var list rpc.NoteListResult
		if err := json.Unmarshal(parse(t, r).Data, &list); err != nil || len(list.Notes) != 1 || list.Notes[0].ID != tc.want {
			t.Errorf("%v: list=%+v err=%v", tc.args, list, err)
		}
	}
	r := e.run("note", "list", "--chat", "77", "--format", "jsonl")
	if r.code != exitcode.OK || strings.Contains(r.stdout, "next_cursor") || strings.Contains(r.stdout, `"ok"`) {
		t.Errorf("jsonl=%q exit=%d", r.stdout, r.code)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 6 {
		t.Fatalf("chat note jsonl lines=%d output=%q", len(lines), r.stdout)
	}
	for _, line := range lines {
		var n rpc.Note
		if err := json.Unmarshal([]byte(line), &n); err != nil || n.ID == 0 {
			t.Errorf("invalid note line %q: %v", line, err)
		}
	}
	var deleted rpc.NoteDeleteResult
	r = e.run("note", "delete", strconv.FormatInt(message.ID, 10))
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &deleted) != nil || deleted.Deleted != 1 {
		t.Errorf("delete=%q", r.stdout)
	}
	wantFailure(t, e.run("note", "delete", strconv.FormatInt(message.ID, 10)), exitcode.NotFound)
	r = e.run("note", "delete", "--author", "ai")
	wantFailure(t, r, exitcode.Usage)
	if !strings.Contains(r.stdout, "ai") || !strings.Contains(r.stdout, "--yes") {
		t.Errorf("confirmation message=%q", r.stdout)
	}
	r = e.run("note", "delete", "--author", "ai", "--yes")
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &deleted) != nil || deleted.Deleted != 1 {
		t.Errorf("delete by author=%q", r.stdout)
	}
	r = e.run("note", "list", "--author", "owner")
	var owners rpc.NoteListResult
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &owners) != nil || len(owners.Notes) == 0 {
		t.Errorf("owner notes missing: %q", r.stdout)
	}
}

func TestSearchCommandsOfflineAndSecrets(t *testing.T) {
	e := newEnv(t)
	e.tg.user = &rpc.User{ID: 1, Username: "me"}
	e.tg.chats = []rpc.Chat{{ID: 77, Type: "supergroup", Title: "Ёлка Team", Username: "teamchat"}}
	e.tg.messages = []rpc.Message{{ID: 30, ChatID: 77, Date: "2026-01-03T10:00:00Z", Text: "Елка message"}}
	e.startDaemon()
	if r := e.run("chat", "list"); r.code != exitcode.OK {
		t.Fatalf("chat list=%q", r.stdout)
	}
	if r := e.run("message", "history", "@teamchat"); r.code != exitcode.OK {
		t.Fatalf("history=%q", r.stdout)
	}
	owner := noteFromResult(t, e.run("note", "add", "--chat", "77", "Ёлка owner"))
	ai := noteFromResult(t, e.run("note", "add", "--chat", "77", "--author", "ai", "Елка AI"))
	r := e.run("search", "ЕЛКА", "--scope", "messages,notes", "--scope", "chats")
	var found rpc.SearchResult
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &found) != nil || len(found.Hits) != 4 {
		t.Fatalf("search=%q", r.stdout)
	}
	if found.Hits[0].Scope != "chats" || found.Hits[1].Scope != "messages" || found.Hits[2].Scope != "notes" || found.Hits[3].Scope != "notes" {
		t.Errorf("scope order=%+v", found.Hits)
	}
	r = e.run("search", "ёл*", "--scope", "notes", "--prefix", "--author", "ai", "--chat", "77", "--deleted")
	found = rpc.SearchResult{}
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &found) != nil || len(found.Hits) != 1 || found.Hits[0].ID != ai.ID {
		t.Errorf("filtered search=%q owner=%d ai=%d", r.stdout, owner.ID, ai.ID)
	}
	r = e.run("search", "ЕЛКА", "--format", "jsonl")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != exitcode.OK || len(lines) != 4 || strings.Contains(r.stdout, `"ok"`) {
		t.Errorf("search jsonl=%q exit=%d", r.stdout, r.code)
	}
	for _, line := range lines {
		var h rpc.SearchHit
		if err := json.Unmarshal([]byte(line), &h); err != nil || h.Scope == "" {
			t.Errorf("invalid search line=%q err=%v", line, err)
		}
	}
	marker := "PRIVATE_BODY_MARKER"
	added := e.run("note", "add", "--chat", "77", marker)
	if added.code != exitcode.OK || strings.Contains(added.stderr, marker) {
		t.Errorf("note output leaked to stderr=%q", added.stderr)
	}
	bad := e.run("search", "PRIVATE_QUERY_MARKER", "--scope", "unknown")
	wantFailure(t, bad, exitcode.Usage)
	if strings.Contains(bad.stdout+bad.stderr, "PRIVATE_QUERY_MARKER") {
		t.Errorf("query leaked in error=%q stderr=%q", bad.stdout, bad.stderr)
	}
	bad = e.run("note", "add", "PRIVATE_BODY_MARKER")
	wantFailure(t, bad, exitcode.Usage)
	if strings.Contains(bad.stdout+bad.stderr, marker) {
		t.Errorf("body leaked in error=%q stderr=%q", bad.stdout, bad.stderr)
	}
}
