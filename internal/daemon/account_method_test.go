package daemon

import (
	"testing"
	"tg-cli/internal/rpc"
)

func TestAccountMethod(t *testing.T) {
	tests := []struct {
		method  string
		covered bool
	}{
		{rpc.MethodChatList, true}, {rpc.MethodMessageSend, true}, {rpc.MethodNoteAdd, true},
		{rpc.MethodSearch, true}, {rpc.MethodHistorySyncStart, true}, {rpc.MethodSubscribe, true}, {rpc.MethodUnsubscribe, true},
		{rpc.MethodUserProfile, true}, {rpc.MethodUserBlock, true}, {rpc.MethodUserUnblock, true},
		{rpc.MethodContactList, true}, {rpc.MethodContactAdd, true}, {rpc.MethodContactDelete, true}, {rpc.MethodContactSearch, true},
		{rpc.MethodFolderList, true}, {rpc.MethodAccountSessions, true}, {rpc.MethodAccountBlocked, true}, {rpc.MethodAccountNotify, true},
		{rpc.MethodPing, false}, {rpc.MethodStatus, false}, {rpc.MethodShutdown, false}, {rpc.MethodAuthStart, false}, {rpc.MethodAuthPassword, false}, {rpc.MethodEvent, false}, {"account", false}, {"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			if got := accountMethod(tt.method); got != tt.covered {
				t.Errorf("accountMethod(%q) = %v, want %v", tt.method, got, tt.covered)
			}
		})
	}
}
