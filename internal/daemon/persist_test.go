package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func authorizeFake(tg *fakeTelegram) {
	tg.mu.Lock()
	tg.user = &rpc.User{ID: 1}
	tg.mu.Unlock()
	close(tg.ready)
}

func TestSaveDetachesRequestCancellation(t *testing.T) {
	d := &daemon{log: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	d.save(ctx, rpc.MethodChatList, func(saveCtx context.Context) error {
		called = true
		if err := saveCtx.Err(); err != nil {
			t.Errorf("save context was canceled: %v", err)
		}
		deadline, ok := saveCtx.Deadline()
		if !ok || time.Until(deadline) > saveTimeout || time.Until(deadline) < saveTimeout-time.Second {
			t.Errorf("save deadline = %v, present=%t", deadline, ok)
		}
		return nil
	})
	if !called {
		t.Fatal("save callback was not called")
	}
}

func TestChatAnswersArePersistedWithoutLosingDetails(t *testing.T) {
	client, db, tg, _ := noteTestRPC(t)
	authorizeFake(tg)
	tg.mu.Lock()
	tg.chats = []rpc.Chat{
		{ID: 42, Type: "user", Title: "Ёлка Person", Username: "TreeUser", Unread: 3},
		{ID: 43, Type: "bot", Title: "Helper Bot", Username: "helperbot", Unread: 0},
		{ID: -10, Type: "group", Title: "Green Group", Unread: 7},
	}
	tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Ёлка Person", Username: "TreeUser"}
	tg.info = &rpc.ChatInfo{ID: 42, Type: "user", Title: "Ёлка Person", Username: "TreeUser", About: "Зелёный лес", Members: 11, Forum: true, Verified: true}
	wantChats := append([]rpc.Chat(nil), tg.chats...)
	tg.mu.Unlock()
	var listed rpc.ChatListResult
	if err := noteCall(t, client, rpc.MethodChatList, rpc.ChatListParams{}, &listed); err != nil || !reflect.DeepEqual(listed.Chats, wantChats) {
		t.Fatalf("chat.list changed response: %+v, %v", listed, err)
	}
	ctx := context.Background()
	chat, err := db.GetChat(ctx, 42)
	if err != nil || chat.Unread == nil || *chat.Unread != 3 || chat.Type != "user" || chat.Username == nil || *chat.Username != "TreeUser" {
		t.Fatalf("stored user chat = %+v, %v", chat, err)
	}
	bot, err := db.GetChat(ctx, 43)
	if err != nil || bot.Unread == nil || *bot.Unread != 0 {
		t.Fatalf("stored bot chat = %+v, %v", bot, err)
	}
	person, err := db.GetPerson(ctx, 42)
	if err != nil || person.FirstName == nil || *person.FirstName != "Ёлка Person" || person.IsBot == nil || *person.IsBot {
		t.Fatalf("stored person = %+v, %v", person, err)
	}
	botPerson, err := db.GetPerson(ctx, 43)
	if err != nil || botPerson.IsBot == nil || !*botPerson.IsBot {
		t.Fatalf("stored bot person = %+v, %v", botPerson, err)
	}
	if _, err := db.GetPerson(ctx, -10); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("group was stored as a person: %v", err)
	}
	if err := noteCall(t, client, rpc.MethodChatList, rpc.ChatListParams{}, &listed); err != nil {
		t.Fatal(err)
	}
	if hits, err := db.Search(ctx, store.SearchOptions{Query: "ЁЛКА", Scopes: []string{"chats"}}); err != nil || len(hits) != 1 {
		t.Fatalf("duplicate chat or missing FTS: %+v, %v", hits, err)
	}
	var resolved rpc.Chat
	if err := noteCall(t, client, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: "@treeuser"}, &resolved); err != nil || resolved.ID != 42 {
		t.Fatalf("chat.resolve = %+v, %v", resolved, err)
	}
	chat, err = db.GetChat(ctx, 42)
	if err != nil || chat.Unread == nil || *chat.Unread != 3 {
		t.Fatalf("resolve erased unread: %+v, %v", chat, err)
	}
	tg.mu.Lock()
	tg.resolved = &rpc.Chat{ID: 44, Type: "bot", Title: "Resolved Bot", Username: "resolvedbot"}
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: "@resolvedbot"}, &resolved); err != nil {
		t.Fatal(err)
	}
	resolvedPerson, err := db.GetPerson(ctx, 44)
	if err != nil || resolvedPerson.IsBot == nil || !*resolvedPerson.IsBot {
		t.Fatalf("resolved bot person = %+v, %v", resolvedPerson, err)
	}
	var info rpc.ChatInfo
	if err := noteCall(t, client, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: "@treeuser"}, &info); err != nil || info.About != "Зелёный лес" {
		t.Fatalf("chat.info = %+v, %v", info, err)
	}
	chat, err = db.GetChat(ctx, 42)
	if err != nil || chat.About == nil || *chat.About != info.About || chat.Members == nil || *chat.Members != 11 || chat.Forum == nil || !*chat.Forum || chat.Verified == nil || !*chat.Verified || *chat.Unread != 3 {
		t.Fatalf("info not merged: %+v, %v", chat, err)
	}
	tg.mu.Lock()
	tg.info.About = ""
	tg.info.Members = 0
	tg.info.Forum = false
	tg.info.Verified = false
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: "@treeuser"}, &info); err != nil {
		t.Fatal(err)
	}
	chat, err = db.GetChat(ctx, 42)
	if err != nil || chat.About == nil || *chat.About != "Зелёный лес" || chat.Members == nil || *chat.Members != 11 || chat.Forum == nil || *chat.Forum || chat.Verified == nil || *chat.Verified {
		// The member count 0 means "unknown" and keeps the stored 11; forum and verified are real booleans.
		t.Fatalf("empty info erased details: %+v, %v", chat, err)
	}
	if err := noteCall(t, client, rpc.MethodChatList, rpc.ChatListParams{}, &listed); err != nil {
		t.Fatal(err)
	}
	chat, err = db.GetChat(ctx, 42)
	if err != nil || chat.About == nil || *chat.About != "Зелёный лес" || chat.Members == nil || *chat.Members != 11 {
		t.Fatalf("list erased info: %+v, %v", chat, err)
	}
}

