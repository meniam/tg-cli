package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// auditWriteTelegram mirrors the numeric-ID contract of the Telegram client.
type auditWriteTelegram struct{ *fakeTelegram }

func (f auditWriteTelegram) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	if ref.Kind == chatref.ID && ref.ID == 999 {
		return rpc.Chat{}, rpc.ErrUnknownChatID()
	}
	return f.fakeTelegram.Resolve(ctx, ref)
}

func TestRegisteredWritesHideClosedAndUnknownIDs(t *testing.T) {
	// The remaining writes have no target chat. Settings list values are
	// configuration data, not references to be resolved as chats.
	excluded := map[string]string{
		rpc.MethodSettingsSet:         "profile setting or owner credential",
		rpc.MethodSettingsUnset:       "profile setting or owner credential",
		rpc.MethodSettingsAdd:         "profile setting or owner credential",
		rpc.MethodSettingsRemove:      "profile setting or owner credential",
		rpc.MethodSettingsTokenInit:   "profile setting or owner credential",
		rpc.MethodSettingsTokenChange: "profile setting or owner credential",
		rpc.MethodAuthStart:           "session login flow",
		rpc.MethodAuthStartQR:         "session login flow",
		rpc.MethodAuthCode:            "session login flow",
		rpc.MethodAuthPassword:        "session login flow",
		rpc.MethodAuthWait:            "session login flow",
		rpc.MethodAuthCancel:          "session login flow",
		rpc.MethodAuthResend:          "session login flow",
		rpc.MethodAccountTerminate:    "session identifier",
		rpc.MethodFolderOrder:         "folder IDs",
		rpc.MethodFolderDelete:        "folder IDs",
		rpc.MethodChatCreate:          "new chat has no ID yet",
		rpc.MethodSavedTagRename:      "owner’s saved tag",
		rpc.MethodNoteDelete:          "note ID or author",
		rpc.MethodNoteDeleteByAuthor:  "note ID or author",
		rpc.MethodSearchIndexStart:    "profile-wide job",
		rpc.MethodSearchIndexCancel:   "profile-wide job",
		rpc.MethodVoiceBackfill:       "profile-wide job",
		rpc.MethodChatFullCancel:      "profile-wide job",
		rpc.MethodShutdown:            "daemon lifecycle",
	}
	for method, class := range registeredAccessClasses() {
		if !strings.HasSuffix(class, "-write") && class != "chat-admin" && class != "chat-create" {
			continue
		}
		if excluded[method] != "" {
			continue
		}
		t.Run(method, func(t *testing.T) {
			d, tg, db, _ := accessTestDaemon(t)
			d.opts.Telegram = auditWriteTelegram{tg}
			ctx := t.Context()
			d.sync = newHistorySyncManager(ctx, d, nil)
			d.full = newChatFullSyncManager(ctx, d, nil)
			defer d.full.close()
			defer d.sync.close()
			d.startVoice(ctx)
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.block": params(t, []int{77}), "access.profile": params(t, true), "access.downloads": params(t, []string{dir})}); err != nil {
				t.Fatal(err)
			}
			d.opts.Access = nil
			d.refreshAccess(ctx)
			if err := db.UpsertChats(ctx, []store.Chat{{ID: 77, Type: "supergroup", Title: "Closed"}}); err != nil {
				t.Fatal(err)
			}
			d.register()
			server, conn := net.Pipe()
			done := make(chan struct{})
			go func() { d.srv.ServeConn(server); close(done) }()
			c := rpc.NewClient(conn)
			defer func() { c.Close(); <-done }()
			call := func(id string) *rpc.Error {
				p := map[string]any{"chat": id}
				switch method {
				case rpc.MethodWebSetChatSettings:
					p["history_mode"] = "auto"
				case rpc.MethodFolderCreate, rpc.MethodFolderEdit:
					p = map[string]any{"id": 2, "title": "Folder", "include": []string{id}}
				case rpc.MethodAccountSet:
					p = map[string]any{"personal_channel": id}
				case rpc.MethodAccountPrivacySet:
					p = map[string]any{"key": "last-seen", "default": "contacts", "allow_chats": []string{id}, "yes": true}
				case rpc.MethodChatArchive, rpc.MethodChatUnarchive, rpc.MethodChatPinDialog, rpc.MethodChatUnpinDialog:
					p = map[string]any{"chats": []string{id}}
				case rpc.MethodChatMute, rpc.MethodChatUnmute, rpc.MethodChatNotify:
					p["silent"] = true
				case rpc.MethodChatSet:
					p["forum"] = "on"
				case rpc.MethodUserBlock, rpc.MethodUserUnblock, rpc.MethodContactAdd, rpc.MethodContactDelete:
					p = map[string]any{"user": id, "yes": true, "first_name": "Test"}
				case rpc.MethodChatApprove, rpc.MethodChatDecline, rpc.MethodChatKick, rpc.MethodChatBan, rpc.MethodChatUnban, rpc.MethodChatRestrict:
					p["user"] = "5"
				case rpc.MethodChatJoin, rpc.MethodChatLeave, rpc.MethodChatEdit, rpc.MethodChatPermissions, rpc.MethodChatSlowMode:
				case rpc.MethodMessageDownload:
					p["ids"] = []int{1}
					p["output"] = dir
				case rpc.MethodMessageExportMonth:
					p = map[string]any{"chats": []string{id}, "output": dir, "month": "2020-01"}
				case rpc.MethodNoteAdd:
					p["kind"] = "chat"
					p["body"] = "note"
				case rpc.MethodMessageSend, rpc.MethodChatDraftSet:
					p["text"] = "hello"
				case rpc.MethodMessageEdit:
					p["id"] = 1
					p["text"] = "hello"
				case rpc.MethodMessageForward:
					p["from"] = id
					p["to"] = "7"
					p["ids"] = []int{1}
				case rpc.MethodMessageDelete, rpc.MethodMessageScheduledDelete, rpc.MethodMessageScheduledSendNow:
					p["ids"] = []int{1}
					p["revoke"] = true
				case rpc.MethodMessageSendMedia:
					p["as"] = "document"
					p["files"] = []string{"/tmp/file"}
				case rpc.MethodMessageEditMedia:
					p["as"] = "document"
					p["file"] = "/tmp/file"
					p["id"] = 1
				case rpc.MethodMessagePress:
					p["id"] = 1
					p["button"] = 0
				case rpc.MethodMessageReact:
					p["id"] = 1
					p["emoji"] = "👍"
				case rpc.MethodMessagePin, rpc.MethodMessageUnpin, rpc.MethodVoiceCancel, rpc.MethodVoiceRetry:
					p["id"] = 1
				case rpc.MethodMessagePoll:
					p["question"] = "Question?"
					p["options"] = []string{"one", "two"}
				case rpc.MethodMessageSendInline:
					p["query_id"] = "1"
					p["result"] = "x"
				case rpc.MethodMessageInlineQuery:
					p["bot"] = "@examplebot"
					p["query"] = "x"
				case rpc.MethodMessageSendSticker:
					p["set"] = "fixture"
					p["index"] = 0
				case rpc.MethodChatTyping:
					p["action"] = "typing"
				case rpc.MethodChatAutoDelete:
					p["period"] = "1d"
				case rpc.MethodChatDraftClear, rpc.MethodChatMarkRead, rpc.MethodChatVoiceOn, rpc.MethodChatVoiceOff, rpc.MethodChatFullStart, rpc.MethodHistorySyncStart, rpc.MethodHistorySyncCancel:
				default:
					t.Fatalf("missing write probe for %s", method)
				}
				var re *rpc.Error
				err := rpcCall(t, c, method, p, nil)
				if !errors.As(err, &re) {
					t.Fatalf("%s: expected not found, got %v", id, err)
				}
				return re
			}
			closed, unknown := call("77"), call("999")
			if closed.Code != rpc.CodeNotFound || closed.Code != unknown.Code || closed.Message != unknown.Message {
				t.Fatalf("closed: %v; unknown: %v", closed, unknown)
			}
			tg.mu.Lock()
			tg.user = nil
			tg.mu.Unlock()
			closed, unknown = call("77"), call("999")
			if closed.Code != unknown.Code || closed.Message != unknown.Message {
				t.Errorf("unauthorized closed: %v; unknown: %v", closed, unknown)
			}
			tg.mu.Lock()
			tg.user = &rpc.User{ID: 42}
			tg.mu.Unlock()
			// Network errors during Resolve may distinguish unknown IDs; this is
			// an accepted risk in .agents/plans/20261120_2242_settings_in_database.md.
		})
	}
}
