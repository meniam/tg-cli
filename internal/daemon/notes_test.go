package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func noteTestRPC(t *testing.T) (*rpc.Client, *store.DB, *fakeTelegram, *safeBuffer) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tg := newFake()
	logs := &safeBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := &daemon{opts: Options{Telegram: tg, Store: db}, store: db, log: logger, srv: rpc.NewServer(logger)}
	d.register()
	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() { d.srv.ServeConn(serverSide); close(done) }()
	client := rpc.NewClient(clientSide)
	t.Cleanup(func() { client.Close(); <-done })
	return client, db, tg, logs
}
func noteCall(t *testing.T, client *rpc.Client, method string, params, result any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return client.Call(ctx, method, params, result)
}
func resolveCount(tg *fakeTelegram) int { tg.mu.Lock(); defer tg.mu.Unlock(); return tg.resolveCalls }

func TestNoteRPCNumericTargetsAndFilters(t *testing.T) {
	client, db, tg, _ := noteTestRPC(t)
	var chat, person, message, ai rpc.Note
	for _, tc := range []struct {
		params rpc.NoteAddParams
		out    *rpc.Note
	}{
		{rpc.NoteAddParams{Kind: "chat", Chat: "-10", Body: "Chat Ёлка", Tags: []string{" Tree "}}, &chat},
		{rpc.NoteAddParams{Kind: "person", Person: "42", Body: "Person note"}, &person},
		{rpc.NoteAddParams{Kind: "message", Chat: "-10", MessageID: 7, Body: "Message note"}, &message},
		{rpc.NoteAddParams{Kind: "chat", Chat: "-10", Body: "AI note", Author: "ai", Model: "model-x"}, &ai},
	} {
		if err := noteCall(t, client, rpc.MethodNoteAdd, tc.params, tc.out); err != nil {
			t.Fatal(err)
		}
	}
	if resolveCount(tg) != 0 {
		t.Fatal("numeric targets called Telegram")
	}
	var collision rpc.Note
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "message", Chat: "-10", MessageID: 42, Body: "same target id as person"}, &collision); err != nil {
		t.Fatal(err)
	}
	if chat.Author != "owner" || chat.ChatID == nil || *chat.ChatID != -10 || person.TargetID == nil || *person.TargetID != 42 || message.TargetID == nil || *message.TargetID != 7 || ai.Model != "model-x" {
		t.Fatalf("notes: chat=%+v person=%+v message=%+v ai=%+v", chat, person, message, ai)
	}
	if _, err := time.Parse(time.RFC3339, chat.CreatedAt); err != nil {
		t.Errorf("created_at=%q: %v", chat.CreatedAt, err)
	}
	if chat.UpdatedAt != chat.CreatedAt || len(chat.Tags) != 1 || chat.Tags[0] != "tree" {
		t.Errorf("chat note=%+v", chat)
	}
	if _, err := db.GetNote(context.Background(), chat.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		params rpc.NoteListParams
		want   []int64
	}{
		{rpc.NoteListParams{Kind: "chat", Chat: "-10", Author: "owner", Tag: "TREE"}, []int64{chat.ID}},
		{rpc.NoteListParams{Person: "42"}, []int64{person.ID}},
		{rpc.NoteListParams{Chat: "-10", MessageID: 42}, []int64{collision.ID}},
		{rpc.NoteListParams{Kind: "message", Chat: "-10", MessageID: 7}, []int64{message.ID}},
		{rpc.NoteListParams{Chat: "-10", BeforeID: ai.ID, Limit: 1}, []int64{message.ID}},
	} {
		var list rpc.NoteListResult
		if err := noteCall(t, client, rpc.MethodNoteList, tc.params, &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Notes) != len(tc.want) {
			t.Fatalf("list=%+v want=%v", list.Notes, tc.want)
		}
		for i, n := range list.Notes {
			if n.ID != tc.want[i] {
				t.Errorf("note id=%d want=%d", n.ID, tc.want[i])
			}
		}
	}
	var deleted rpc.NoteDeleteResult
	if err := noteCall(t, client, rpc.MethodNoteDelete, rpc.NoteDeleteParams{ID: message.ID}, &deleted); err != nil || deleted.Deleted != 1 {
		t.Fatalf("delete=%+v err=%v", deleted, err)
	}
	if err := noteCall(t, client, rpc.MethodNoteDelete, rpc.NoteDeleteParams{ID: message.ID}, nil); rpcCode(t, err) != rpc.CodeNotFound {
		t.Errorf("missing delete=%v", err)
	}
	if err := noteCall(t, client, rpc.MethodNoteDeleteByAuthor, rpc.NoteDeleteByAuthorParams{Author: "ai"}, &deleted); err != nil || deleted.Deleted != 1 {
		t.Fatalf("delete ai=%+v err=%v", deleted, err)
	}
	if _, err := db.GetNote(context.Background(), chat.ID); err != nil {
		t.Fatal("owner note removed:", err)
	}
	if _, err := db.GetNote(context.Background(), ai.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AI note remained: %v", err)
	}
	if err := noteCall(t, client, rpc.MethodNoteDeleteByAuthor, rpc.NoteDeleteByAuthorParams{Author: "bot"}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Errorf("invalid author=%v", err)
	}
}

