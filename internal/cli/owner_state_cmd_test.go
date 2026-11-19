package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (f *fakeTelegram) Folders(context.Context) (rpc.FoldersResult, error) {
	return rpc.FoldersResult{Folders: []rpc.Folder{{ID: 2, Kind: "filter", Title: "test"}}}, nil
}
func (f *fakeTelegram) ChatStates(context.Context, *chatref.Ref, int) (rpc.ChatStatesResult, error) {
	return rpc.ChatStatesResult{States: []rpc.ChatState{{ChatID: 77, Unread: 2, TopMessage: 30}}}, nil
}
func (f *fakeTelegram) Sessions(context.Context) (rpc.SessionsResult, error) {
	return rpc.SessionsResult{Sessions: []rpc.Session{{Hash: 1, DeviceModel: "Fixture", Platform: "test", AppName: "fixture"}}}, nil
}
func (f *fakeTelegram) Blocked(context.Context, rpc.BlockedParams) (rpc.BlockedResult, error) {
	return rpc.BlockedResult{Peers: []rpc.BlockedPeer{{PeerID: 8, Type: "user", Title: "Blocked", Username: "blocked", Date: 1}}}, nil
}
func (f *fakeTelegram) NotifyDefaults(context.Context) (rpc.NotifyDefaultsResult, error) {
	return rpc.NotifyDefaultsResult{Defaults: []rpc.NotifyDefault{{Scope: "users"}}}, nil
}

func TestOwnerStateCLI(t *testing.T) {
	fixtures := func(method string, raw json.RawMessage) (any, error) {
		switch method {
		case rpc.MethodFolderList:
			return rpc.FoldersResult{Folders: []rpc.Folder{{ID: 1, Title: "Folder", Kind: "filter"}}}, nil
		case rpc.MethodChatState:
			return rpc.ChatStatesResult{States: []rpc.ChatState{{ChatID: 42}}}, nil
		case rpc.MethodAccountSessions:
			return rpc.SessionsResult{Sessions: []rpc.Session{{DeviceModel: "Device"}}}, nil
		case rpc.MethodAccountBlocked:
			return rpc.BlockedResult{Peers: []rpc.BlockedPeer{{PeerID: 42, Title: "Name"}}, NextCursor: "3"}, nil
		case rpc.MethodAccountNotify:
			return rpc.NotifyDefaultsResult{Defaults: []rpc.NotifyDefault{{Scope: "users"}}}, nil
		}
		return audit02Reject(t, "method %s", method)
	}
	for _, tc := range []struct {
		args   []string
		method string
	}{{[]string{"folder", "list", "--local"}, rpc.MethodFolderList}, {[]string{"chat", "state", "--limit", "2"}, rpc.MethodChatState}, {[]string{"chat", "state", "--local", "--", "-77"}, rpc.MethodChatState}, {[]string{"account", "sessions"}, rpc.MethodAccountSessions}, {[]string{"account", "blocked", "--cursor", "2"}, rpc.MethodAccountBlocked}, {[]string{"account", "notify"}, rpc.MethodAccountNotify}} {
		r := runDiscoveryCLI(t, tc.args, fixtures, nil)
		if r.code != exitcode.OK || r.method != tc.method || !strings.Contains(r.stdout, `"ok":true`) {
			t.Fatalf("args %v: %+v", tc.args, r)
		}
	}
	for _, args := range [][]string{{"chat", "state", "--limit", "0"}, {"chat", "state", "--limit", "501"}, {"chat", "state", "--limit", "3", "42"}, {"account", "blocked", "--limit", "1001"}, {"account", "blocked", "--local", "--cursor", "1"}} {
		r := runDiscoveryCLI(t, args, fixtures, nil)
		if r.code != exitcode.Usage || r.method != "" {
			t.Fatalf("args %v: %+v", args, r)
		}
	}
	r := runDiscoveryCLI(t, []string{"account", "blocked", "--format", "jsonl"}, fixtures, nil)
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"peer_id":42`) || !strings.Contains(r.stdout, `"next_cursor":"3"`) {
		t.Fatalf("jsonl %+v", r)
	}
	r = runDiscoveryCLI(t, []string{"folder", "list", "--human"}, fixtures, nil)
	if r.code != exitcode.OK || !strings.Contains(r.stdout, "Folder") {
		t.Fatalf("human %+v", r)
	}
}

func TestOwnerStateCLIParameters(t *testing.T) {
	reply := func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case rpc.MethodChatState:
			return rpc.ChatStatesResult{States: []rpc.ChatState{}}, nil
		case rpc.MethodAccountBlocked:
			return rpc.BlockedResult{Peers: []rpc.BlockedPeer{}}, nil
		}
		return nil, nil
	}
	r := runDiscoveryCLI(t, []string{"chat", "state", "--local", "--", "-77"}, reply, nil)
	var state rpc.ChatStateParams
	if err := json.Unmarshal(r.params, &state); err != nil || state.Chat != "-77" || !state.Local || state.Limit != 0 {
		t.Fatalf("state %+v %v", state, err)
	}
	r = runDiscoveryCLI(t, []string{"account", "blocked", "--limit", "12", "--cursor", "2"}, reply, nil)
	var blocked rpc.BlockedParams
	if err := json.Unmarshal(r.params, &blocked); err != nil || blocked.Limit != 12 || blocked.Cursor != "2" || blocked.Local {
		t.Fatalf("blocked %+v %v", blocked, err)
	}
}

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
