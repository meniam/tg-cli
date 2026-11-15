package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// schemaCommands runs `schema` once and returns its commands by path.
func schemaCommands(t *testing.T) map[string]commandDoc {
	t.Helper()
	e := newEnv(t)
	r := e.run("schema")
	if r.code != exitcode.OK {
		t.Fatalf("schema: exit %d, %q", r.code, r.stdout)
	}
	var doc schemaDoc
	if err := json.Unmarshal(parse(t, r).Data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]commandDoc{}
	for _, cmd := range doc.Commands {
		out[cmd.Path] = cmd
	}
	return out
}

// noteIDToken in a case below stands for the id of a note the test creates, so
// that the deletion cases have something of their own to delete.
const noteIDToken = "@note"

// realResponseCases are the commands that a response fixture covers, with the
// arguments of the run. The token noteIDToken is replaced at run time.
var realResponseCases = map[string][]string{
	"auth code":              {"auth", "code"},
	"auth password":          {"auth", "password", "--password-stdin"},
	"user profile":           {"user", "profile", "@aliceuser"},
	"user block":             {"user", "block", "@aliceuser"},
	"user unblock":           {"user", "unblock", "@aliceuser"},
	"contact list":           {"contact", "list"},
	"contact add":            {"contact", "add", "@aliceuser"},
	"contact delete":         {"contact", "delete", "@aliceuser", "--yes"},
	"contact search":         {"contact", "search", "ann"},
	"auth cancel":            {"auth", "cancel"},
	"auth status":            {"auth", "status"},
	"folder list":            {"folder", "list"},
	"chat state":             {"chat", "state"},
	"account sessions":       {"account", "sessions"},
	"account privacy get":    {"account", "privacy", "get", "last-seen"},
	"account privacy set":    {"account", "privacy", "set", "last-seen", "--default", "nobody", "--yes"},
	"account set":            {"account", "set", "--birthday", "none"},
	"account profile":        {"account", "profile"},
	"account emoji-statuses": {"account", "emoji-statuses"},
	"account blocked":        {"account", "blocked"},
	"account notify":         {"account", "notify"},
	"chat requests":          {"chat", "requests", "@teamchat"},
	"chat set":               {"chat", "set", "@teamchat", "--forum", "on"},
	"chat approve":           {"chat", "approve", "@teamchat", "@aliceuser"},
	"chat decline":           {"chat", "decline", "@teamchat", "@aliceuser"},
	"chat info":              {"chat", "info", "@teamchat"},
	"chat stats":             {"chat", "stats", "@teamchat"},
	"chat boosts":            {"chat", "boosts", "@teamchat"},
	"chat invite-info":       {"chat", "invite-info", "https://t.me/+AbCdEfGh1234"},
	"chat list":              {"chat", "list"},
	"chat members":           {"chat", "members", "@teamchat"},
	"chat topics":            {"chat", "topics", "@teamchat"},
	"chat common":            {"chat", "common", "@aliceuser"},
	"chat resolve":           {"chat", "resolve", "@teamchat"},
	"daemon status":          {"daemon", "status"},
	"daemon logs":            {"daemon", "logs"},
	"message get":            {"message", "get", "@teamchat", "30"},
	"message readers":        {"message", "readers", "@teamchat", "30"},
	"message transcribe":     {"message", "transcribe", "@teamchat", "40", "--wait", "0s"},
	"message history":        {"message", "history", "@teamchat"},
	"message pinned":         {"message", "pinned", "@teamchat"},
	"note add":               {"note", "add", "--chat", "77", "--author", "ai", "--tag", "t", "one more"},
	"note delete":            {"note", "delete", noteIDToken},
	"note list":              {"note", "list"},
	"schema":                 {"schema"},
	"search":                 {"search", "third"},
	"version":                {"version"},
	"sticker search":         {"sticker", "search", "cats"},
	"sticker installed":      {"sticker", "installed"},
	"sticker set":            {"sticker", "set", "fixture"},
	"message send-sticker":   {"message", "send-sticker", "5", "--set", "fixture", "--index", "0", "--dry-run"},
	"message edit-media":     {"message", "edit-media", "5", "30", "--as", "document", "--file", "@media", "--dry-run"},
	"message inline-query":   {"message", "inline-query", "5", "--bot", "@fixturebot", "--query", "test"},
	"message send-inline":    {"message", "send-inline", "5", "--query-id", "123", "--result", "result", "--dry-run"},
	"message buttons":        {"message", "buttons", "@teamchat", "30"},
	"message press":          {"message", "press", "5", "30", "--button", "0", "--dry-run"},
	"chat auto-delete":       {"chat", "auto-delete", "5", "--period", "1d", "--dry-run"},
	"saved list":             {"saved", "list"},
	"saved history":          {"saved", "history", "77"},
	"saved search":           {"saved", "search", "test"},
	"saved tags":             {"saved", "tags"},
	"saved tag-rename":       {"saved", "tag-rename", "👍", "--title", "tag", "--dry-run"},
	"folder create":          {"folder", "create", "test", "--include", "77", "--dry-run"},
	"folder edit":            {"folder", "edit", "2", "--title", "test", "--dry-run"},
	"folder delete":          {"folder", "delete", "2", "--dry-run"},
	"folder order":           {"folder", "order", "2", "--dry-run"},
	"folder invite-info":     {"folder", "invite-info", "t.me/addlist/AbCdEfGh"},
	"chat mute":              {"chat", "mute", "77", "--dry-run"},
	"chat unmute":            {"chat", "unmute", "77", "--dry-run"},
	"chat notify":            {"chat", "notify", "77", "--silent=true", "--dry-run"},
	"chat archive":           {"chat", "archive", "77", "--dry-run"},
	"chat unarchive":         {"chat", "unarchive", "77", "--dry-run"},
	"chat pin":               {"chat", "pin", "77", "--dry-run"},
	"chat unpin":             {"chat", "unpin", "77", "--dry-run"},
	"account terminate":      {"account", "terminate", "cd2662154e6d76b2", "--dry-run"},
}

