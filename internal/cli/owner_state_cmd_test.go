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
	return rpc.FoldersResult{Folders: []rpc.Folder{}}, nil
}
func (f *fakeTelegram) ChatStates(context.Context, *chatref.Ref, int) (rpc.ChatStatesResult, error) {
	return rpc.ChatStatesResult{States: []rpc.ChatState{}}, nil
}
func (f *fakeTelegram) Sessions(context.Context) (rpc.SessionsResult, error) {
	return rpc.SessionsResult{Sessions: []rpc.Session{}}, nil
}
func (f *fakeTelegram) Blocked(context.Context, rpc.BlockedParams) (rpc.BlockedResult, error) {
	return rpc.BlockedResult{Peers: []rpc.BlockedPeer{}}, nil
}
func (f *fakeTelegram) NotifyDefaults(context.Context) (rpc.NotifyDefaultsResult, error) {
	return rpc.NotifyDefaultsResult{Defaults: []rpc.NotifyDefault{}}, nil
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
		t.Fatalf("method %s", method)
		return nil, nil
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
