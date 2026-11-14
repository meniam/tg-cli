package cli

import (
	"encoding/json"
	"testing"
	"tg-cli/internal/rpc"
)

func TestDraftAndScheduledCLI(t *testing.T) {
	tests := []struct {
		args   []string
		method string
		reply  any
	}{
		{[]string{"message", "scheduled", "list", "42"}, rpc.MethodMessageScheduledList, rpc.ScheduledResult{ChatID: 42, Messages: []rpc.Message{}}},
		{[]string{"message", "scheduled", "delete", "42", "5", "--dry-run"}, rpc.MethodMessageScheduledDelete, rpc.ScheduledResult{ChatID: 42, DryRun: true}},
		{[]string{"message", "scheduled", "send-now", "42", "5"}, rpc.MethodMessageScheduledSendNow, rpc.ScheduledResult{ChatID: 42, Messages: []rpc.Message{}}},
		{[]string{"chat", "draft", "get", "42"}, rpc.MethodChatDraftGet, rpc.Draft{ChatID: 42, Empty: true}},
		{[]string{"chat", "draft", "list"}, rpc.MethodChatDraftList, rpc.DraftList{Drafts: []rpc.Draft{}}},
		{[]string{"chat", "draft", "set", "42", "--text", "hi", "--reply-to", "7"}, rpc.MethodChatDraftSet, rpc.DraftWriteResult{ChatID: 42, TextLength: 2}},
		{[]string{"chat", "draft", "clear", "42"}, rpc.MethodChatDraftClear, rpc.DraftWriteResult{ChatID: 42, Cleared: true}},
	}
	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			r := runWriteCLI(t, tc.args, "", func(string, json.RawMessage) (any, error) { return tc.reply, nil }, nil)
			if r.code != 0 || r.method != tc.method {
				t.Fatalf("%+v", r)
			}
		})
	}
}
