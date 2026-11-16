package cli

import (
	"encoding/json"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// TestChatInfoFlags covers the new fields of `chat info`: the creation date and the
// restriction with its reason, and that the daemon stored them for the overview.
func TestChatInfoFlags(t *testing.T) {
	e := loggedInEnv(t)
	r := e.run("chat", "info", "@TeamChat")
	if r.code != exitcode.OK {
		t.Fatalf("info: exit %d, %q", r.code, r.stdout)
	}
	var got rpc.ChatInfo
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt != 1700000000 || !got.Restricted || len(got.RestrictionReason) != 1 {
		t.Fatalf("flags = %+v", got)
	}
	if got.RestrictionReason[0].Platform != "ios" || got.RestrictionReason[0].Reason != "porno" {
		t.Errorf("reason = %+v", got.RestrictionReason[0])
	}
	// The flags were stored with the chat, so the local overview shows the date.
	overview := e.run("chat", "overview")
	if overview.code != exitcode.OK {
		t.Fatalf("overview: exit %d, %q", overview.code, overview.stdout)
	}
	var rows rpc.ChatOverviewResult
	if err := json.Unmarshal(parse(t, overview).Data, &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows.Chats {
		if row.ChatID == 77 {
			found = true
			if row.CreatedAt == nil || *row.CreatedAt != 1700000000 {
				t.Errorf("the overview lost the creation date: %+v", row.CreatedAt)
			}
		}
	}
	if !found {
		t.Errorf("chat 77 is absent from the overview: %+v", rows.Chats)
	}
}