func TestMessageAnswersPersistVersionsSendersAndSearch(t *testing.T) {
	client, db, tg, logs := noteTestRPC(t)
	authorizeFake(tg)
	ctx := context.Background()
	var noteIDs []int64
	if note, err := db.AddNote(ctx, store.Note{Kind: "chat", ChatID: persistInt64(-10), Body: "Keep note", Author: "owner", Tags: []string{"stable"}}); err != nil {
		t.Fatal(err)
	} else {
		noteIDs = append(noteIDs, note.ID)
	}
	if note, err := db.AddNote(ctx, store.Note{Kind: "person", TargetID: persistInt64(42), Body: "Keep person", Author: "owner", Tags: []string{"stable"}}); err != nil {
		t.Fatal(err)
	} else {
		noteIDs = append(noteIDs, note.ID)
	}
	if note, err := db.AddNote(ctx, store.Note{Kind: "message", ChatID: persistInt64(-10), TargetID: persistInt64(1), Body: "Keep message", Author: "owner", Tags: []string{"stable"}}); err != nil {
		t.Fatal(err)
	} else {
		noteIDs = append(noteIDs, note.ID)
	}
	tg.mu.Lock()
	tg.chats = []rpc.Chat{{ID: -10, Type: "group", Title: "Ёлка Group"}}
	tg.resolved = &rpc.Chat{ID: -10, Type: "group", Title: "Ёлка Group"}
	tg.info = &rpc.ChatInfo{ID: -10, Type: "group", Title: "Ёлка Group", About: "Green"}
	tg.messages = []rpc.Message{
		{ID: 1, ChatID: -10, Date: "2026-11-13T10:00:00Z", Sender: &rpc.Sender{ID: 42, Name: "Alice", Username: "alice"}, Text: "Ёлка first", Out: false, Pinned: true, ReplyTo: 4, TopicID: 5, MediaType: "photo", Edited: true},
		{ID: 2, ChatID: -10, Date: "2026-11-13T10:01:00Z", Sender: &rpc.Sender{ID: 42, Name: "Alice"}, Text: "Second message"},
		{ID: 3, ChatID: -10, Date: "2026-11-13T10:02:00Z", Sender: &rpc.Sender{ID: -1000000000099, Name: "Channel"}, Text: "Channel post"},
		{ID: 4, ChatID: -10, Date: "bad date", Sender: &rpc.Sender{ID: 99, Name: "Skipped"}, Text: "Skipped message"},
	}
	wantHistory := append([]rpc.Message(nil), tg.messages...)
	tg.mu.Unlock()
	var chats rpc.ChatListResult
	if err := noteCall(t, client, rpc.MethodChatList, rpc.ChatListParams{}, &chats); err != nil {
		t.Fatal(err)
	}
	if err := noteCall(t, client, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: "-10"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := noteCall(t, client, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: "-10"}, nil); err != nil {
		t.Fatal(err)
	}
	var history rpc.MessagesResult
	if err := noteCall(t, client, rpc.MethodMessageHistory, rpc.HistoryParams{Chat: "-10"}, &history); err != nil || !reflect.DeepEqual(history.Messages, wantHistory) {
		t.Fatalf("history changed response: %+v, %v", history, err)
	}
	first, err := db.GetMessage(ctx, -10, 1)
	firstDate, _ := time.Parse(time.RFC3339, wantHistory[0].Date)
	if err != nil || first.Text != "Ёлка first" || first.Date != firstDate.Unix() || first.Out == nil || *first.Out || first.Pinned == nil || !*first.Pinned || first.ReplyTo == nil || *first.ReplyTo != 4 || first.TopicID == nil || *first.TopicID != 5 || first.MediaType == nil || *first.MediaType != "photo" || first.EditedAt != nil {
		t.Fatalf("stored first message = %+v, %v", first, err)
	}
	wantRaw, _ := json.Marshal(wantHistory[0])
	if !bytes.Equal(first.Raw, wantRaw) {
		t.Errorf("raw message = %s, want %s", first.Raw, wantRaw)
	}
	if first.SenderID == nil || *first.SenderID != 42 || first.SenderName == nil || *first.SenderName != "Alice" {
		t.Errorf("sender fields = %+v", first)
	}
	person, err := db.GetPerson(ctx, 42)
	if err != nil || person.FirstName == nil || *person.FirstName != "Alice" || person.Username == nil || *person.Username != "alice" {
		t.Fatalf("stored sender = %+v, %v", person, err)
	}
	if _, err := db.GetPerson(ctx, -1000000000099); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("channel sender stored as person: %v", err)
	}
	if _, err := db.GetPerson(ctx, 99); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("sender of skipped message stored: %v", err)
	}
	if _, err := db.GetMessage(ctx, -10, 4); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("bad date message stored: %v", err)
	}
	if !strings.Contains(logs.String(), "count=1") || strings.Contains(logs.String(), "Skipped message") {
		t.Errorf("skip log = %s", logs.String())
	}
	var search rpc.SearchResult
	if err := noteCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: "ЕЛКА", Scopes: []string{"chats", "messages"}}, &search); err != nil || len(search.Hits) != 2 || search.Hits[0].Scope != "chats" || search.Hits[1].Scope != "messages" {
		t.Fatalf("offline search = %+v, %v", search, err)
	}
	tg.mu.Lock()
	tg.messages[0].Text = "Елка edited"
	tg.messages[0].Pinned = false
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodMessageHistory, rpc.HistoryParams{Chat: "-10"}, &history); err != nil {
		t.Fatal(err)
	}
	changed, err := db.GetMessage(ctx, -10, 1)
	if err != nil || changed.Text != "Елка edited" || changed.FirstSeenAt != first.FirstSeenAt || changed.Pinned == nil || *changed.Pinned {
		t.Fatalf("changed message = %+v, %v", changed, err)
	}
	versions, err := db.MessageVersions(ctx, -10, 1)
	if err != nil || len(versions) != 1 || versions[0].Text != "Ёлка first" {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	tg.mu.Lock()
	tg.pinned = []rpc.Message{{ID: 1, ChatID: -10, Date: "2026-11-13T10:00:00Z", Text: "Елка edited", Pinned: true}}
	tg.mu.Unlock()
	var pinned rpc.MessagesResult
	if err := noteCall(t, client, rpc.MethodMessagePinned, rpc.PinnedParams{Chat: "-10"}, &pinned); err != nil || len(pinned.Messages) != 1 {
		t.Fatalf("pinned = %+v, %v", pinned, err)
	}
	storedPinned, err := db.GetMessage(ctx, -10, 1)
	if err != nil || storedPinned.Pinned == nil || !*storedPinned.Pinned {
		t.Fatalf("pinned true not stored: %+v, %v", storedPinned, err)
	}
	tg.mu.Lock()
	tg.pinned[0].Pinned = false
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodMessagePinned, rpc.PinnedParams{Chat: "-10"}, &pinned); err != nil {
		t.Fatal(err)
	}
	storedPinned, err = db.GetMessage(ctx, -10, 1)
	if err != nil || storedPinned.Pinned == nil || *storedPinned.Pinned {
		t.Fatalf("pinned false not stored: %+v, %v", storedPinned, err)
	}
	tg.mu.Lock()
	tg.messages = append(tg.messages, rpc.Message{ID: 5, ChatID: -10, Date: "2026-11-13T10:05:00Z", Text: "Get result"})
	tg.pinned = []rpc.Message{{ID: 6, ChatID: -10, Date: "2026-11-13T10:06:00Z", Text: "Pinned result", Pinned: true}}
	tg.mu.Unlock()
	var got rpc.Message
	if err := noteCall(t, client, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: "-10", ID: 5}, &got); err != nil || got.ID != 5 {
		t.Fatalf("message.get = %+v, %v", got, err)
	}
	if err := noteCall(t, client, rpc.MethodMessagePinned, rpc.PinnedParams{Chat: "-10"}, &pinned); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMessage(ctx, -10, 5); err != nil {
		t.Fatal("message.get did not persist:", err)
	}
	if _, err := db.GetMessage(ctx, -10, 6); err != nil {
		t.Fatal("message.pinned did not persist:", err)
	}
	for _, id := range noteIDs {
		note, err := db.GetNote(ctx, id)
		if err != nil || len(note.Tags) != 1 || note.Tags[0] != "stable" || !strings.HasPrefix(note.Body, "Keep ") {
			t.Fatalf("note %d changed: %+v, %v", id, note, err)
		}
	}
	noteHits, err := db.Search(ctx, store.SearchOptions{Query: "stable", Scopes: []string{"notes"}})
	if err != nil || len(noteHits) != 3 {
		t.Fatalf("notes FTS changed: %+v, %v", noteHits, err)
	}
}
func persistInt64(v int64) *int64 { return &v }

