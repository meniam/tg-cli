package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"go/ast"
	"go/types"
	"golang.org/x/tools/go/packages"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestRawLegacyColumnsOverrideRaw(t *testing.T) {
	d, _, db, path := accessTestDaemon(t)
	writeAccess(t, path, `{"block":[-1000000000777]}`)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "user", Title: "Allowed"}}); err != nil {
		t.Fatal(err)
	}
	senderID, senderName := int64(7), "Allowed sender"
	raw := json.RawMessage(`{"sender":{"username":"CLOSED-TITLE-7f3a"},"text":"CLOSED-TITLE-7f3a","message":"CLOSED-TITLE-7f3a"}`)
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 42, ID: 1, Date: 1700000000, Text: "Allowed", SenderID: &senderID, SenderName: &senderName, Raw: raw}}); err != nil {
		t.Fatal(err)
	}
	response, err := d.localContext(ctx, rpc.ContextParams{Chat: "42", ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	messages := response.(rpc.MessagesResult).Messages
	if len(messages) != 1 || messages[0].Text != "Allowed" || messages[0].Sender == nil || messages[0].Sender.ID != senderID || messages[0].Sender.Name != senderName || messages[0].Sender.Username != "" {
		t.Fatalf("columns not preserved: %+v", messages)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CLOSED-TITLE-7f3a", "closed_user_7f3a", "-1000000000777", `"raw"`, `"_"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("legacy raw exposed %q: %s", forbidden, encoded)
		}
	}
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(path), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `UPDATE messages SET raw='{}' WHERE chat_id=42 AND id=1`); err != nil {
		t.Fatal(err)
	}
	baseline, err := d.localContext(ctx, rpc.ContextParams{Chat: "42", ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	withoutRaw, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, withoutRaw) {
		t.Fatalf("legacy column fields changed: %s != %s", encoded, withoutRaw)
	}
}

func TestRawLegacySenderAndEditedFallback(t *testing.T) {
	// This is main's previous behavior, unchanged by the plan; the owner's database has zero such rows.
	legacy := store.Message{ChatID: 42, ID: 1, Date: 1700000000, Text: "Allowed", Raw: json.RawMessage(`{"sender":{"username":"legacy_author"},"edited":true,"text":"CLOSED-TITLE-7f3a","message":"CLOSED-TITLE-7f3a"}`)}
	got := rpcStoredMessage(legacy)
	if !got.Edited || got.Sender == nil || got.Sender.Username != "legacy_author" || got.Text != "Allowed" {
		t.Fatalf("legacy fallback = %+v", got)
	}
	legacy.Raw = nil
	baseline := rpcStoredMessage(legacy)
	if baseline.Edited || baseline.Sender != nil {
		t.Fatalf("baseline = %+v", baseline)
	}
	got.Edited, got.Sender = baseline.Edited, baseline.Sender
	left, _ := json.Marshal(got)
	right, _ := json.Marshal(baseline)
	if !bytes.Equal(left, right) {
		t.Fatalf("other legacy fields differ: %s != %s", left, right)
	}
}

// TestRawStoreReadGuard prevents private store fields from entering new readers.
func TestRawStoreReadGuard(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo, Dir: "."}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("load daemon source: %v", err)
	}
	if len(pkgs[0].Errors) != 0 {
		t.Fatalf("load daemon source: %v", pkgs[0].Errors)
	}
	// voiceMessageMedia reads only the size and the duration of the media of a
	// stored message for the voice worker; nothing of the object leaves it.
	allowed := map[string]bool{"rpcStoredMessage": true, "convertStoredMessages": true, "voiceMessageMedia": true, "webMessage": true, "webThumb": true}
	guarded := map[string]bool{"Chat": true, "Person": true, "Message": true, "MessageVersion": true}
	for _, file := range pkgs[0].Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Raw" {
					return true
				}
				selection := pkgs[0].TypesInfo.Selections[sel]
				if selection == nil {
					return true
				}
				recv := types.Unalias(selection.Recv())
				if p, ok := recv.(*types.Pointer); ok {
					recv = types.Unalias(p.Elem())
				}
				named, ok := recv.(*types.Named)
				if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "tg-cli/internal/store" || !guarded[named.Obj().Name()] {
					return true
				}
				if !allowed[fn.Name.Name] {
					t.Errorf("%s: %s reads store.%s.Raw; update the plan's reader table", pkgs[0].Fset.Position(sel.Pos()), fn.Name.Name, named.Obj().Name())
				}
				return true
			})
		}
	}
}

