package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestStatsAndBoostsCommands(t *testing.T) {
	r := runDiscoveryCLI(t, []string{"chat", "stats", "42", "--graph", "growth"}, func(method string, raw json.RawMessage) (any, error) {
		var p rpc.ChatStatsParams
		if err := json.Unmarshal(raw, &p); err != nil || method != rpc.MethodChatStats || p.Chat != "42" || p.Graph != "growth" {
			return audit02Reject(t, "stats request: %s %+v %v", method, p, err)
		}
		return rpc.ChatStats{ChatID: 42, Name: "growth", Data: json.RawMessage(`{"columns":[]}`)}, nil
	}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"name":"growth"`) {
		t.Fatalf("stats: %+v", r)
	}
	r = runDiscoveryCLI(t, []string{"chat", "boosts", "42"}, func(method string, _ json.RawMessage) (any, error) {
		if method != rpc.MethodChatBoosts {
			return audit02Reject(t, "method: %s", method)
		}
		return rpc.ChatBoosts{ChatID: 42, Level: 2}, nil
	}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"level":2`) {
		t.Fatalf("boosts: %+v", r)
	}
}
