package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func TestDialogCommandsWithFakeRPC(t *testing.T) {
	commands := schemaCommands(t)
	cases := []struct {
		args   []string
		method string
		reply  any
	}{
		{[]string{"chat", "archive", "77", "--dry-run"}, rpc.MethodChatArchive, rpc.DialogArchiveResult{Chats: []rpc.DialogItem{{ChatID: 77, Archived: true}}, DryRun: true}},
		{[]string{"chat", "unarchive", "77", "--dry-run"}, rpc.MethodChatUnarchive, rpc.DialogArchiveResult{Chats: []rpc.DialogItem{{ChatID: 77}}, DryRun: true}},
		{[]string{"chat", "pin", "77", "--dry-run"}, rpc.MethodChatPinDialog, rpc.DialogPinResult{ChatID: 77, Pinned: true, DryRun: true}},
		{[]string{"chat", "unpin", "77", "--dry-run"}, rpc.MethodChatUnpinDialog, rpc.DialogPinResult{ChatID: 77, DryRun: true}},
		{[]string{"chat", "mute", "77", "--dry-run"}, rpc.MethodChatMute, rpc.NotifyResult{ChatID: 77, DryRun: true}},
		{[]string{"chat", "unmute", "77", "--dry-run"}, rpc.MethodChatUnmute, rpc.NotifyResult{ChatID: 77, DryRun: true}},
		{[]string{"chat", "notify", "77", "--silent=true", "--dry-run"}, rpc.MethodChatNotify, rpc.NotifyResult{ChatID: 77, DryRun: true}},
		{[]string{"folder", "create", "folder", "--include", "77", "--dry-run"}, rpc.MethodFolderCreate, rpc.FolderWriteResult{ID: 2, DryRun: true}},
		{[]string{"folder", "edit", "2", "--rules=", "--dry-run"}, rpc.MethodFolderEdit, rpc.FolderWriteResult{ID: 2, DryRun: true}},
		{[]string{"folder", "delete", "2", "--dry-run"}, rpc.MethodFolderDelete, rpc.FolderDeleteResult{ID: 2, Deleted: true, DryRun: true}},
		{[]string{"folder", "order", "2", "--dry-run"}, rpc.MethodFolderOrder, rpc.FolderOrderResult{IDs: []int{2}, DryRun: true}},
		{[]string{"folder", "invite-info", "t.me/addlist/AbCdEfGh"}, rpc.MethodFolderInviteInfo, rpc.FolderInviteResult{Chats: []rpc.FolderInviteChat{}}},
		{[]string{"saved", "list"}, rpc.MethodSavedList, rpc.SavedListResult{Dialogs: []rpc.SavedDialog{}}},
		{[]string{"saved", "history", "77"}, rpc.MethodSavedHistory, rpc.MessagesResult{Messages: []rpc.Message{}}},
		{[]string{"saved", "search", "--tag", "👍"}, rpc.MethodSavedSearch, rpc.MessagesResult{Messages: []rpc.Message{}}},
		{[]string{"saved", "tags"}, rpc.MethodSavedTags, rpc.SavedTagsResult{Tags: []rpc.SavedTag{}}},
		{[]string{"saved", "tag-rename", "👍", "--clear", "--dry-run"}, rpc.MethodSavedTagRename, rpc.SavedTagRenameResult{Emoji: "👍", Cleared: true, DryRun: true}},
		{[]string{"account", "terminate", "0123456789abcdef", "--dry-run"}, rpc.MethodAccountTerminate, rpc.TerminateResult{ID: "0123456789abcdef", DryRun: true}},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := runDiscoveryCLI(t, tc.args, func(method string, _ json.RawMessage) (any, error) {
				if method != tc.method {
					t.Fatalf("method %s", method)
				}
				return tc.reply, nil
			}, nil)
			if got.code != exitcode.OK || got.method != tc.method {
				t.Fatalf("result %+v", got)
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