func TestNoteRPCOfflineFirstAndFallback(t *testing.T) {
	client, db, tg, _ := noteTestRPC(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: -10, Type: "group", Title: "Team", Username: noteString("TeamNews")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []store.Person{{ID: 42, Username: noteString("PersonX")}}); err != nil {
		t.Fatal(err)
	}
	var c, p rpc.Note
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "chat", Chat: "@teamnews", Body: "offline"}, &c); err != nil {
		t.Fatal(err)
	}
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "person", Person: "@PERSONX", Body: "offline"}, &p); err != nil {
		t.Fatal(err)
	}
	if c.ChatID == nil || *c.ChatID != -10 || p.TargetID == nil || *p.TargetID != 42 || resolveCount(tg) != 0 {
		t.Fatalf("offline lookup failed: %+v %+v calls=%d", c, p, resolveCount(tg))
	}
	var list rpc.NoteListResult
	if err := noteCall(t, client, rpc.MethodNoteList, rpc.NoteListParams{Chat: "@unknownname"}, &list); err != nil || len(list.Notes) != 0 {
		t.Fatalf("unknown list=%+v err=%v", list, err)
	}
	if err := noteCall(t, client, rpc.MethodNoteList, rpc.NoteListParams{Person: "@unknownname"}, &list); err != nil || len(list.Notes) != 0 {
		t.Fatalf("unknown person list=%+v err=%v", list, err)
	}
	var search rpc.SearchResult
	if err := noteCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: "offline", Chat: "@unknownname"}, &search); err != nil || len(search.Hits) != 0 || resolveCount(tg) != 0 {
		t.Fatalf("unknown search=%+v err=%v calls=%d", search, err, resolveCount(tg))
	}
	for _, params := range []rpc.SearchParams{{Chat: "@unknownname", Query: "*"}, {Chat: "@unknownname", Query: "offline", Scopes: []string{"bad"}}} {
		if err := noteCall(t, client, rpc.MethodSearch, params, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("invalid search with unknown chat: %v", err)
		}
	}
	close(tg.ready)
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "chat", Chat: "@unknownname", Body: "fallback"}, nil); rpcCode(t, err) != rpc.CodeNotAuthorized {
		t.Errorf("without login=%v", err)
	}
	tg.mu.Lock()
	tg.user = &rpc.User{ID: 1}
	tg.mu.Unlock()
	var fallback rpc.Note
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "chat", Chat: "@unknownname", Body: "fallback"}, &fallback); err != nil {
		t.Fatal(err)
	}
	if fallback.ChatID == nil || *fallback.ChatID != 77 || resolveCount(tg) != 1 {
		t.Fatalf("fallback=%+v calls=%d", fallback, resolveCount(tg))
	}
	tg.mu.Lock()
	tg.resolved = &rpc.Chat{ID: 99, Type: "user", Title: "Person"}
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "person", Person: "@unknownname", Body: "fallback person"}, &fallback); err != nil || fallback.TargetID == nil || *fallback.TargetID != 99 {
		t.Fatalf("person fallback=%+v err=%v", fallback, err)
	}
	tg.mu.Lock()
	tg.resolved = &rpc.Chat{ID: -20, Type: "group"}
	tg.mu.Unlock()
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "person", Person: "@unknownname", Body: "bad"}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Errorf("negative person=%v", err)
	}
	for _, params := range []rpc.NoteAddParams{{Kind: "person", Person: "-20", Body: "bad"}, {Kind: "chat", Chat: "+15551234567", Body: "bad"}, {Kind: "chat", Chat: "t.me/+AbCdEfGh1234", Body: "bad"}} {
		if err := noteCall(t, client, rpc.MethodNoteAdd, params, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("params=%+v err=%v", params, err)
		}
	}
}
func noteString(v string) *string { return &v }

