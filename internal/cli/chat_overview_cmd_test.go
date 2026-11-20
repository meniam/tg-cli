package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// TestChatOverviewReadsOnlyTheDatabase covers the contract of Q6: rows come from
// the local database, so the answer needs no Telegram call even for a chat the
// history load has just filled.
func TestChatOverviewReadsOnlyTheDatabase(t *testing.T) {
	e := loggedInEnv(t)
	// `chat info` stores the chat row; the messages come with a history read.
	if r := e.run("chat", "info", "@TeamChat"); r.code != exitcode.OK {
		t.Fatalf("info: exit %d, %q", r.code, r.stdout)
	}
	if r := e.run("message", "history", "@TeamChat", "--limit", "3"); r.code != exitcode.OK {
		t.Fatalf("history: exit %d, %q", r.code, r.stdout)
	}
	// The counters cover the methods of fakeTelegram defined in cli_test.go; methods
	// that other test files add to the fake are not counted.
	before := e.tg.audit01CallSnapshot()
	if before["History"] == 0 || before["ChatInfo"] == 0 {
		t.Fatalf("the setup did not reach the fake Telegram: %v", before)
	}
	r := e.run("chat", "overview")
	if after := e.tg.audit01CallSnapshot(); !reflect.DeepEqual(before, after) {
		t.Errorf("chat overview called Telegram: before %v, after %v", before, after)
	}
	if r.code != exitcode.OK {
		t.Fatalf("overview: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	var got rpc.ChatOverviewResult
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chats) == 0 {
		t.Fatal("the overview is empty after a history load")
	}
	var found *rpc.ChatOverview
	for i := range got.Chats {
		if got.Chats[i].ChatID == 77 {
			found = &got.Chats[i]
		}
	}
	if found == nil {
		t.Fatalf("chat 77 is absent: %+v", got.Chats)
	}
	// The title comes from the chat info the daemon stored; the counts and the
	// previews come from the messages of the history read.
	if found.Title != "Team" || found.Messages != 3 || found.LastMessage == nil || found.FirstMessage == nil {
		t.Errorf("the row lacks stored data: %+v", found)
	}
}

// TestChatOverviewRejectsBadFlags keeps the argument contract of the command.
func TestChatOverviewRejectsBadFlags(t *testing.T) {
	e := loggedInEnv(t)
	wantFailure(t, e.run("chat", "overview", "--type", "robot"), exitcode.Usage)
}
