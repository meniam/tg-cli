package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestChatLimitSetPassesTheConfirmationAndTheNumber(t *testing.T) {
	r := runVoiceCLI(t, []string{"chat", "limit", "set", "@alice", "5000", "--yes"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatLimitSetResult{ChatID: 5, Messages: 5000, Keep: 4750, Saved: 6000, WillDelete: 1250}, nil
	})
	var p rpc.ChatLimitSetParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || r.method != rpc.MethodChatLimitSet || p != (rpc.ChatLimitSetParams{Chat: "@alice", Messages: 5000, Confirm: true}) {
		t.Fatalf("set: %+v %+v %v", r, p, err)
	}
	if !strings.Contains(r.stdout, `"will_delete":1250`) {
		t.Fatalf("output: %s", r.stdout)
	}
	// Without --yes the Daemon refuses and the code is 2.
	r = runVoiceCLI(t, []string{"chat", "limit", "set", "--", "-1001234567890", "100"}, nil, func(string, json.RawMessage) (any, error) {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "repeat with --yes", Data: map[string]any{"reason": "confirm_required"}}
	})
	p = rpc.ChatLimitSetParams{}
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != exitcode.Usage || p != (rpc.ChatLimitSetParams{Chat: "-1001234567890", Messages: 100}) || !strings.Contains(r.stdout, "confirm_required") {
		t.Fatalf("unconfirmed: %+v %+v %v", r, p, err)
	}
	for _, args := range [][]string{{"chat", "limit", "set", "@a"}, {"chat", "limit", "set", "@a", "many"}, {"chat", "limit", "set", "@a", "1", "2"}} {
		if r = runVoiceCLI(t, args, nil, nil); r.code != exitcode.Usage || r.method != "" {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}

func TestChatLimitOffAndList(t *testing.T) {
	r := runVoiceCLI(t, []string{"chat", "limit", "off", "@alice"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatLimitOffResult{ChatID: 5, Removed: true}, nil
	})
	if r.code != 0 || r.method != rpc.MethodChatLimitOff || !strings.Contains(r.stdout, `"removed":true`) {
		t.Fatalf("off: %+v", r)
	}
	r = runVoiceCLI(t, []string{"chat", "limit", "list"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatLimitListResult{Chats: []rpc.ChatLimitEntry{{ChatID: 5, Title: "Team", Messages: 5000, Saved: 4800}}}, nil
	})
	if r.code != 0 || r.method != rpc.MethodChatLimitList || !strings.Contains(r.stdout, `"messages":5000`) || !strings.Contains(r.stdout, `"saved":4800`) {
		t.Fatalf("list: %+v", r)
	}
}