func TestNoteRPCValidationSearchAndSecrets(t *testing.T) {
	client, db, _, logs := noteTestRPC(t)
	ctx := context.Background()
	secretBody := "PRIVATE_NOTE_BODY_Ёлка"
	secretQuery := "PRIVATE_SEARCH_QUERY"
	for _, params := range []rpc.NoteAddParams{
		{Kind: "chat", Chat: "1", Body: " "},
		{Kind: "chat", Chat: "1", Body: strings.Repeat("x", 16*1024+1)},
		{Kind: "chat", Chat: "1", Body: "valid", Tags: []string{" "}},
		{Kind: "chat", Chat: "1", Body: "valid", Tags: []string{strings.Repeat("x", 65)}},
		{Kind: "chat", Chat: "1", Body: "valid", Author: "bot"},
		{Kind: "chat", Chat: "1", Body: "valid", Author: "owner", Model: "bad"},
	} {
		if err := noteCall(t, client, rpc.MethodNoteAdd, params, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("validation %+v: %v", params, err)
		}
	}
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 1, ID: 5, Date: 100, Text: "Ёлка message", Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	var owner, ai rpc.Note
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "chat", Chat: "1", Body: secretBody}, &owner); err != nil {
		t.Fatal(err)
	}
	if err := noteCall(t, client, rpc.MethodNoteAdd, rpc.NoteAddParams{Kind: "chat", Chat: "1", Body: "Елка AI", Author: "ai"}, &ai); err != nil {
		t.Fatal(err)
	}
	var result rpc.SearchResult
	if err := noteCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: "ЕЛКА"}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 3 {
		t.Fatalf("search hits=%+v", result.Hits)
	}
	var noteHits, messageHits int
	for _, h := range result.Hits {
		if h.Scope == "notes" {
			noteHits++
		}
		if h.Scope == "messages" {
			messageHits++
			if h.Date != "1970-01-01T00:01:40Z" || !strings.Contains(h.Snippet, "Ёлка") {
				t.Errorf("message hit=%+v", h)
			}
		}
	}
	if noteHits != 2 || messageHits != 1 {
		t.Fatalf("hits=%+v", result.Hits)
	}
	if err := noteCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: "ёлка", Author: "ai"}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 {
		t.Fatalf("AI search=%+v", result.Hits)
	}
	for _, h := range result.Hits {
		if h.Scope == "notes" && h.ID == owner.ID {
			t.Error("owner note shown in AI search")
		}
	}
	for _, params := range []rpc.SearchParams{{Query: "ёлка", Scopes: []string{"bad"}}, {Query: " "}, {Query: "ёлка", Author: "bot"}} {
		if err := noteCall(t, client, rpc.MethodSearch, params, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("search validation %+v: %v", params, err)
		}
	}
	if err := noteCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: secretQuery}, &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), secretBody) || strings.Contains(logs.String(), secretQuery) {
		t.Errorf("secret appeared in log: %s", logs.String())
	}
}

func TestRunDatabaseFailureBeforeSocket(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Database, 0700); err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), Options{Profile: "test", Paths: paths, Telegram: newFake()})
	if err == nil || !strings.Contains(err.Error(), "open database") {
		t.Fatalf("Run error=%v", err)
	}
	if _, err := os.Stat(paths.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket exists after database failure: %v", err)
	}
}

func TestRunStoreOwnershipBeforeListening(t *testing.T) {
	for _, injected := range []bool{false, true} {
		name := "owned"
		if injected {
			name = "injected"
		}
		t.Run(name, func(t *testing.T) {
			paths, err := ResolvePaths(shortTempDir(t), "test", DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(paths.Socket, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.Socket, "keep"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			opts := Options{Profile: "test", Paths: paths, Telegram: newFake()}
			if injected {
				opts.Store, err = store.Open(paths.Database)
				if err != nil {
					t.Fatal(err)
				}
				defer opts.Store.Close()
			}
			if err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "remove stale socket") {
				t.Fatalf("Run error = %v", err)
			}
			if injected {
				if _, err := opts.Store.Version(); err != nil {
					t.Errorf("injected store was closed: %v", err)
				}
			} else {
				opened, err := store.Open(paths.Database)
				if err != nil {
					t.Fatalf("Run did not create database before socket: %v", err)
				}
				opened.Close()
			}
		})
	}
}
