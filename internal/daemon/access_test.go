package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"tg-cli/internal/access"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func accessTestDaemon(t *testing.T) (*daemon, *fakeTelegram, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tg := newFake()
	close(tg.ready)
	tg.user = &rpc.User{ID: 42}
	path := filepath.Join(dir, "access.json")
	logger := slog.New(slog.DiscardHandler)
	d := &daemon{opts: Options{Telegram: tg, Access: access.Open(path), Paths: Paths{Dir: dir, Deletions: filepath.Join(dir, "deletions.jsonl")}}, store: db, log: logger, srv: rpc.NewServer(logger), hub: rpc.NewHub()}
	return d, tg, db, path
}

func writeAccess(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func params(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sameRPCError(t *testing.T, got, want error) {
	t.Helper()
	var a, b *rpc.Error
	if !errors.As(got, &a) || !errors.As(want, &b) {
		t.Fatalf("errors: got %v, want %v", got, want)
	}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	if string(left) != string(right) {
		t.Errorf("response errors differ: %s vs %s", left, right)
	}
}

func TestRegisteredMethodsHaveAccessClass(t *testing.T) {
	d, _, _, _ := accessTestDaemon(t)
	d.register()
	d.srv.SetEventSource(d.hub, d.replayEvents)
	classes := map[string]string{
		rpc.MethodUserProfile: "user-ref", rpc.MethodUserBlock: "user-ref", rpc.MethodUserUnblock: "user-ref", rpc.MethodContactAdd: "user-ref", rpc.MethodContactDelete: "user-ref", rpc.MethodContactList: "chat-search", rpc.MethodContactSearch: "chat-search",
		rpc.MethodPing: "none", rpc.MethodStatus: "none", rpc.MethodShutdown: "none",
		rpc.MethodAuthStart: "none", rpc.MethodAuthCode: "none", rpc.MethodAuthPassword: "none", rpc.MethodAuthResend: "none", rpc.MethodAuthStartQR: "none", rpc.MethodAuthWait: "none", rpc.MethodAuthCancel: "none",
		rpc.MethodChatList: "chat-list", rpc.MethodChatResolve: "chat-ref", rpc.MethodChatInfo: "chat-ref", rpc.MethodChatInviteInfo: "chat-ref",
		rpc.MethodChatDraftGet: "chat-ref", rpc.MethodChatDraftList: "chat-search", rpc.MethodChatDraftSet: "chat-write", rpc.MethodChatDraftClear: "chat-write",
		rpc.MethodChatMembers: "chat-ref", rpc.MethodChatTopics: "chat-ref", rpc.MethodChatCommon: "chat-search",
		rpc.MethodChatJoin: "chat-write", rpc.MethodChatLeave: "chat-write", rpc.MethodChatCreate: "chat-create", rpc.MethodChatEdit: "chat-admin", rpc.MethodChatKick: "chat-admin", rpc.MethodChatBan: "chat-admin", rpc.MethodChatUnban: "chat-admin", rpc.MethodChatRestrict: "chat-admin", rpc.MethodChatPermissions: "chat-admin", rpc.MethodChatSlowMode: "chat-admin",
		rpc.MethodMessageHistory: "chat-ref", rpc.MethodMessageGet: "chat-ref", rpc.MethodMessagePinned: "chat-ref",
		rpc.MethodMessageScheduledList: "chat-ref", rpc.MethodMessageScheduledDelete: "chat-write", rpc.MethodMessageScheduledSendNow: "chat-write",
		rpc.MethodMessageSend: "chat-write", rpc.MethodMessageSendMedia: "chat-write", rpc.MethodMessageEdit: "chat-write", rpc.MethodMessageDelete: "chat-write", rpc.MethodMessageForward: "chat-write",
		rpc.MethodMessageReact: "chat-write", rpc.MethodMessagePin: "chat-write", rpc.MethodMessageUnpin: "chat-write", rpc.MethodMessagePoll: "chat-write",
		rpc.MethodMessageSearch: "chat-ref", rpc.MethodMessageContext: "chat-ref", rpc.MethodMessageExportLocal: "chat-ref", rpc.MethodMessageComments: "chat-ref", rpc.MethodMessageLink: "chat-ref", rpc.MethodMessageSearchPublic: "chat-search", rpc.MethodChatMarkRead: "chat-write", rpc.MethodChatTyping: "chat-write",
		rpc.MethodNoteAdd: "chat-notes", rpc.MethodNoteList: "chat-notes", rpc.MethodNoteDelete: "chat-notes", rpc.MethodNoteDeleteByAuthor: "none", rpc.MethodSearch: "chat-search",
		rpc.MethodHistorySyncStart: "chat-ref", rpc.MethodHistorySyncStatus: "chat-search", rpc.MethodHistorySyncCancel: "chat-ref",
		rpc.MethodSubscribe: "chat-stream", rpc.MethodUnsubscribe: "none",
	}
	methods := d.srv.Methods()
	if len(methods) != len(classes) {
		t.Errorf("registered %d methods, classified %d", len(methods), len(classes))
	}
	for _, method := range methods {
		if classes[method] == "" {
			t.Errorf("unclassified method %s", method)
		}
	}
}

func TestChatAccessRejectsDirectReferencesAndPreservesMissingError(t *testing.T) {
	d, tg, db, path := accessTestDaemon(t)
	ctx := context.Background()
	writeAccess(t, path, `{"allow":[42],"block":[77]}`)
	tg.resolved = &rpc.Chat{ID: 77, Type: "group", Title: "Private"}
	// A closed numeric id answers like an id the session does not know; other
	// references answer like a missing chat.
	checks := []struct {
		name string
		want *rpc.Error
		run  func() error
	}{
		{"resolve ID", rpc.ErrUnknownChatID(), func() error { _, err := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "77"})); return err }},
		{"resolve username", rpc.ErrChatNotFound(), func() error {
			_, err := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "@privatechat"}))
			return err
		}},
		{"info", rpc.ErrUnknownChatID(), func() error { _, err := d.chatInfo(ctx, params(t, rpc.ChatRefParams{Chat: "77"})); return err }},
		{"invite", rpc.ErrChatNotFound(), func() error {
			_, err := d.chatInviteInfo(ctx, params(t, rpc.ChatRefParams{Chat: "https://t.me/+AbCdEfGh1234"}))
			return err
		}},
		{"history", rpc.ErrUnknownChatID(), func() error { _, err := d.messageHistory(ctx, params(t, rpc.HistoryParams{Chat: "77"})); return err }},
		{"message", rpc.ErrUnknownChatID(), func() error {
			_, err := d.messageGet(ctx, params(t, rpc.MessageGetParams{Chat: "77", ID: 1}))
			return err
		}},
		{"pinned", rpc.ErrUnknownChatID(), func() error { _, err := d.messagePinned(ctx, params(t, rpc.PinnedParams{Chat: "77"})); return err }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) { sameRPCError(t, tc.run(), tc.want) })
	}
	if tg.resolveCalls != 1 {
		t.Errorf("Resolve calls = %d, want only the username lookup", tg.resolveCalls)
	}
	if _, err := db.GetChat(ctx, 77); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("closed chat was saved: %v", err)
	}
	// The fake now reports an actually missing chat with the same wire error.
	tg.missing = true
	_, err := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "@missingchat"}))
	sameRPCError(t, err, rpc.ErrChatNotFound())
}

