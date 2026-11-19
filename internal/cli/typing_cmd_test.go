package cli

import (
	"encoding/json"
	"testing"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestTypingCLI(t *testing.T) {
	reply := func(method string, raw json.RawMessage) (any, error) {
		var p rpc.TypingParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return audit02Reject(t, "typing params: %v", err)
		}
		return rpc.TypingResult{ChatID: 42, Action: p.Action, DryRun: p.DryRun}, nil
	}
	r := runWriteCLI(t, []string{"chat", "typing", "42", "--topic", "7", "--cancel", "--dry-run"}, "", reply, nil)
	var p rpc.TypingParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != exitcode.OK || r.method != rpc.MethodChatTyping || p.Action != "cancel" || p.Topic != 7 || !p.DryRun {
		t.Fatalf("result=%+v params=%+v error=%v", r, p, err)
	}
	r = runWriteCLI(t, []string{"chat", "typing", "42", "--action", "bad"}, "", reply, nil)
	if r.code != exitcode.Usage || r.method != "" {
		t.Fatalf("invalid action: %+v", r)
	}
}