// realResponses runs the commands of realResponseCases against the fake daemon
// and keeps the JSON of data.
func realResponses(t *testing.T) map[string][]byte {
	t.Helper()
	e := loggedInEnv(t)
	paths, err := daemon.ResolvePaths(e.home, daemon.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	e.tg.mu.Lock()
	e.tg.peopleFixture = true
	e.tg.messages = append(e.tg.messages, rpc.Message{ID: 40, ChatID: 77, MediaType: "voice"})
	for i := range e.tg.messages {
		if e.tg.messages[i].ID == 30 {
			e.tg.messages[i].MediaType = "document"
		}
	}
	e.tg.mu.Unlock()
	mediaPath := filepath.Join(e.home, "fixture.dat")
	if err := os.WriteFile(mediaPath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	accessData, _ := json.Marshal(map[string]any{"write": []int{1, 5, 42}, "admin": []int{77}, "profile": true, "dialogs": true, "sessions": true, "files": []string{e.home}, "downloads": []string{e.home}})
	if err := os.WriteFile(paths.Access, accessData, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := e.run("message", "inline-query", "5", "--bot", "@fixturebot", "--query", "test"); r.code != exitcode.OK {
		t.Fatalf("inline query setup: %q", r.stdout)
	}
	if err := os.WriteFile(paths.Log, []byte("test line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := e.run("note", "add", "--chat", "77", "--author", "ai", "--model", "model-x", "--tag", "t", "A note"); r.code != exitcode.OK {
		t.Fatalf("note add: %q", r.stdout)
	}
	if r := e.run("search", "third"); r.code != exitcode.OK {
		t.Fatalf("search: %q", r.stdout)
	}
	// Every deletion case deletes one note of its own, so the cases do not
	// depend on the order they run in and none of them empties the list.
	own := func() string {
		r := e.run("note", "add", "--chat", "77", "--tag", "doomed", "to delete")
		if r.code != exitcode.OK {
			t.Fatalf("note add: %q", r.stdout)
		}
		var note rpc.Note
		if err := json.Unmarshal(parse(t, r).Data, &note); err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(note.ID, 10)
	}

	out := authStepResponses(t)
	for path, args := range realResponseCases {
		if path == "auth code" || path == "auth password" {
			continue
		}
		run := make([]string, len(args))
		for i, arg := range args {
			if arg == "@media" {
				arg = mediaPath
			}
			if arg == noteIDToken {
				arg = own()
			}
			run[i] = arg
		}
		r := e.run(run...)
		if r.code != exitcode.OK {
			t.Errorf("%s: exit %d, %q", path, r.code, r.stdout)
			continue
		}
		out[path] = parse(t, r).Data
	}
	// The daemon stop is last: it ends the daemon the other cases need.
	if r := e.run("daemon", "stop"); r.code != exitcode.OK {
		t.Errorf("daemon stop: exit %d, %q", r.code, r.stdout)
	} else {
		out["daemon stop"] = parse(t, r).Data
	}
	return out
}

// authStepResponses exercises both steps on one separate login flow. The other
// response cases require an already authorized account.
func authStepResponses(t *testing.T) map[string][]byte {
	t.Helper()
	e := newEnv(t)
	e.tg.need2FA = true
	e.startDaemon()
	start := e.run("auth", "login", "--phone", "+15550001111", "--no-input")
	if start.code != exitcode.OK {
		t.Fatalf("start login: exit %d, %q", start.code, start.stdout)
	}
	out := map[string][]byte{}
	for _, step := range []struct{ path, input string }{{"auth code", "CODE777\n"}, {"auth password", "PASS888\n"}} {
		e.stdin = strings.NewReader(step.input)
		r := e.run(realResponseCases[step.path]...)
		if r.code != exitcode.OK {
			t.Fatalf("%s: exit %d, %q", step.path, r.code, r.stdout)
		}
		out[step.path] = parse(t, r).Data
	}
	return out
}

// TestSchemaFieldsMatchRealResponses checks the schema against responses the
// commands really print: a path of a response must be a path of the schema.
func TestSchemaFieldsMatchRealResponses(t *testing.T) {
	commands := schemaCommands(t)
	checked := 0
	for path, data := range realResponses(t) {
		cmd, ok := commands[path]
		if !ok {
			t.Errorf("%s: not in the schema", path)
			continue
		}
		paths, err := schema.Paths(data)
		if err != nil {
			t.Errorf("%s: data is not JSON: %v", path, err)
			continue
		}
		if len(paths) == 0 {
			t.Errorf("%s: the response has no fields", path)
		}
		for _, missing := range schema.Missing(paths, cmd.Fields) {
			t.Errorf("%s: the response has %q, the schema does not", path, missing)
		}
		checked++
	}
	if want := len(realResponseCases) + 1; checked != want { // + daemon stop
		t.Fatalf("%d commands compared, want %d", checked, want)
	}
}

// uncoveredCommands are the commands no response fixture covers, and why. Every
// other command of the schema is compared with a real response above, so a new
// command has to be either exercised or listed here with a reason.
var uncoveredCommands = map[string]string{
	"chat unread":                "access and persistence are covered by daemon tests",
	"chat changes":               "local history is covered by daemon and store tests",
	"message unread":             "access is covered by daemon tests",
	"chat join":                  "account write is covered by net.Pipe CLI tests",
	"chat leave":                 "account write is covered by net.Pipe CLI tests",
	"chat create":                "account write is covered by net.Pipe CLI tests",
	"chat edit":                  "account write is covered by net.Pipe CLI tests",
	"chat kick":                  "account write is covered by net.Pipe CLI tests",
	"chat ban":                   "account write is covered by net.Pipe CLI tests",
	"chat unban":                 "account write is covered by net.Pipe CLI tests",
	"chat restrict":              "account write is covered by net.Pipe CLI tests",
	"chat permissions":           "account write is covered by net.Pipe CLI tests",
	"chat slow-mode":             "account write is covered by net.Pipe CLI tests",
	"daemon stop":                "runs last, after the cases that need the daemon it stops, so it is checked separately",
	"session export":             "exports a secret to a separate owner-only file",
	"session import":             "imports a secret into a separate stopped profile",
	"auth login":                 "asks for a phone number, a code and a password on stdin",
	"auth resend":                "needs a login that waits for the code, started by another command",
	"daemon start":               "runs until the daemon stops, the other cases need the daemon it starts",
	"message watch":              "requires a live event stream; its net.Pipe contract is checked separately",
	"message export":             "local export is a JSONL stream covered by net.Pipe CLI tests",
	"message send":               "account write is covered through net.Pipe tests without a real account",
	"message send-media":         "account write is covered through net.Pipe tests without a real account",
	"message edit":               "account write is covered through net.Pipe tests without a real account",
	"message delete":             "account write is covered through net.Pipe tests without a real account",
	"message forward":            "account write is covered through net.Pipe tests without a real account",
	"message react":              "account write is covered through net.Pipe tests without a real account",
	"message pin":                "account write is covered through net.Pipe tests without a real account",
	"message unpin":              "account write is covered through net.Pipe tests without a real account",
	"message poll":               "account write is covered through net.Pipe tests without a real account",
	"message search":             "Telegram discovery is covered by net.Pipe CLI tests",
	"message context":            "Telegram and local context routing is covered by net.Pipe CLI tests",
	"message comments":           "channel comments routing is covered by net.Pipe CLI tests",
	"message link":               "message link routing is covered by net.Pipe CLI tests",
	"message scheduled list":     "scheduled commands are covered by net.Pipe CLI tests",
	"message scheduled delete":   "scheduled commands are covered by net.Pipe CLI tests",
	"message scheduled send-now": "scheduled commands are covered by net.Pipe CLI tests",
	"chat draft get":             "draft commands are covered by net.Pipe CLI tests",
	"chat draft list":            "draft commands are covered by net.Pipe CLI tests",
	"chat draft set":             "draft commands are covered by net.Pipe CLI tests",
	"chat draft clear":           "draft commands are covered by net.Pipe CLI tests",
	"chat typing":                "account write is covered by net.Pipe CLI tests",
	"chat mark-read":             "account read-state write is covered by net.Pipe CLI tests",
	"chat sync start":            "background history command is covered by net.Pipe CLI tests",
	"chat sync status":           "job status is covered by net.Pipe CLI tests",
	"chat sync cancel":           "background cancellation is covered by net.Pipe CLI tests",
}

// fixtureResponseCases cover commands whose daemon side effects are isolated by net.Pipe.
var fixtureResponseCases = map[string]bool{"message download": true, "message export-month": true, "message export-verify": true}

// TestEveryCommandResponseIsCompared makes sure the comparison covers the whole
// command tree: a command that is neither exercised nor explained fails here.
func TestEveryCommandResponseIsCompared(t *testing.T) {
	t.Run("message download", TestDownloadResponseFixture)
	t.Run("message export-month and verify", TestExportMonthCLI)
	cases := map[string]bool{}
	for path := range realResponseCases {
		cases[path] = true
	}
	for path := range fixtureResponseCases {
		cases[path] = true
	}
	for path := range uncoveredCommands {
		cases[path] = true
	}
	for path := range schemaCommands(t) {
		if !cases[path] {
			t.Errorf("%s: no response fixture and no reason in uncoveredCommands", path)
		}
	}
	for path, why := range uncoveredCommands {
		if why == "" {
			t.Errorf("%s: uncovered without a reason", path)
		}
	}
}

// TestSchemaListItemsMatchJSONL does the same for JSONL, where the schema
// describes one line: the wrapper list is out of the way and the fields of an
// item are the fields of a line.
func TestSchemaListItemsMatchJSONL(t *testing.T) {
	e := loggedInEnv(t)
	if r := e.run("note", "add", "--chat", "77", "line"); r.code != exitcode.OK {
		t.Fatalf("note add: %q", r.stdout)
	}
	commands := schemaCommands(t)
	for path, args := range map[string][]string{
		"chat list":       {"chat", "list", "--format", "jsonl"},
		"chat members":    {"chat", "members", "@teamchat", "--format", "jsonl"},
		"chat topics":     {"chat", "topics", "@teamchat", "--format", "jsonl"},
		"chat common":     {"chat", "common", "@aliceuser", "--format", "jsonl"},
		"message history": {"message", "history", "@teamchat", "--format", "jsonl"},
		"note list":       {"note", "list", "--format", "jsonl"},
		"search":          {"search", "line", "--format", "jsonl"},
	} {
		cmd := commands[path]
		if cmd.Item == nil {
			t.Errorf("%s: no item schema", path)
			continue
		}
		r := e.run(args...)
		if r.code != exitcode.OK {
			t.Fatalf("%s: exit %d, %q", path, r.code, r.stdout)
		}
		items := 0
		for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
			if strings.Contains(line, "next_cursor") || strings.Contains(line, "next_offset") {
				continue // the last line of a page is not an item
			}
			paths, err := schema.Paths([]byte(line))
			if err != nil {
				t.Errorf("%s: a JSONL line is not JSON: %v", path, err)
				continue
			}
			for _, missing := range schema.Missing(paths, cmd.Item.Fields) {
				t.Errorf("%s: a JSONL line has %q, the item schema does not", path, missing)
			}
			items++
		}
		if items == 0 {
			t.Errorf("%s: no JSONL item lines", path)
		}
	}
}
