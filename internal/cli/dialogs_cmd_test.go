package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func TestDialogCommandsWithFakeRPC(t *testing.T) {
	audit01Isolate(t)
	commands := schemaCommands(t)
	cases := []struct {
		args   []string
		method string
		reply  any
		params string // exact JSON of the request the command must send
	}{
		{[]string{"chat", "archive", "77", "--dry-run"}, rpc.MethodChatArchive, rpc.DialogArchiveResult{Chats: []rpc.DialogItem{{ChatID: 77, Archived: true}}, DryRun: true}, `{"chats":["77"],"dry_run":true}`},
		{[]string{"chat", "unarchive", "77", "--dry-run"}, rpc.MethodChatUnarchive, rpc.DialogArchiveResult{Chats: []rpc.DialogItem{{ChatID: 77}}, DryRun: true}, `{"chats":["77"],"dry_run":true}`},
		{[]string{"chat", "pin", "77", "--dry-run"}, rpc.MethodChatPinDialog, rpc.DialogPinResult{ChatID: 77, Pinned: true, DryRun: true}, `{"chats":["77"],"dry_run":true}`},
		{[]string{"chat", "unpin", "77", "--dry-run"}, rpc.MethodChatUnpinDialog, rpc.DialogPinResult{ChatID: 77, DryRun: true}, `{"chats":["77"],"dry_run":true}`},
		{[]string{"chat", "mute", "77", "--dry-run"}, rpc.MethodChatMute, rpc.NotifyResult{ChatID: 77, DryRun: true}, `{"chat":"77","mute_until":2147483647,"dry_run":true}`},
		{[]string{"chat", "unmute", "77", "--dry-run"}, rpc.MethodChatUnmute, rpc.NotifyResult{ChatID: 77, DryRun: true}, `{"chat":"77","mute_until":0,"dry_run":true}`},
		{[]string{"chat", "notify", "77", "--silent=true", "--dry-run"}, rpc.MethodChatNotify, rpc.NotifyResult{ChatID: 77, DryRun: true}, `{"chat":"77","silent":true,"dry_run":true}`},
		{[]string{"folder", "create", "folder", "--include", "77", "--dry-run"}, rpc.MethodFolderCreate, rpc.FolderWriteResult{ID: 2, DryRun: true}, `{"title":"folder","include":["77"],"dry_run":true}`},
		{[]string{"folder", "edit", "2", "--rules=", "--dry-run"}, rpc.MethodFolderEdit, rpc.FolderWriteResult{ID: 2, DryRun: true}, `{"id":2,"rules":"","dry_run":true}`},
		{[]string{"folder", "delete", "2", "--dry-run"}, rpc.MethodFolderDelete, rpc.FolderDeleteResult{ID: 2, Deleted: true, DryRun: true}, `{"id":2,"dry_run":true}`},
		{[]string{"folder", "order", "2", "--dry-run"}, rpc.MethodFolderOrder, rpc.FolderOrderResult{IDs: []int{2}, DryRun: true}, `{"ids":[2],"dry_run":true}`},
		{[]string{"folder", "invite-info", "t.me/addlist/AbCdEfGh"}, rpc.MethodFolderInviteInfo, rpc.FolderInviteResult{Title: "Shared", Chats: []rpc.FolderInviteChat{{ID: 77, Type: "group", Title: "Team", Members: 3}}}, `{"link":"t.me/addlist/AbCdEfGh"}`},
		{[]string{"saved", "list"}, rpc.MethodSavedList, rpc.SavedListResult{Dialogs: []rpc.SavedDialog{{PeerID: 77, Type: "user", Title: "Ann", TopMessage: 5, Date: "2026-11-14T10:00:00Z"}}}, `{"limit":50}`},
		{[]string{"saved", "history", "77"}, rpc.MethodSavedHistory, rpc.MessagesResult{Messages: []rpc.Message{{ID: 5, ChatID: 77, Date: "2026-11-14T10:00:00Z", Text: "saved"}}}, `{"peer":"77","limit":50}`},
		{[]string{"saved", "search", "--tag", "👍"}, rpc.MethodSavedSearch, rpc.MessagesResult{Messages: []rpc.Message{{ID: 5, ChatID: 77, Date: "2026-11-14T10:00:00Z", Text: "saved"}}}, `{"tag":"👍","limit":50}`},
		{[]string{"saved", "tags"}, rpc.MethodSavedTags, rpc.SavedTagsResult{Tags: []rpc.SavedTag{{Emoji: "👍", Title: "good", Count: 2}}}, `{}`},
		{[]string{"saved", "tag-rename", "👍", "--clear", "--dry-run"}, rpc.MethodSavedTagRename, rpc.SavedTagRenameResult{Emoji: "👍", Cleared: true, DryRun: true}, `{"emoji":"👍","clear":true,"dry_run":true}`},
		{[]string{"account", "terminate", "0123456789abcdef", "--dry-run"}, rpc.MethodAccountTerminate, rpc.TerminateResult{ID: "0123456789abcdef", DryRun: true}, `{"id":"0123456789abcdef","dry_run":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := runDiscoveryCLI(t, tc.args, func(method string, _ json.RawMessage) (any, error) {
				return tc.reply, nil
			}, nil)
			if got.code != exitcode.OK || got.method != tc.method {
				t.Fatalf("result %+v", got)
			}
			if !reflect.DeepEqual(audit01Decode(t, got.params), audit01Decode(t, json.RawMessage(tc.params))) {
				t.Fatalf("params %s, want %s", got.params, tc.params)
			}
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
				t.Fatal(err)
			}
			paths, err := schema.Paths(envelope.Data)
			if err != nil {
				t.Fatal(err)
			}
			if missing := schema.Missing(paths, commands[strings.Join(tc.args[:2], " ")].Fields); len(missing) != 0 {
				t.Fatalf("undocumented fields: %v", missing)
			}
		})
	}
}

func audit01Decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}
