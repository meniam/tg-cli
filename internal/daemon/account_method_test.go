package daemon

import (
	"context"
	"errors"
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
		// The full-data job reads Telegram, so it needs the account checks too.
		{rpc.MethodChatFullStart, true}, {rpc.MethodChatFullStatus, true}, {rpc.MethodChatFullCancel, true},
		{rpc.MethodChatOverview, true}, {rpc.MethodFolderList, true},
		{rpc.MethodUserProfile, true}, {rpc.MethodUserBlock, true}, {rpc.MethodUserUnblock, true},
		{rpc.MethodContactList, true}, {rpc.MethodContactAdd, true}, {rpc.MethodContactDelete, true}, {rpc.MethodContactSearch, true},
		{rpc.MethodAccountSessions, true}, {rpc.MethodAccountBlocked, true}, {rpc.MethodAccountNotify, true},
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

// TestNeedsTelegram covers which methods require a live Telegram connection. The
// status and the cancel of a background job work on in-memory state, so they stay
// available while the client is offline; a start always needs the connection.
func TestNeedsTelegram(t *testing.T) {
	for _, method := range []string{
		rpc.MethodHistorySyncStatus, rpc.MethodHistorySyncCancel,
		rpc.MethodChatFullStatus, rpc.MethodChatFullCancel,
		rpc.MethodChatOverview, rpc.MethodSearch, rpc.MethodNoteList,
	} {
		if needsTelegram(method) {
			t.Errorf("needsTelegram(%q) = true, want false", method)
		}
	}
	for _, method := range []string{
		rpc.MethodChatFullStart, rpc.MethodHistorySyncStart, rpc.MethodChatList, rpc.MethodChatInfo, rpc.MethodSessionExport,
	} {
		if !needsTelegram(method) {
			t.Errorf("needsTelegram(%q) = false, want true", method)
		}
	}
}

// TestGuardRequestKeepsAccountChecks keeps the authorization checks of a local job
// method: it needs no connection, but a lost login and a wrong expected account
// still stop it, exactly as they stop the history load.
func TestGuardRequestKeepsAccountChecks(t *testing.T) {
	d, tg, _, _ := accessTestDaemon(t)
	tg.user = &rpc.User{ID: 42, Username: "me"}
	// A factory makes the offline state reachable: guardRequest only reports it
	// when the client can be replaced.
	d.opts.NewTelegram = func(context.Context, TelegramConfig) (Telegram, error) { return tg, nil }
	d.admission.block()

	for _, method := range []string{rpc.MethodChatFullStatus, rpc.MethodChatFullCancel} {
		// No connection: still allowed.
		if err := d.guardRequest(rpc.Request{Method: method}); err != nil {
			t.Errorf("%s while offline: %v", method, err)
		}
		// A lost login: code 4, the same answer as a method that needs Telegram.
		d.setAuthLost("key_invalid")
		err := d.guardRequest(rpc.Request{Method: method})
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != rpc.CodeNotAuthorized {
			t.Errorf("%s after authorization loss: %v", method, err)
		}
		d.setAuthLost("")
		// A wrong expected account: code 4 as well.
		err = d.guardRequest(rpc.Request{Method: method, ExpectUser: "@other"})
		if !errors.As(err, &re) || re.Code != rpc.CodeNotAuthorized {
			t.Errorf("%s with another account: %v", method, err)
		}
	}
	// A start needs the connection, so it is the offline code.
	var re *rpc.Error
	if err := d.guardRequest(rpc.Request{Method: rpc.MethodChatFullStart}); !errors.As(err, &re) || re.Code != rpc.CodeOffline {
		t.Errorf("chat_full.start while offline: %v", err)
	}
}
