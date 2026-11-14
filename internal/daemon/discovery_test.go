package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type discoveryTelegram struct {
	*fakeTelegram
	calls    map[string]int
	public   rpc.PublicSearchResult
	messages rpc.MessagesResult
	link     rpc.LinkResult
	err      error
}

func (f *discoveryTelegram) count(name string) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}
func (f *discoveryTelegram) SearchMessages(context.Context, chatref.Ref, rpc.SearchMessagesParams) (rpc.MessagesResult, error) {
	f.count("search")
	return f.messages, f.err
}
func (f *discoveryTelegram) SearchPublic(context.Context, rpc.PublicSearchParams) (rpc.PublicSearchResult, error) {
	f.count("public")
	return f.public, f.err
}
func (f *discoveryTelegram) MessageContext(context.Context, chatref.Ref, rpc.ContextParams) (rpc.MessagesResult, error) {
	f.count("context")
	return f.messages, f.err
}
func (f *discoveryTelegram) Comments(context.Context, chatref.Ref, rpc.CommentsParams) (rpc.MessagesResult, error) {
	f.count("comments")
	return f.messages, f.err
}
func (f *discoveryTelegram) MessageLink(context.Context, chatref.Ref, rpc.LinkParams) (rpc.LinkResult, error) {
	f.count("link")
	return f.link, f.err
}
func (f *discoveryTelegram) MarkRead(context.Context, chatref.Ref) (rpc.MarkReadResult, error) {
	f.count("mark_read")
	return rpc.MarkReadResult{ChatID: 42}, f.err
}
func discoveryDaemon(t *testing.T) (*daemon, *discoveryTelegram, *store.DB, string) {
	t.Helper()
	d, f, db, path := accessTestDaemon(t)
	fake := &discoveryTelegram{fakeTelegram: f, calls: map[string]int{}}
	d.opts.Telegram = fake
	return d, fake, db, path
}
func discoveryMessage(chat int64, id int, text string) rpc.Message {
	return rpc.Message{ChatID: chat, ID: id, Date: "2026-11-14T10:00:00Z", Text: text}
}

func TestDiscoveryReadVisibilityAndPublicFilter(t *testing.T) {
	d, tg, db, path := discoveryDaemon(t)
	ctx := context.Background()
	writeAccess(t, path, `{"allow":[42]}`)
	tg.messages = rpc.MessagesResult{Messages: []rpc.Message{discoveryMessage(42, 11, "visible"), discoveryMessage(77, 12, "secret")}}
	tg.public = rpc.PublicSearchResult{Hits: []rpc.PublicHit{{ChatID: 42, Message: discoveryMessage(42, 13, "public")}, {ChatID: 77, Message: discoveryMessage(77, 14, "hidden")}}}
	search, err := d.messageSearch(ctx, params(t, rpc.SearchMessagesParams{Chat: "42", Query: "a"}))
	if err != nil || len(search.(rpc.MessagesResult).Messages) != 1 {
		t.Fatalf("search: %+v %v", search, err)
	}
	if _, err := db.GetMessage(ctx, 42, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMessage(ctx, 77, 12); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden search saved: %v", err)
	}
	public, err := d.messageSearchPublic(ctx, params(t, rpc.PublicSearchParams{Query: "#tag"}))
	if err != nil || len(public.(rpc.PublicSearchResult).Hits) != 1 {
		t.Fatalf("public: %+v %v", public, err)
	}
	if _, err := db.GetMessage(ctx, 77, 14); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden public hit saved: %v", err)
	}
	for _, run := range []func() (any, error){
		func() (any, error) {
			return d.messageSearch(ctx, params(t, rpc.SearchMessagesParams{Chat: "77", Query: "a"}))
		},
		func() (any, error) { return d.messageContext(ctx, params(t, rpc.ContextParams{Chat: "77", ID: 11})) },
		func() (any, error) { return d.messageComments(ctx, params(t, rpc.CommentsParams{Chat: "77", ID: 11})) },
		func() (any, error) { return d.messageLink(ctx, params(t, rpc.LinkParams{Chat: "77", ID: 11})) },
	} {
		_, err := run()
		sameRPCError(t, err, rpc.ErrUnknownChatID())
	}
	if tg.calls["context"] != 0 || tg.calls["comments"] != 0 || tg.calls["link"] != 0 {
		t.Fatalf("closed chat reached Telegram: %+v", tg.calls)
	}
}

