package cli

import (
	"encoding/json"
	"testing"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestChatAdminCLIRoutingAndValidation(t *testing.T) {
	reply := func(method string, raw json.RawMessage) (any, error) {
		return map[string]any{"chat_id": 42, "dry_run": true}, nil
	}
	for _, tc := range []struct {
		args   []string
		method string
	}{
		{[]string{"chat", "join", "@somechat", "--dry-run"}, rpc.MethodChatJoin}, {[]string{"chat", "leave", "42", "--dry-run"}, rpc.MethodChatLeave},
		{[]string{"chat", "create", "new", "--type", "supergroup", "--dry-run"}, rpc.MethodChatCreate}, {[]string{"chat", "edit", "42", "--title", "new", "--dry-run"}, rpc.MethodChatEdit},
		{[]string{"chat", "kick", "42", "123", "--dry-run"}, rpc.MethodChatKick}, {[]string{"chat", "ban", "42", "123", "--yes"}, rpc.MethodChatBan},
		{[]string{"chat", "unban", "42", "123"}, rpc.MethodChatUnban}, {[]string{"chat", "restrict", "42", "123", "--deny", "send-photos"}, rpc.MethodChatRestrict},
		{[]string{"chat", "permissions", "42", "--clear"}, rpc.MethodChatPermissions}, {[]string{"chat", "slow-mode", "42", "30"}, rpc.MethodChatSlowMode},
	} {
		r := runWriteCLI(t, tc.args, "", reply, nil)
		if r.code != exitcode.OK || r.method != tc.method {
			t.Errorf("%v: %+v", tc.args, r)
		}
	}
	for _, args := range [][]string{{"chat", "leave", "42"}, {"chat", "kick", "42", "123"}, {"chat", "ban", "42", "123"}, {"chat", "permissions", "42", "--deny", "unknown"}, {"chat", "slow-mode", "42", "12"}, {"chat", "edit", "42"}, {"chat", "create", "title"}, {"chat", "kick", "42", "-2", "--yes"}} {
		r := runWriteCLI(t, args, "", reply, nil)
		if r.code != exitcode.Usage || r.method != "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}
