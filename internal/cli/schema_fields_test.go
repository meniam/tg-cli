package cli

import (
	"encoding/json"
	"os"
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
	"auth cancel":      {"auth", "cancel"},
	"auth status":      {"auth", "status"},
	"chat info":        {"chat", "info", "@teamchat"},
	"chat invite-info": {"chat", "invite-info", "https://t.me/+AbCdEfGh1234"},
	"chat list":        {"chat", "list"},
	"chat members":     {"chat", "members", "@teamchat"},
	"chat topics":      {"chat", "topics", "@teamchat"},
	"chat common":      {"chat", "common", "@aliceuser"},
	"chat resolve":     {"chat", "resolve", "@teamchat"},
	"daemon status":    {"daemon", "status"},
	"daemon logs":      {"daemon", "logs"},
	"message get":      {"message", "get", "@teamchat", "30"},
	"message history":  {"message", "history", "@teamchat"},
	"message pinned":   {"message", "pinned", "@teamchat"},
	"note add":         {"note", "add", "--chat", "77", "--author", "ai", "--tag", "t", "one more"},
	"note delete":      {"note", "delete", noteIDToken},
	"note list":        {"note", "list"},
	"schema":           {"schema"},
	"search":           {"search", "third"},
	"version":          {"version"},
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

	out := map[string][]byte{}
	for path, args := range realResponseCases {
		run := make([]string, len(args))
		for i, arg := range args {
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
	"daemon stop":        "runs last, after the cases that need the daemon it stops, so it is checked separately",
	"auth login":         "asks for a phone number, a code and a password on stdin",
	"auth resend":        "needs a login that waits for the code, started by another command",
	"daemon start":       "runs until the daemon stops, the other cases need the daemon it starts",
	"message watch":      "requires a live event stream; its net.Pipe contract is checked separately",
	"message export":     "local export is a JSONL stream covered by net.Pipe CLI tests",
	"message send":       "account write is covered through net.Pipe tests without a real account",
	"message send-media": "account write is covered through net.Pipe tests without a real account",
	"message edit":       "account write is covered through net.Pipe tests without a real account",
	"message delete":     "account write is covered through net.Pipe tests without a real account",
	"message forward":    "account write is covered through net.Pipe tests without a real account",
	"message react":      "account write is covered through net.Pipe tests without a real account",
	"message pin":        "account write is covered through net.Pipe tests without a real account",
	"message unpin":      "account write is covered through net.Pipe tests without a real account",
	"message poll":       "account write is covered through net.Pipe tests without a real account",
	"message search":     "Telegram discovery is covered by net.Pipe CLI tests",
	"message context":    "Telegram and local context routing is covered by net.Pipe CLI tests",
	"message comments":   "channel comments routing is covered by net.Pipe CLI tests",
	"message link":       "message link routing is covered by net.Pipe CLI tests",
	"chat typing":        "account write is covered by net.Pipe CLI tests",
	"chat mark-read":     "account read-state write is covered by net.Pipe CLI tests",
	"chat sync start":    "background history command is covered by net.Pipe CLI tests",
	"chat sync status":   "job status is covered by net.Pipe CLI tests",
	"chat sync cancel":   "background cancellation is covered by net.Pipe CLI tests",
}

// TestEveryCommandResponseIsCompared makes sure the comparison covers the whole
// command tree: a command that is neither exercised nor explained fails here.
func TestEveryCommandResponseIsCompared(t *testing.T) {
	cases := map[string]bool{}
	for path := range realResponseCases {
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