func TestRawLocalReadersDoNotExposeTelegramObjects(t *testing.T) {
	d, fake, db, accessPath := accessTestDaemon(t)
	fake.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Allowed"}
	d.opts.Telegram = &transcriptionFake{fakeTelegram: fake}
	d.sync = newHistorySyncManager(context.Background(), d, newSyncTestClock())
	t.Cleanup(d.sync.close)
	writeAccess(t, accessPath, `{"block":[-1000000000777]}`)
	ctx := context.Background()
	marker := `{"_":"channel","id":777,"title":"CLOSED-TITLE-7f3a","username":"closed_user_7f3a"}`
	chatRaw := json.RawMessage(`{"channels.getFullChannel":{"_":"messages.chatFull","chats":[` + marker + `]}}`)
	personRaw := json.RawMessage(`{"users.getFullUser":{"_":"users.userFull","chats":[` + marker + `]}}`)
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "user", Title: "Allowed", Raw: chatRaw}}); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := db.UpsertPeople(ctx, []store.Person{{ID: 42, FirstName: strPtrRaw("Allowed"), IsContact: &yes, Raw: personRaw}}); err != nil {
		t.Fatal(err)
	}
	folderRaw := json.RawMessage(`{"_":"dialogFilter","include_peers":[{"_":"inputPeerChannel","channel_id":777}],"pinned_peers":[{"_":"inputPeerChannel","channel_id":777}]}`)
	if err := db.ReplaceFolders(ctx, []rpc.Folder{{ID: 2, Kind: "filter", Title: "Allowed", Raw: folderRaw}}, store.ChatFilter{Block: []int64{-1000000000777}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 42, ID: 1, Date: 1700000000, Text: "Allowed", Raw: json.RawMessage(`{"_":"message","fwd_from":{"_":"peerChannel","channel_id":777},"private":` + marker + `}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 42, Unread: 2, Raw: json.RawMessage(`{"_":"dialog","private":` + marker + `}`)}}, store.ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.AddEvent(ctx, store.Event{Type: rpc.EventReaction, Payload: json.RawMessage(`{"chat_id":42}`), Raw: json.RawMessage(`{"_":"updateMessageReactions","private":` + marker + `}`)}); err != nil {
		t.Fatal(err)
	}
	// Create versions whose old snapshots retain the marker.
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "user", Title: "Allowed changed"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 42, ID: 1, Date: 1700000000, Text: "Allowed changed", Raw: json.RawMessage(`{"_":"message","fwd_from":{"_":"peerChannel","channel_id":777},"private":` + marker + `}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutTranscription(ctx, store.Transcription{ChatID: 42, MessageID: 1, TranscriptionID: 9, Text: "Allowed", Raw: json.RawMessage(`{"messages.transcribeAudio":{"_":"messages.transcribedAudio","private":` + marker + `}}`)}); err != nil {
		t.Fatal(err)
	}
	connection, err := sql.Open("sqlite", filepath.Join(filepath.Dir(accessPath), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	methods := []struct {
		name, request string
		call          func(context.Context, json.RawMessage) (any, error)
	}{
		{"overview", `{}`, d.chatOverview}, {"state", `{"local":true}`, d.chatState}, {"changes", `{"chat":"42"}`, d.chatChanges},
		{"contacts", `{"local":true}`, d.contactList}, {"contact search", `{"local":true,"query":"Allowed"}`, d.contactSearch},
		{"search", `{"query":"CLOSED-TITLE-7f3a"}`, d.search}, {"folders", `{"local":true}`, d.folderList}, {"notes", `{}`, d.noteList},
		{"context", `{"chat":"42","id":1,"local":true}`, d.messageContext}, {"export", `{"chat":"42"}`, d.messageExportLocal},
		{"sessions", `{"local":true}`, d.accountSessions}, {"blocked", `{"local":true}`, d.accountBlocked}, {"notify", `{"local":true}`, d.accountNotify},
		{"transcribe", `{"chat":"42","id":1}`, d.messageTranscribe},
		{"history_sync.status", `{}`, d.historySyncStatus},
		{"replay", `{"after":"0.0.0.0"}`, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var p rpc.SubscribeParams
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			return d.replayEvents(ctx, p)
		}},
	}
	read := func() map[string][]byte {
		t.Helper()
		result := make(map[string][]byte)
		for _, tc := range methods {
			value, err := tc.call(ctx, json.RawMessage(tc.request))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"CLOSED-TITLE-7f3a", "closed_user_7f3a", "-1000000000777", `"raw"`, `"_"`} {
				if strings.Contains(string(data), forbidden) {
					t.Errorf("%s exposed %q: %s", tc.name, forbidden, data)
				}
			}
			if tc.name == "search" && len(value.(rpc.SearchResult).Hits) != 0 {
				t.Errorf("search returned raw: %s", data)
			}
			result[tc.name] = data
		}
		return result
	}
	before := read()
	for _, table := range []string{"chats", "people", "folders", "message_versions", "chat_versions", "dialog_state", "events", "transcriptions"} {
		if _, err := connection.ExecContext(ctx, "UPDATE "+table+" SET raw=NULL"); err != nil {
			t.Fatal(err)
		}
	}
	// messages.raw is NOT NULL, so an empty daemon JSON is the closest baseline.
	if _, err := connection.ExecContext(ctx, `UPDATE messages SET raw='{}'`); err != nil {
		t.Fatal(err)
	}
	after := read()
	for name, data := range before {
		if !bytes.Equal(data, after[name]) {
			t.Errorf("%s changed without raw: %s != %s", name, data, after[name])
		}
	}

}

func strPtrRaw(s string) *string { return &s }

func TestRawExportMonthDoesNotExposeTelegramObject(t *testing.T) {
	d, db, _, p := monthFixture(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"_":"message","fwd_from":{"_":"peerChannel","channel_id":777},"private":"CLOSED-TITLE-7f3a"}`)
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 42, ID: 1, Date: 1698796800, Text: "<test &>", Raw: raw}}); err != nil {
		t.Fatal(err)
	}
	response, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	archive := response.(rpc.ExportMonthResult)
	output, err := os.ReadFile(filepath.Join(archive.Dir, "42.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CLOSED-TITLE-7f3a", "closed_user_7f3a", "-1000000000777", `"raw"`, `"_"`} {
		if bytes.Contains(output, []byte(forbidden)) {
			t.Fatalf("archive exposed %q: %s", forbidden, output)
		}
	}
	result, err := d.messageExportVerify(ctx, params(t, rpc.ExportVerifyParams{Dir: archive.Dir}))
	if err != nil || !result.(rpc.ExportVerifyResult).OK {
		t.Fatalf("verify=%+v err=%v", result, err)
	}
}

type rawExposureTelegram struct{ *fakeTelegram }

func (f *rawExposureTelegram) UnreadMessages(_ context.Context, _ chatref.Ref, _ rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error) {
	return rpc.UnreadMessagesResult{ChatID: -10, Messages: []rpc.Message{}}, nil, nil
}

func TestRawRemoteReadersDoNotExposeStoreObjects(t *testing.T) {
	d, fake, db, path := accessTestDaemon(t)
	writeAccess(t, path, `{"block":[-1000000000777]}`)
	ctx := context.Background()
	raw := json.RawMessage(`{"channels.getFullChannel":{"_":"messages.chatFull","chats":[{"_":"channel","id":777,"title":"CLOSED-TITLE-7f3a","username":"closed_user_7f3a"}]}}`)
	fake.resolved = &rpc.Chat{ID: -10, Type: "group", Title: "Allowed"}
	fake.chats = []rpc.Chat{*fake.resolved}
	fake.info = &rpc.ChatInfo{ID: -10, Type: "group", Title: "Allowed"}
	d.opts.Telegram = &rawExposureTelegram{fake}
	if err := db.UpsertChats(ctx, []store.Chat{{ID: -10, Type: "group", Title: "Allowed", Raw: raw}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []store.Person{{ID: 5, FirstName: strPtrRaw("Allowed"), Raw: raw}}); err != nil {
		t.Fatal(err)
	}
	methods := []struct {
		name, request string
		call          func(context.Context, json.RawMessage) (any, error)
	}{
		{"chat.list", `{}`, d.chatList}, {"chat.info", `{"chat":"-10"}`, d.chatInfo}, {"chat.resolve", `{"chat":"-10"}`, d.chatResolve},
		{"chat_full.status", `{}`, d.chatFullStatus},
		{"chat.unread", `{}`, d.chatUnread}, {"message.unread", `{"chat":"-10"}`, d.messageUnread}, {"message.readers", `{"chat":"-10","id":1}`, d.messageReaders},
	}
	for _, tc := range methods {
		t.Run(tc.name, func(t *testing.T) {
			value, err := tc.call(ctx, json.RawMessage(tc.request))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"CLOSED-TITLE-7f3a", "closed_user_7f3a", "-1000000000777", `"raw"`, `"_"`} {
				if bytes.Contains(encoded, []byte(forbidden)) {
					t.Fatalf("exposed %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestRawEditedDateIsOnlyLocalReadDifference(t *testing.T) {
	d, _, db, path := accessTestDaemon(t)
	writeAccess(t, path, `{"block":[-1000000000777]}`)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "user", Title: "Allowed"}}); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"_":"message","edit_date":1,"fwd_from":{"_":"peerChannel","channel_id":777,"title":"CLOSED-TITLE-7f3a"}}`)
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 42, ID: 1, Date: 1700000000, Text: "Allowed", Raw: raw}}); err != nil {
		t.Fatal(err)
	}
	before, err := d.localContext(ctx, rpc.ContextParams{Chat: "42", ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	a := before.(rpc.MessagesResult).Messages
	if len(a) != 1 || !a[0].Edited {
		t.Fatalf("edited message=%+v", a)
	}
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(path), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `UPDATE messages SET raw='{}' WHERE chat_id=42 AND id=1`); err != nil {
		t.Fatal(err)
	}
	after, err := d.localContext(ctx, rpc.ContextParams{Chat: "42", ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	b := after.(rpc.MessagesResult).Messages
	if len(b) != 1 || b[0].Edited {
		t.Fatalf("baseline=%+v", b)
	}
	a[0].Edited = false
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	if !bytes.Equal(left, right) {
		t.Fatalf("other fields changed: %s != %s", left, right)
	}
}
