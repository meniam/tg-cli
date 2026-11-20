package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestDraftAndScheduledCLI(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stdin  string
		method string
		params func(*testing.T, json.RawMessage)
		reply  any
	}{
		{"scheduled list", []string{"message", "scheduled", "list", "42"}, "", rpc.MethodMessageScheduledList, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.ScheduledParams{Chat: "42"})
		}, rpc.ScheduledResult{ChatID: 42, Messages: []rpc.Message{}}},
		{"scheduled delete", []string{"message", "scheduled", "delete", "42", "5", "6", "--dry-run"}, "", rpc.MethodMessageScheduledDelete, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.ScheduledParams{Chat: "42", IDs: []int{5, 6}, DryRun: true})
		}, rpc.ScheduledResult{ChatID: 42, DryRun: true}},
		{"scheduled send-now", []string{"message", "scheduled", "send-now", "42", "5"}, "", rpc.MethodMessageScheduledSendNow, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.ScheduledParams{Chat: "42", IDs: []int{5}})
		}, rpc.ScheduledResult{ChatID: 42, Messages: []rpc.Message{}}},
		{"draft get", []string{"chat", "draft", "get", "42"}, "", rpc.MethodChatDraftGet, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.DraftParams{Chat: "42"})
		}, rpc.Draft{ChatID: 42, Empty: true}},
		{"draft list", []string{"chat", "draft", "list"}, "", rpc.MethodChatDraftList, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.DraftParams{})
		}, rpc.DraftList{Drafts: []rpc.Draft{}}},
		{"draft set text and reply", []string{"chat", "draft", "set", "42", "--text", "hi", "--reply-to", "7", "--dry-run"}, "", rpc.MethodChatDraftSet, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.DraftParams{Chat: "42", Text: "hi", ReplyTo: 7, DryRun: true})
		}, rpc.DraftWriteResult{ChatID: 42, TextLength: 2, DryRun: true}},
		{"draft set from stdin", []string{"chat", "draft", "set", "42"}, "from stdin", rpc.MethodChatDraftSet, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.DraftParams{Chat: "42", Text: "from stdin"})
		}, rpc.DraftWriteResult{ChatID: 42, TextLength: 10}},
		{"draft clear", []string{"chat", "draft", "clear", "42", "--dry-run"}, "", rpc.MethodChatDraftClear, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.DraftParams{Chat: "42", DryRun: true})
		}, rpc.DraftWriteResult{ChatID: 42, Cleared: true, DryRun: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, tc.args, tc.stdin, func(string, json.RawMessage) (any, error) { return tc.reply, nil }, nil)
			if r.code != exitcode.OK || r.method != tc.method {
				t.Fatalf("%+v", r)
			}
			tc.params(t, r.params)
			ok, data := audit02Envelope(t, r.stdout)
			if !ok {
				t.Fatalf("envelope is not ok: %s", r.stdout)
			}
			var got, want map[string]any
			wantJSON, _ := json.Marshal(tc.reply)
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wantJSON, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("data %s, want %s", data, wantJSON)
			}
		})
	}
}
