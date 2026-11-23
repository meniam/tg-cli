package daemon

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func seedLimitedChat(t *testing.T, db *store.DB, id int64, n int) {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: id, Type: "supergroup", Title: "Chat " + strconv.FormatInt(id, 10)}}); err != nil {
		t.Fatal(err)
	}
	messages := make([]store.Message, 0, n)
	for m := 1; m <= n; m++ {
		messages = append(messages, store.Message{ChatID: id, ID: m, Date: int64(m), Text: "m", Raw: []byte(`{}`)})
	}
	if _, err := db.SaveHistoryPage(ctx, id, messages, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
}

func TestChatLimitSetNeedsConfirmationWhenItWouldDelete(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	ctx := context.Background()
	seedLimitedChat(t, db, 42, 150)
	set := func(messages int, confirm bool) (rpc.ChatLimitSetResult, error) {
		res, err := d.chatLimitSet(ctx, params(t, rpc.ChatLimitSetParams{Chat: "42", Messages: messages, Confirm: confirm}))
		if err != nil {
			return rpc.ChatLimitSetResult{}, err
		}
		return res.(rpc.ChatLimitSetResult), nil
	}
	_, err := set(100, false)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams {
		t.Fatalf("unconfirmed: %v", err)
	}
	if _, found, _ := db.ChatMessageLimit(ctx, 42); found {
		t.Fatal("the limit was stored without the confirmation")
	}
	for _, bad := range []int{0, 99} {
		if _, err := set(bad, true); err == nil {
			t.Errorf("accepted %d", bad)
		}
	}
	res, err := set(100, true)
	if err != nil || res.Saved != 150 || res.Keep != 95 || res.WillDelete != 55 {
		t.Fatalf("confirmed: %+v %v", res, err)
	}
	// A limit above the saved count needs no confirmation and deletes nothing.
	res, err = set(1000, false)
	if err != nil || res.WillDelete != 0 {
		t.Fatalf("roomy: %+v %v", res, err)
	}
	if limit, found, _ := db.ChatMessageLimit(ctx, 42); !found || limit != 1000 {
		t.Fatalf("stored limit %d %v", limit, found)
	}
	if _, err := d.chatLimitSet(ctx, params(t, rpc.ChatLimitSetParams{Chat: "99999", Messages: 100})); err == nil {
		t.Fatal("an unknown chat was accepted")
	}
}

func TestEnforceChatLimitsTrimsMarksCompleteAndSkipsClosedChats(t *testing.T) {
	d, _, db, accessPath := accessTestDaemon(t)
	ctx := context.Background()
	seedLimitedChat(t, db, 42, 150)
	seedLimitedChat(t, db, 43, 150)
	seedLimitedChat(t, db, 44, 150) // no limit
	seedLimitedChat(t, db, 45, 150) // closed by access.json
	for _, id := range []int64{42, 43, 45} {
		if err := db.SetChatMessageLimit(ctx, id, 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetChatMessageLimit(ctx, 43, 200); err != nil {
		t.Fatal(err)
	}
	writeAccess(t, accessPath, `{"block":[45]}`)

	if got := d.enforceChatLimits(ctx); got != 55 {
		t.Fatalf("removed %d, want 55", got)
	}
	counts := map[int64]int{42: 95, 43: 150, 44: 150, 45: 150}
	for id, want := range counts {
		if got, _ := db.CountChatMessages(ctx, id); got != want {
			t.Errorf("chat %d holds %d, want %d", id, got, want)
		}
	}
	state, _, err := db.SyncState(ctx, 42)
	if err != nil || !state.Complete || state.FirstMessageDate != nil {
		t.Fatalf("trimmed chat state: %+v %v", state, err)
	}
	if state, _, _ := db.SyncState(ctx, 43); state.Complete {
		t.Fatal("an untouched chat was marked complete")
	}
	if got := d.enforceChatLimits(ctx); got != 0 {
		t.Fatalf("second pass removed %d", got)
	}
}

func TestChatLimitListShowsOpenChatsOnly(t *testing.T) {
	d, _, db, accessPath := accessTestDaemon(t)
	ctx := context.Background()
	seedLimitedChat(t, db, 42, 120)
	seedLimitedChat(t, db, 45, 120)
	for _, id := range []int64{42, 45} {
		if err := db.SetChatMessageLimit(ctx, id, 500); err != nil {
			t.Fatal(err)
		}
	}
	writeAccess(t, accessPath, `{"block":[45]}`)
	res, err := d.chatLimitList(ctx, params(t, rpc.ChatLimitListParams{}))
	list := res.(rpc.ChatLimitListResult)
	if err != nil || len(list.Chats) != 1 || list.Chats[0].ChatID != 42 || list.Chats[0].Messages != 500 || list.Chats[0].Saved != 120 || list.Chats[0].Title == "" {
		t.Fatalf("list: %+v %v", list, err)
	}
	off, err := d.chatLimitOff(ctx, params(t, rpc.ChatLimitOffParams{Chat: "42"}))
	if err != nil || !off.(rpc.ChatLimitOffResult).Removed {
		t.Fatalf("off: %+v %v", off, err)
	}
}

func TestHistorySyncFullLoadUsesTheLimitOfTheChat(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "supergroup", Title: "Chat"}}); err != nil {
		t.Fatal(err)
	}
	// The global limits are off, the chat has its own.
	if err := db.SetChatMessageLimit(ctx, 42, 100); err != nil {
		t.Fatal(err)
	}
	ids := func(from, to int) []int {
		out := make([]int, 0, to-from+1)
		for id := to; id >= from; id-- {
			out = append(out, id)
		}
		return out
	}
	pages := &depthPages{pages: []rpc.HistoryPage{syncPage(42, ids(151, 200)...), syncPage(42, ids(101, 150)...), syncPage(42, ids(51, 100)...), syncPage(42)}}
	tg.page = pages.fetch
	startSync(t, d, 42)
	job := finishPages(t, d, clock, 42, rpc.HistorySyncDone)
	if job.Pages != 2 || !job.Complete || job.FirstMessageDate != nil {
		t.Fatalf("job: %+v", job)
	}
	if got := pages.requests(); len(got) != 2 {
		t.Fatalf("requests=%v, want two", got)
	}
}
