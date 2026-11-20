package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestChatAdminCLIRoutingAndValidation(t *testing.T) {
	audit01Isolate(t)
	reply := func(method string, raw json.RawMessage) (any, error) {
		return map[string]any{"chat_id": 42, "dry_run": true}, nil
	}
	for _, tc := range []struct {
		args   []string
		method string
		params map[string]any
	}{
		{[]string{"chat", "join", "@somechat", "--dry-run"}, rpc.MethodChatJoin, map[string]any{"chat": "@somechat", "dry_run": true}},
		{[]string{"chat", "leave", "42", "--dry-run"}, rpc.MethodChatLeave, map[string]any{"chat": "42", "dry_run": true}},
		{[]string{"chat", "create", "new", "--type", "supergroup", "--dry-run"}, rpc.MethodChatCreate, map[string]any{"title": "new", "type": "supergroup", "dry_run": true}},
		{[]string{"chat", "edit", "42", "--title", "new", "--dry-run"}, rpc.MethodChatEdit, map[string]any{"chat": "42", "title": "new", "dry_run": true}},
		{[]string{"chat", "kick", "42", "123", "--dry-run"}, rpc.MethodChatKick, map[string]any{"chat": "42", "user": "123", "dry_run": true}},
		{[]string{"chat", "ban", "42", "123", "--yes"}, rpc.MethodChatBan, map[string]any{"chat": "42", "user": "123", "until": "", "dry_run": false}},
		{[]string{"chat", "unban", "42", "123"}, rpc.MethodChatUnban, map[string]any{"chat": "42", "user": "123", "dry_run": false}},
		{[]string{"chat", "restrict", "42", "123", "--deny", "send-photos"}, rpc.MethodChatRestrict, map[string]any{"chat": "42", "user": "123", "deny": []any{"send-photos"}, "clear": false, "until": "", "dry_run": false}},
		{[]string{"chat", "permissions", "42", "--clear"}, rpc.MethodChatPermissions, map[string]any{"chat": "42", "clear": true, "deny": nil, "dry_run": false}},
		{[]string{"chat", "slow-mode", "42", "30"}, rpc.MethodChatSlowMode, map[string]any{"chat": "42", "seconds": float64(30), "dry_run": false}},
	} {
		r := runWriteCLI(t, tc.args, "", reply, nil)
		if r.code != exitcode.OK || r.method != tc.method {
			t.Errorf("%v: %+v", tc.args, r)
			continue
		}
		var got map[string]any
		if err := json.Unmarshal(r.params, &got); err != nil {
			t.Errorf("%v: params %s: %v", tc.args, r.params, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.params) {
			t.Errorf("%v: params %v, want %v", tc.args, got, tc.params)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"chat", "leave", "42"}, "add --yes to confirm or --dry-run to preview"},
		{[]string{"chat", "kick", "42", "123"}, "add --yes to confirm or --dry-run to preview"},
		{[]string{"chat", "ban", "42", "123"}, "add --yes to confirm or --dry-run to preview"},
		{[]string{"chat", "permissions", "42", "--deny", "unknown"}, "unknown or duplicate denied right: unknown"},
		{[]string{"chat", "slow-mode", "42", "12"}, "invalid slow mode seconds"},
		{[]string{"chat", "edit", "42"}, "title or about is required"},
		{[]string{"chat", "create", "title"}, "type must be supergroup or channel"},
		// -- keeps cobra from reading -2 as a flag, so the user check itself runs.
		{[]string{"chat", "kick", "--yes", "--", "42", "-2"}, "user must be a username or positive id"},
	} {
		r := runWriteCLI(t, tc.args, "", reply, nil)
		if r.method != "" {
			t.Errorf("%v reached the daemon: %+v", tc.args, r)
			continue
		}
		audit01WantUsage(t, r.code, r.stdout, tc.want)
	}
}