func TestChatListBackfillsAndPersistsOnlyVisibleChats(t *testing.T) {
	d, tg, db, path := accessTestDaemon(t)
	ctx := context.Background()
	tg.chats = []rpc.Chat{{ID: 1, Type: "group", Title: "Hidden"}, {ID: 2, Type: "group", Title: "Visible"}, {ID: 3, Type: "group", Title: "Also visible"}}
	writeAccess(t, path, `{"block":[1]}`)
	result, err := d.chatList(ctx, params(t, rpc.ChatListParams{Limit: 1}))
	if err != nil {
		t.Fatal(err)
	}
	chats := result.(rpc.ChatListResult).Chats
	if len(chats) != 1 || chats[0].ID != 2 || tg.chatsCalls != 2 {
		t.Errorf("chats=%+v calls=%d", chats, tg.chatsCalls)
	}
	if _, err := db.GetChat(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("hidden chat stored: %v", err)
	}
	if _, err := db.GetChat(ctx, 2); err != nil {
		t.Errorf("visible chat not stored: %v", err)
	}
}

func TestChatAccessFiltersStoredSearchAndNotes(t *testing.T) {
	d, _, db, path := accessTestDaemon(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 1, Type: "group", Title: "Secret tree"}, {ID: 2, Type: "group", Title: "Public tree"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 1, ID: 11, Date: 1, Text: "Secret tree", Raw: []byte(`{}`)}, {ChatID: 2, ID: 22, Date: 2, Text: "Public tree", Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 3, ID: 33, Date: 3, Text: "orphan", Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	hidden, err := db.AddNote(ctx, store.Note{Kind: "chat", ChatID: accessInt(1), Body: "Secret tree", Author: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddNote(ctx, store.Note{Kind: "chat", ChatID: accessInt(2), Body: "Public tree", Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	writeAccess(t, path, `{"block":[1]}`)
	search, err := d.search(ctx, params(t, rpc.SearchParams{Query: "tree"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range search.(rpc.SearchResult).Hits {
		if hit.Scope == "chats" && hit.ID == 1 || hit.ChatID != nil && *hit.ChatID == 1 {
			t.Errorf("hidden search hit: %+v", hit)
		}
	}
	list, err := d.noteList(ctx, params(t, rpc.NoteListParams{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range list.(rpc.NoteListResult).Notes {
		if note.ChatID != nil && *note.ChatID == 1 {
			t.Errorf("hidden note: %+v", note)
		}
	}
	for _, chat := range []string{"1", "999", "@unknownchat"} {
		result, err := d.search(ctx, params(t, rpc.SearchParams{Query: "tree", Chat: chat}))
		if err != nil || len(result.(rpc.SearchResult).Hits) != 0 {
			t.Errorf("search %s = %+v, %v", chat, result, err)
		}
		result, err = d.noteList(ctx, params(t, rpc.NoteListParams{Chat: chat}))
		if err != nil || len(result.(rpc.NoteListResult).Notes) != 0 {
			t.Errorf("notes %s = %+v, %v", chat, result, err)
		}
	}
	orphan, err := d.search(ctx, params(t, rpc.SearchParams{Query: "orphan", Chat: "3", Scopes: []string{"messages"}}))
	if err != nil || len(orphan.(rpc.SearchResult).Hits) != 1 {
		t.Errorf("message-only chat = %+v, %v", orphan, err)
	}
	_, err = d.noteAdd(ctx, params(t, rpc.NoteAddParams{Kind: "chat", Chat: "1", Body: "new", Author: "owner"}))
	sameRPCError(t, err, rpc.ErrChatNotFound())
	_, err = d.noteAdd(ctx, params(t, rpc.NoteAddParams{Kind: "message", Chat: "1", MessageID: 11, Body: "new", Author: "owner"}))
	sameRPCError(t, err, rpc.ErrChatNotFound())
	_, err = d.noteDelete(ctx, params(t, rpc.NoteDeleteParams{ID: hidden.ID}))
	_, missing := d.noteDelete(ctx, params(t, rpc.NoteDeleteParams{ID: 99999}))
	sameRPCError(t, err, missing)
}

func TestAccessFileRecoveryAndNoFileCompatibility(t *testing.T) {
	d, tg, _, path := accessTestDaemon(t)
	ctx := context.Background()
	tg.chats = []rpc.Chat{{ID: 77, Type: "group", Title: "Team"}}
	tg.resolved = &tg.chats[0]
	for i := 0; i < 2; i++ {
		if _, err := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "@teamchat"})); err != nil {
			t.Fatal(err)
		}
	}
	if tg.resolveCalls != 2 {
		t.Errorf("without access file Resolve calls = %d", tg.resolveCalls)
	}
	if _, err := d.chatList(ctx, params(t, rpc.ChatListParams{Limit: 1})); err != nil {
		t.Fatal(err)
	}
	if tg.chatsCalls != 1 {
		t.Errorf("without access file Chats calls = %d", tg.chatsCalls)
	}
	if _, err := d.noteAdd(ctx, params(t, rpc.NoteAddParams{Kind: "chat", Chat: "77", Body: "Team note", Author: "owner"})); err != nil {
		t.Fatal(err)
	}
	writeAccess(t, path, `{"allow":["secret"]}`)
	for _, tc := range []struct {
		want *rpc.Error
		run  func() error
	}{
		{rpc.ErrUnknownChatID(), func() error { _, err := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "77"})); return err }},
		{rpc.ErrUnknownChatID(), func() error { _, err := d.chatInfo(ctx, params(t, rpc.ChatRefParams{Chat: "77"})); return err }},
		{rpc.ErrChatNotFound(), func() error {
			_, err := d.chatInviteInfo(ctx, params(t, rpc.ChatRefParams{Chat: "https://t.me/+AbCdEfGh1234"}))
			return err
		}},
		{rpc.ErrUnknownChatID(), func() error { _, err := d.messageHistory(ctx, params(t, rpc.HistoryParams{Chat: "77"})); return err }},
		{rpc.ErrUnknownChatID(), func() error {
			_, err := d.messageGet(ctx, params(t, rpc.MessageGetParams{Chat: "77", ID: 1}))
			return err
		}},
		{rpc.ErrUnknownChatID(), func() error { _, err := d.messagePinned(ctx, params(t, rpc.PinnedParams{Chat: "77"})); return err }},
		{rpc.ErrChatNotFound(), func() error {
			_, err := d.noteAdd(ctx, params(t, rpc.NoteAddParams{Kind: "chat", Chat: "77", Body: "new"}))
			return err
		}},
	} {
		sameRPCError(t, tc.run(), tc.want)
	}
	list, err := d.chatList(ctx, params(t, rpc.ChatListParams{Limit: 1}))
	if err != nil || len(list.(rpc.ChatListResult).Chats) != 0 {
		t.Errorf("invalid policy list = %+v, %v", list, err)
	}
	search, err := d.search(ctx, params(t, rpc.SearchParams{Query: "Team"}))
	if err != nil || len(search.(rpc.SearchResult).Hits) != 0 {
		t.Errorf("invalid policy search = %+v, %v", search, err)
	}
	notes, err := d.noteList(ctx, params(t, rpc.NoteListParams{}))
	if err != nil || len(notes.(rpc.NoteListResult).Notes) != 0 {
		t.Errorf("invalid policy notes = %+v, %v", notes, err)
	}
	writeAccess(t, path, `{"allow":[77]}`)
	chat, err := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "77"}))
	if err != nil {
		t.Fatal(err)
	}
	if chat.(rpc.Chat).ID != 77 {
		t.Errorf("recovered = %+v", chat)
	}
}

func accessInt(v int64) *int64 { return &v }