func TestClosedStoreDoesNotChangeReadResponsesOrLeakData(t *testing.T) {
	client, db, tg, logs := noteTestRPC(t)
	authorizeFake(tg)
	marker := "DISTINCT_PRIVATE_MARKER"
	tg.mu.Lock()
	tg.chats = []rpc.Chat{{ID: 42, Type: "user", Title: marker, Username: "markeruser"}}
	tg.resolved = &rpc.Chat{ID: 77, Type: "group", Title: marker}
	tg.info = &rpc.ChatInfo{ID: 77, Type: "group", Title: marker, About: marker}
	tg.messages = []rpc.Message{{ID: 1, ChatID: 77, Date: "2026-11-13T10:00:00Z", Sender: &rpc.Sender{ID: 42, Name: marker}, Text: marker}}
	tg.pinned = []rpc.Message{{ID: 2, ChatID: 77, Date: "2026-11-13T10:00:00Z", Text: marker, Pinned: true}}
	tg.mu.Unlock()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var list rpc.ChatListResult
	if err := noteCall(t, client, rpc.MethodChatList, rpc.ChatListParams{}, &list); err != nil || len(list.Chats) != 1 || list.Chats[0].Title != marker {
		t.Fatalf("chat.list = %+v, %v", list, err)
	}
	var chat rpc.Chat
	if err := noteCall(t, client, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: "@markeruser"}, &chat); err != nil || !reflect.DeepEqual(chat, *tg.resolved) {
		t.Fatalf("chat.resolve = %+v, %v", chat, err)
	}
	var info rpc.ChatInfo
	if err := noteCall(t, client, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: "@markeruser"}, &info); err != nil || !reflect.DeepEqual(info, *tg.info) {
		t.Fatalf("chat.info = %+v, %v", info, err)
	}
	var history rpc.MessagesResult
	if err := noteCall(t, client, rpc.MethodMessageHistory, rpc.HistoryParams{Chat: "77"}, &history); err != nil || len(history.Messages) != 1 || history.Messages[0].Text != marker {
		t.Fatalf("message.history = %+v, %v", history, err)
	}
	var message rpc.Message
	if err := noteCall(t, client, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: "77", ID: 1}, &message); err != nil || !reflect.DeepEqual(message, tg.messages[0]) {
		t.Fatalf("message.get = %+v, %v", message, err)
	}
	history = rpc.MessagesResult{}
	if err := noteCall(t, client, rpc.MethodMessagePinned, rpc.PinnedParams{Chat: "77"}, &history); err != nil || !reflect.DeepEqual(history.Messages, tg.pinned) {
		t.Fatalf("message.pinned = %+v, %v", history, err)
	}
	for _, op := range []string{rpc.MethodChatList, rpc.MethodChatResolve, rpc.MethodChatInfo, rpc.MethodMessageHistory, rpc.MethodMessageGet, rpc.MethodMessagePinned} {
		if !strings.Contains(logs.String(), "op="+op) {
			t.Errorf("no warning for %s: %s", op, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "err=") {
		t.Errorf("storage warning lacks error: %s", logs.String())
	}
	if strings.Contains(logs.String(), marker) {
		t.Errorf("private answer data in log: %s", logs.String())
	}
}

func TestChatInfoWithoutMemberCountKeepsTheStoredOne(t *testing.T) {
	client, db, tg, _ := noteTestRPC(t)
	authorizeFake(tg)
	tg.mu.Lock()
	tg.info = &rpc.ChatInfo{ID: -1001234567890, Type: "channel", Title: "News", Members: 12}
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: "-1001234567890"}, nil); err != nil {
		t.Fatal(err)
	}
	// A later answer that does not know the size (it is left out of the JSON) must not reset it.
	tg.mu.Lock()
	tg.info = &rpc.ChatInfo{ID: -1001234567890, Type: "channel", Title: "News"}
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: "-1001234567890"}, nil); err != nil {
		t.Fatal(err)
	}
	chat, err := db.GetChat(context.Background(), -1001234567890)
	if err != nil || chat.Members == nil || *chat.Members != 12 {
		t.Fatalf("stored members = %+v, %v", chat.Members, err)
	}
}

