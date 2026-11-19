package daemon

import (
	"context"
	"errors"
	"net"
	"testing"

	"tg-cli/internal/rpc"
)

// audit03NoChatMethods take no chat reference, so the closed-chat rule does not apply.
var audit03NoChatMethods = map[string]bool{
	rpc.MethodAccountBlocked: true, rpc.MethodAccountEmojiStatuses: true, rpc.MethodAccountNotify: true,
	rpc.MethodAccountPrivacyGet: true, rpc.MethodAccountPrivacySet: true, rpc.MethodAccountProfile: true,
	rpc.MethodAccountSessions: true, rpc.MethodAccountSet: true, rpc.MethodAccountTerminate: true,
	rpc.MethodAuthCancel: true, rpc.MethodAuthCode: true, rpc.MethodAuthPassword: true, rpc.MethodAuthResend: true,
	rpc.MethodAuthStart: true, rpc.MethodAuthStartQR: true, rpc.MethodAuthWait: true,
	rpc.MethodChatList: true, rpc.MethodChatOverview: true, rpc.MethodChatUnread: true,
	rpc.MethodChatFullCancel: true, rpc.MethodChatFullStart: true, rpc.MethodChatFullStatus: true,
	rpc.MethodChatDraftList: true, rpc.MethodChatCreate: true, rpc.MethodContactList: true, rpc.MethodContactSearch: true,
	rpc.MethodFolderCreate: true, rpc.MethodFolderDelete: true, rpc.MethodFolderEdit: true, rpc.MethodFolderInviteInfo: true,
	rpc.MethodFolderList: true, rpc.MethodFolderOrder: true,
	rpc.MethodNoteAdd: true, rpc.MethodNoteDelete: true, rpc.MethodNoteDeleteByAuthor: true, rpc.MethodNoteList: true,
	rpc.MethodPing: true, rpc.MethodSearch: true, rpc.MethodSessionExport: true, rpc.MethodShutdown: true, rpc.MethodStatus: true,
	rpc.MethodStickerInstalled: true, rpc.MethodStickerSearch: true, rpc.MethodStickerSet: true,
	rpc.MethodSavedList: true, rpc.MethodSavedTags: true, rpc.MethodSavedHistory: true, rpc.MethodSavedSearch: true, rpc.MethodSavedTagRename: true,
	rpc.MethodMessageSearchPublic: true, rpc.MethodSubscribe: true, rpc.MethodUnsubscribe: true,
}

// audit03ParamsFirst validate other parameters before the chat is looked at,
// so the shared probe below cannot reach the chat check. They are not covered
// by this sweep; their own tests exercise them.
var audit03ParamsFirst = map[string]bool{
	rpc.MethodChatArchive: true, rpc.MethodChatUnarchive: true, rpc.MethodChatPinDialog: true, rpc.MethodChatUnpinDialog: true,
	rpc.MethodChatAutoDelete: true, rpc.MethodChatInviteInfo: true, rpc.MethodChatMute: true, rpc.MethodChatUnmute: true,
	rpc.MethodChatNotify: true, rpc.MethodChatSet: true, rpc.MethodChatTyping: true,
	rpc.MethodMessageDelete: true, rpc.MethodMessageDownload: true, rpc.MethodMessageEditMedia: true,
	rpc.MethodMessageExportMonth: true, rpc.MethodMessageExportVerify: true, rpc.MethodMessagePoll: true,
	rpc.MethodMessageReact: true, rpc.MethodMessageSearch: true, rpc.MethodMessageSendInline: true,
	rpc.MethodMessageSendMedia: true, rpc.MethodMessageSendSticker: true, rpc.MethodMessageTranslate: true,
}

// Every method that takes a chat reference answers a closed chat like an
// unknown one. The access class is not read from a table: a method that loses
// its access check starts answering instead of refusing.
func TestAudit03ChatMethodsRefuseAClosedChat(t *testing.T) {
	d, tg, _, path := accessTestDaemon(t)
	d.register()
	d.srv.SetEventSource(d.hub, d.replayEvents)
	writeAccess(t, path, `{"block":[77]}`)
	tg.resolved = &rpc.Chat{ID: 77, Type: "group", Title: "Private"}
	probe := map[string]any{"chat": "77", "from": "77", "to": "77", "id": 1, "ids": []int{1}, "user": "77", "yes": true, "text": "x", "message_id": 1, "kind": "chat"}
	want := rpc.ErrUnknownChatID().Message
	checked := 0
	for _, method := range d.srv.Methods() {
		if audit03NoChatMethods[method] || audit03ParamsFirst[method] {
			continue
		}
		t.Run(method, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			go d.srv.ServeConn(serverConn)
			client := rpc.NewClient(clientConn)
			defer client.Close()
			var result any
			err := client.Call(context.Background(), method, probe, &result)
			var rpcErr *rpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound || rpcErr.Message != want {
				t.Fatalf("%s on a closed chat: result=%v err=%v, want %q", method, result, err, want)
			}
		})
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d methods were probed; the sweep lost its coverage", checked)
	}
}
