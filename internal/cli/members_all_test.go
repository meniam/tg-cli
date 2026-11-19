package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"tg-cli/internal/rpc"
)

func TestMembersAllPages(t *testing.T) {
	calls := 0
	r := runDiscoveryCLI(t, []string{"chat", "members", "42", "--all", "--format", "jsonl"}, func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.MembersParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return audit02Reject(t, "members params: %v", err)
		}
		if p.Offset != calls*200 || p.Limit != 200 {
			return audit02Reject(t, "page: %+v", p)
		}
		calls++
		next := 0
		if calls < 3 {
			next = calls * 200
		}
		return rpc.MembersResult{Members: []rpc.Member{{ID: int64(calls), Name: "A"}}, NextOffset: next}, nil
	}, nil)
	if r.code != 0 || calls != 3 || strings.Count(r.stdout, "\n") != 3 || strings.Contains(r.stdout, "next_offset") {
		t.Fatalf("result: %+v calls=%d", r, calls)
	}
}
func TestMembersAllFailure(t *testing.T) {
	calls := 0
	r := runDiscoveryCLI(t, []string{"chat", "members", "42", "--all", "--format", "jsonl"}, func(_ string, _ json.RawMessage) (any, error) {
		calls++
		if calls == 2 {
			return nil, rpc.NewError(rpc.CodeTelegram, "failed")
		}
		return rpc.MembersResult{Members: []rpc.Member{{ID: 1}}, NextOffset: 200}, nil
	}, nil)
	if r.code != 5 || !strings.Contains(r.stderr, "last offset 200") {
		t.Fatalf("result: %+v", r)
	}
}

func TestMembersAllInvalidOptions(t *testing.T) {
	for _, args := range [][]string{{"chat", "members", "42", "--all"}, {"chat", "members", "42", "--all", "--format", "jsonl", "--limit", "1"}, {"chat", "members", "42", "--all", "--format", "jsonl", "--offset", "0"}} {
		r := runDiscoveryCLI(t, args, nil, nil)
		if r.code != 2 || r.method != "" {
			t.Fatalf("args %v: %+v", args, r)
		}
	}
}