func TestDiscoveryContextLocalAndRemote(t *testing.T) {
	d, tg, db, path := discoveryDaemon(t)
	ctx := context.Background()
	writeAccess(t, path, `{"write":[42]}`)
	for _, id := range []int{8, 9, 10} {
		storedMessage(t, db, 42, id, "local")
	}
	withSender, _ := json.Marshal(rpc.Message{ID: 9, ChatID: 42, Date: "2026-11-14T10:00:00Z", Text: "local", Sender: &rpc.Sender{ID: 7, Name: "Ann", Username: "ann"}})
	if err := db.UpsertMessages(ctx, []store.Message{{ChatID: 42, ID: 9, Date: 1700000000, Text: "local", Raw: withSender}}); err != nil {
		t.Fatal(err)
	}
	tg.user = nil
	local, err := d.messageContext(ctx, []byte(`{"chat":"42","id":9,"local":true,"before":1,"after":1}`))
	if err != nil || len(local.(rpc.MessagesResult).Messages) != 3 || tg.calls["context"] != 0 {
		t.Fatalf("local: %+v %v calls=%v", local, err, tg.calls)
	}
	if sender := local.(rpc.MessagesResult).Messages[1].Sender; sender == nil || sender.Username != "ann" {
		t.Fatalf("local sender: %+v", sender)
	}
	_, err = d.messageContext(ctx, []byte(`{"chat":"42","id":99,"local":true}`))
	sameRPCError(t, err, rpc.ErrChatNotFound())
	tg.user = &rpc.User{ID: 42}
	tg.messages = rpc.MessagesResult{Messages: []rpc.Message{discoveryMessage(42, 21, "remote")}}
	remote, err := d.messageContext(ctx, params(t, rpc.ContextParams{Chat: "42", ID: 21}))
	if err != nil || len(remote.(rpc.MessagesResult).Messages) != 1 || tg.calls["context"] != 1 {
		t.Fatalf("remote: %+v %v", remote, err)
	}
	if _, err := db.GetMessage(ctx, 42, 21); err != nil {
		t.Fatal(err)
	}
	tg.messages = rpc.MessagesResult{Messages: []rpc.Message{discoveryMessage(42, 22, "comment"), discoveryMessage(77, 23, "hidden")}}
	writeAccess(t, path, `{"block":[77],"write":[42]}`)
	comments, err := d.messageComments(ctx, params(t, rpc.CommentsParams{Chat: "42", ID: 21}))
	if err != nil || len(comments.(rpc.MessagesResult).Messages) != 1 {
		t.Fatalf("comments: %+v %v", comments, err)
	}
	if _, err := db.GetMessage(ctx, 77, 23); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden comment stored: %v", err)
	}
}

func TestDiscoveryMarkReadRequiresWriteAndDryRun(t *testing.T) {
	d, tg, _, path := discoveryDaemon(t)
	ctx := context.Background()
	_, err := d.chatMarkRead(ctx, params(t, rpc.MarkReadParams{Chat: "42"}))
	sameRPCError(t, err, rpc.ErrWriteDisabled())
	if tg.calls["mark_read"] != 0 {
		t.Fatal("mark-read reached Telegram without write")
	}
	writeAccess(t, path, `{"write":[42]}`)
	result, err := d.chatMarkRead(ctx, params(t, rpc.MarkReadParams{Chat: "42", DryRun: true}))
	if err != nil || !result.(rpc.MarkReadResult).DryRun || tg.calls["mark_read"] != 0 {
		t.Fatalf("dry run: %+v %v", result, err)
	}
	result, err = d.chatMarkRead(ctx, params(t, rpc.MarkReadParams{Chat: "42"}))
	if err != nil || result.(rpc.MarkReadResult).ChatID != 42 || tg.calls["mark_read"] != 1 {
		t.Fatalf("read: %+v %v", result, err)
	}
	writeAccess(t, path, `{"allow":[77],"write":[42]}`)
	_, err = d.chatMarkRead(ctx, params(t, rpc.MarkReadParams{Chat: "42"}))
	sameRPCError(t, err, rpc.ErrUnknownChatID())
	if tg.calls["mark_read"] != 1 {
		t.Fatal("closed chat marked read")
	}
}

func TestDiscoveryLinkAndUnfilteredPublicResults(t *testing.T) {
	d, tg, _, _ := discoveryDaemon(t)
	tg.link = rpc.LinkResult{Link: "https://t.me/c/123/9"}
	result, err := d.messageLink(context.Background(), params(t, rpc.LinkParams{Chat: "42", ID: 9}))
	if err != nil || result.(rpc.LinkResult).Link != tg.link.Link || tg.calls["link"] != 1 {
		t.Fatalf("link: %+v %v calls=%v", result, err, tg.calls)
	}
	tg.public = rpc.PublicSearchResult{Hits: []rpc.PublicHit{{ChatID: 77, Message: discoveryMessage(77, 10, "public outside")}}}
	result, err = d.messageSearchPublic(context.Background(), params(t, rpc.PublicSearchParams{Query: "outside"}))
	if err != nil || len(result.(rpc.PublicSearchResult).Hits) != 1 {
		t.Fatalf("open public search: %+v %v", result, err)
	}
}
