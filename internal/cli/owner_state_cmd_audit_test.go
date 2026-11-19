package cli

import (
	"context"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// Fake Telegram methods for sessions, dialogs, folders, notifications and saved
// messages, used by many command tests. They sit here, not in owner_state_cmd_test.go,
// because they are not about the owner-state commands; cli_test.go next to the
// other fakeTelegram methods is their natural home.

func (f *fakeTelegram) TerminateSession(context.Context, int64) error { return nil }

func (f *fakeTelegram) ArchiveDialogs(context.Context, []chatref.Ref, bool) error { return nil }
func (f *fakeTelegram) PinDialog(context.Context, chatref.Ref, bool) error        { return nil }

func (f *fakeTelegram) RefreshDialogStates(context.Context, []chatref.Ref) (rpc.ChatStatesResult, error) {
	return rpc.ChatStatesResult{}, nil
}

func (f *fakeTelegram) ChangeNotify(_ context.Context, _ chatref.Ref, p rpc.NotifyParams) (rpc.NotifyResult, error) {
	return rpc.NotifyResult{MuteUntil: p.MuteUntil, Silent: p.Silent, ShowPreviews: p.Previews, DryRun: p.DryRun}, nil
}

func (f *fakeTelegram) FolderInviteInfo(context.Context, string) (rpc.FolderInviteResult, error) {
	return rpc.FolderInviteResult{Title: "Folder", Chats: []rpc.FolderInviteChat{{ID: 77, Type: "group", Title: "Group"}}}, nil
}

func (f *fakeTelegram) OrderFolders(context.Context, []int, bool) error { return nil }

func (f *fakeTelegram) SavedTags(context.Context, *chatref.Ref) (rpc.SavedTagsResult, error) {
	return rpc.SavedTagsResult{Tags: []rpc.SavedTag{{Emoji: "👍", Title: "tag", Count: 1}}}, nil
}
func (f *fakeTelegram) RenameSavedTag(context.Context, rpc.SavedTagRenameParams) error { return nil }

func (f *fakeTelegram) SavedMessages(context.Context, int64, *chatref.Ref, rpc.SavedMessagesParams, bool) (rpc.MessagesResult, error) {
	return rpc.MessagesResult{Messages: []rpc.Message{{ChatID: 77, ID: 5, Date: "2026-11-14T10:00:00Z", Text: "saved"}}}, nil
}

func (f *fakeTelegram) SavedList(context.Context, rpc.SavedListParams) (rpc.SavedListResult, error) {
	return rpc.SavedListResult{Dialogs: []rpc.SavedDialog{{PeerID: 77, Title: "test", Type: "group"}}}, nil
}

func (f *fakeTelegram) WriteFolder(_ context.Context, p rpc.FolderWriteParams, action string) (rpc.FolderWriteResult, error) {
	return rpc.FolderWriteResult{ID: 2, Deleted: action == "delete", DryRun: p.DryRun}, nil
}