// TestSaveChatsKeepsKnownFlags covers the rule that a partial save must not clear a
// flag a fuller read stored: `chat list` and `chat resolve` save chats without a
// channel object of their own, and `chat info` writes the flags.
func TestSaveChatsKeepsKnownFlags(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := &daemon{store: db, log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()

	// A full read knows the flags, including the false ones.
	d.saveChats(ctx, rpc.MethodChatInfo, []rpc.Chat{{ID: 5, Type: "channel", Title: "News", Scam: true, Fake: true, Restricted: true, FlagsKnown: true}}, false)
	chat, err := db.GetChat(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !chat.Scam || !chat.Fake || !chat.Restricted {
		t.Fatalf("the full read did not store the flags: %+v", chat)
	}

	// A list entry knows no flags and must not clear them.
	d.saveChats(ctx, rpc.MethodChatList, []rpc.Chat{{ID: 5, Type: "channel", Title: "News"}}, true)
	chat, err = db.GetChat(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !chat.Scam || !chat.Fake || !chat.Restricted {
		t.Errorf("a list entry cleared the flags: %+v", chat)
	}

	// A list entry that did read the channel object updates them, so a flag
	// Telegram no longer sets goes away.
	d.saveChats(ctx, rpc.MethodChatList, []rpc.Chat{{ID: 5, Type: "channel", Title: "News", FlagsKnown: true}}, true)
	chat, err = db.GetChat(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Scam || chat.Fake || chat.Restricted {
		t.Errorf("a known record did not clear the flags: %+v", chat)
	}
}
