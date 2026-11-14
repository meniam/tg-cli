package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func testEvent(t *testing.T, typ string, chat *int64, data any, key string) rpc.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return rpc.Event{Type: typ, ChatID: chat, Time: time.Now().UTC().Format(time.RFC3339), Data: raw, DedupKey: key}
}
func testMessage(id int, chat int64, text string) rpc.Message {
	return rpc.Message{ID: id, ChatID: chat, Date: time.Now().UTC().Format(time.RFC3339), Text: text}
}
func noEvent(t *testing.T, sub *rpc.Subscription) {
	t.Helper()
	select {
	case e := <-sub.Events():
		t.Fatalf("unexpected event: %+v", e)
	default:
	}
}
func nextEvent(t *testing.T, sub *rpc.Subscription) rpc.Event {
	t.Helper()
	select {
	case e := <-sub.Events():
		return e
	default:
		t.Fatal("event not delivered")
		return rpc.Event{}
	}
}

func TestEventAccessAndDeduplication(t *testing.T) {
	d, _, db, path := accessTestDaemon(t)
	d.hub = rpc.NewHub()
	sub := d.hub.Subscribe()
	defer sub.Close()
	writeAccess(t, path, `{"allow":[1]}`)
	closed := int64(2)
	samples := []rpc.Event{
		testEvent(t, rpc.EventMessageNew, &closed, testMessage(1, 2, "closed"), "new:2:1"),
		testEvent(t, rpc.EventMessageEdit, &closed, testMessage(1, 2, "changed"), "edit:2:1"),
		testEvent(t, rpc.EventMessageDelete, &closed, rpc.MessageDeleteData{ChatID: &closed, IDs: []int{1}}, "del:2:1"),
		testEvent(t, rpc.EventReaction, &closed, rpc.ReactionData{ChatID: 2, MessageID: 1}, "react:2:1"),
		testEvent(t, rpc.EventRead, &closed, rpc.ReadData{ChatID: 2}, ""),
		testEvent(t, rpc.EventTyping, nil, rpc.TypingData{ChatID: 2, UserID: 2}, ""),
		testEvent(t, rpc.EventStatus, nil, rpc.StatusData{UserID: 2}, ""),
		testEvent(t, rpc.EventGap, &closed, rpc.GapData{ChatID: &closed}, ""),
	}
	for _, event := range samples {
		d.handleEvent(event)
		noEvent(t, sub)
	}
	d.handleEvent(testEvent(t, rpc.EventGap, nil, rpc.GapData{Reason: "unknown"}, ""))
	if got := nextEvent(t, sub); got.Type != rpc.EventGap || got.ChatID != nil {
		t.Fatalf("account gap: %+v", got)
	}
	last, err := db.LastEventID(context.Background())
	if err != nil || last != 0 {
		t.Fatalf("closed events stored: %d %v", last, err)
	}
	open := int64(1)
	event := testEvent(t, rpc.EventMessageNew, &open, testMessage(7, 1, "needle"), "new:1:7")
	d.handleEvent(event)
	first := nextEvent(t, sub)
	if first.ID == "" {
		t.Fatal("durable event has no cursor")
	}
	d.handleEvent(event)
	noEvent(t, sub)
	last, err = db.LastEventID(context.Background())
	if err != nil || last != 1 {
		t.Fatalf("dedup: last=%d err=%v", last, err)
	}
	hits, err := db.Search(context.Background(), store.SearchOptions{Query: "needle", Scopes: []string{"messages"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("message search: %+v %v", hits, err)
	}
}

func TestDeleteWithoutChatPolicy(t *testing.T) {
	d, _, db, path := accessTestDaemon(t)
	d.hub = rpc.NewHub()
	sub := d.hub.Subscribe()
	defer sub.Close()
	ctx := context.Background()
	msg := testMessage(9, 1, "old")
	d.saveMessages(ctx, "test", []rpc.Message{msg})
	event := testEvent(t, rpc.EventMessageDelete, nil, rpc.MessageDeleteData{IDs: []int{9}}, "del:0:9")
	writeAccess(t, path, `{"allow":[2]}`)
	d.handleEvent(event)
	noEvent(t, sub)
	last, _ := db.LastEventID(ctx)
	if last != 0 {
		t.Fatal("closed delete stored")
	}
	writeAccess(t, path, `{"allow":[1]}`)
	d.handleEvent(event)
	got := nextEvent(t, sub)
	if got.ChatID == nil || *got.ChatID != 1 {
		t.Fatalf("delete chat: %+v", got)
	}
	d.saveMessages(ctx, "test", []rpc.Message{testMessage(10, 1, "one"), testMessage(10, 2, "two")})
	writeAccess(t, path, `{"allow":[1]}`)
	d.handleEvent(testEvent(t, rpc.EventMessageDelete, nil, rpc.MessageDeleteData{IDs: []int{10}}, "del:0:10"))
	noEvent(t, sub)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other := testEvent(t, rpc.EventMessageDelete, nil, rpc.MessageDeleteData{IDs: []int{1234}}, "del:0:1234")
	d.handleEvent(other)
	got = nextEvent(t, sub)
	if got.ChatID != nil {
		t.Fatalf("unfiltered unknown delete got chat: %+v", got)
	}
}

func TestDeleteWithoutChatIDMarksKnownMessagesAndPreservesEvent(t *testing.T) {
	const channelID int64 = -1000000000003
	type savedMessage struct {
		chatID int64
		id     int
	}
	for _, tc := range []struct {
		name, access string
		messages     []savedMessage
		ids          []int
		eventChat    int64
		marked       []bool
		delivered    bool
		wantChat     int64
	}{
		{"unfiltered private", "", []savedMessage{{1, 9}}, []int{9}, 0, []bool{true}, true, 0},
		{"unfiltered two chats", "", []savedMessage{{1, 9}, {2, 10}}, []int{9, 10}, 0, []bool{true, true}, true, 0},
		{"unfiltered unknown id", "", []savedMessage{{1, 9}}, []int{1234}, 0, []bool{false}, true, 0},
		{"filtered open", `{"allow":[1]}`, []savedMessage{{1, 9}}, []int{9}, 0, []bool{true}, true, 1},
		{"filtered closed among matches", `{"allow":[1]}`, []savedMessage{{1, 9}, {2, 10}}, []int{9, 10}, 0, []bool{false, false}, false, 0},
		{"filtered unknown id", `{"allow":[1]}`, []savedMessage{{1, 9}}, []int{1234}, 0, []bool{false}, false, 0},
		{"channel with chat id", "", []savedMessage{{channelID, 9}}, []int{9}, channelID, []bool{true}, true, channelID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, db, path := accessTestDaemon(t)
			sub := d.hub.Subscribe()
			defer sub.Close()
			ctx := context.Background()
			for _, message := range tc.messages {
				d.saveMessages(ctx, "test", []rpc.Message{testMessage(message.id, message.chatID, "saved")})
			}
			if tc.access != "" {
				writeAccess(t, path, tc.access)
			}
			var chat *int64
			if tc.eventChat != 0 {
				chat = &tc.eventChat
			}
			event := testEvent(t, rpc.EventMessageDelete, chat, rpc.MessageDeleteData{ChatID: chat, IDs: tc.ids}, "delete")
			d.handleEvent(event)
			if tc.delivered {
				got := nextEvent(t, sub)
				want := event
				want.ID = "1.0.0.0"
				if tc.wantChat != 0 {
					want.ChatID = &tc.wantChat
				}
				gotJSON, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(gotJSON, wantJSON) {
					t.Fatalf("event shape changed: got %s want %s", gotJSON, wantJSON)
				}
			} else {
				noEvent(t, sub)
			}
			for i, message := range tc.messages {
				stored, err := db.GetMessage(ctx, message.chatID, message.id)
				if err != nil || (stored.DeletedAt != nil) != tc.marked[i] {
					t.Fatalf("message %d/%d marker=%v want=%v err=%v", message.chatID, message.id, stored.DeletedAt, tc.marked[i], err)
				}
			}
			if tc.name == "unfiltered private" {
				if page := exportPage(t, d, rpc.ExportParams{Chat: "1"}); len(page.Messages) != 0 {
					t.Fatalf("deleted message exported by default: %+v", page)
				}
				page := exportPage(t, d, rpc.ExportParams{Chat: "1", IncludeDeleted: true})
				if len(page.Messages) != 1 || page.Messages[0].ID != 9 || !page.Messages[0].Deleted {
					t.Fatalf("deleted message not exported with marker: %+v", page)
				}
			}
		})
	}
}

func TestDeleteWithoutChatIDStorageErrorsProduceGap(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setFailure func(*daemon, *int)
	}{
		{"lookup", func(d *daemon, attempts *int) {
			d.findMessageChats = func(context.Context, []int) ([]int64, error) {
				(*attempts)++
				return nil, errors.New("lookup failed")
			}
		}},
		{"write", func(d *daemon, attempts *int) {
			d.markMessagesDeleted = func(context.Context, int64, []int) (int, error) {
				(*attempts)++
				return 0, errors.New("delete failed")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, db, _ := accessTestDaemon(t)
			sub := d.hub.Subscribe()
			defer sub.Close()
			ctx := context.Background()
			d.saveMessages(ctx, "test", []rpc.Message{testMessage(9, 1, "saved")})
			attempts := 0
			tc.setFailure(d, &attempts)
			d.handleEvent(testEvent(t, rpc.EventMessageDelete, nil, rpc.MessageDeleteData{IDs: []int{9}}, "delete"))
			if attempts != eventSaveAttempts {
				t.Fatalf("attempts=%d want=%d", attempts, eventSaveAttempts)
			}
			gap := nextEvent(t, sub)
			if gap.Type != rpc.EventGap || gap.ChatID != nil || gap.ID != "" || string(gap.Data) != `{"reason":"store"}` {
				t.Fatalf("gap=%+v", gap)
			}
			noEvent(t, sub)
			stored, err := db.GetMessage(ctx, 1, 9)
			if err != nil || stored.DeletedAt != nil {
				t.Fatalf("failed delete marked message: %+v err=%v", stored, err)
			}
		})
	}
}

func TestEventReplayAndExpiration(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	d.hub = rpc.NewHub()
	chat := int64(1)
	for i := 1; i <= 3; i++ {
		d.handleEvent(testEvent(t, rpc.EventMessageNew, &chat, testMessage(i, chat, fmt.Sprintf("text %d", i)), fmt.Sprintf("new:1:%d", i)))
	}
	plan, err := d.replayEvents(context.Background(), rpc.SubscribeParams{After: "1.0.0.0"})
	if err != nil || len(plan.Events) != 2 {
		t.Fatalf("replay: %+v %v", plan, err)
	}
	id, _ := rpc.CursorID(plan.Cursor)
	if id != 3 {
		t.Fatalf("cursor: %s", plan.Cursor)
	}
	current, err := d.replayEvents(context.Background(), rpc.SubscribeParams{})
	if err != nil || len(current.Events) != 0 {
		t.Fatalf("current: %+v %v", current, err)
	}
	if _, err := db.PruneEvents(context.Background(), time.Now().Add(time.Hour), 0); err != nil {
		t.Fatal(err)
	}
	_, err = d.replayEvents(context.Background(), rpc.SubscribeParams{After: "1.0.0.0"})
	if e, ok := err.(*rpc.Error); !ok || e.Code != rpc.CodeCursorExpired {
		t.Fatalf("expired cursor: %v", err)
	}
	resume, err := d.replayEvents(context.Background(), rpc.SubscribeParams{After: "3.0.0.0"})
	if err != nil || len(resume.Events) != 0 {
		t.Fatalf("last pruned cursor should resume: %+v %v", resume, err)
	}
}

func TestEventSubscriptionNeedsLogin(t *testing.T) {
	d, tg, _, _ := accessTestDaemon(t)
	tg.mu.Lock()
	tg.user = nil
	tg.mu.Unlock()
	_, err := d.replayEvents(context.Background(), rpc.SubscribeParams{})
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotAuthorized {
		t.Fatalf("unauthorized subscription: %v", err)
	}
}

func TestEventPruningStopsWithContext(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	ctx := context.Background()
	old := time.Now().Add(-8 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		_, _, err := db.AddEvent(ctx, store.Event{Type: rpc.EventReaction, CreatedAt: old.Add(time.Duration(i) * time.Hour), Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.pruneEvents(runCtx, 10*time.Millisecond, 7*24*time.Hour, 2); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pruner did not stop")
	}
	last, err := db.LastEventID(ctx)
	if err != nil || last != 0 {
		t.Fatalf("prune: %d %v", last, err)
	}
}

func TestEventStoreFailureProducesGap(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	d.hub = rpc.NewHub()
	sub := d.hub.Subscribe()
	defer sub.Close()
	var attempts int
	d.addEvent = func(context.Context, store.Event) (int64, bool, error) {
		attempts++
		return 0, false, errors.New("secret event details")
	}
	chat := int64(1)
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &chat, testMessage(1, chat, "secret"), "new:1:1"))
	if attempts != eventSaveAttempts {
		t.Fatalf("attempts = %d", attempts)
	}
	gap := nextEvent(t, sub)
	if gap.Type != rpc.EventGap || gap.ChatID != nil || gap.ID != "" || string(gap.Data) != `{"reason":"store"}` {
		t.Fatalf("gap = %+v", gap)
	}
	encoded, _ := json.Marshal(gap)
	if string(encoded) == "" || jsonContainsField(encoded, "id") {
		t.Fatalf("live gap has id: %s", encoded)
	}
	last, err := db.LastEventID(context.Background())
	if err != nil || last != 0 {
		t.Fatalf("failed event stored: %d %v", last, err)
	}
}

func jsonContainsField(raw []byte, field string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	_, found := fields[field]
	return found
}

func TestEventSubscriptionReplaysThenStreams(t *testing.T) {
	d, _, _, _ := accessTestDaemon(t)
	d.hub = rpc.NewHub()
	d.srv.SetEventSource(d.hub, d.replayEvents)
	chat := int64(1)
	for i := 1; i <= 2; i++ {
		d.handleEvent(testEvent(t, rpc.EventMessageNew, &chat, testMessage(i, chat, "history"), fmt.Sprintf("new:1:%d", i)))
	}
	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() { d.srv.ServeConn(serverSide); close(done) }()
	defer func() { clientSide.Close(); <-done; d.srv.Close() }()
	request, _ := json.Marshal(rpc.Request{JSONRPC: rpc.Version, ID: json.RawMessage(`1`), Method: rpc.MethodSubscribe, Params: json.RawMessage(`{"after":"1.0.0.0"}`)})
	if _, err := clientSide.Write(append(request, '\n')); err != nil {
		t.Fatal(err)
	}
	clientSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	decoder := json.NewDecoder(clientSide)
	var response rpc.Response
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("subscribe: %+v %v", response, err)
	}
	var notice rpc.Request
	if err := decoder.Decode(&notice); err != nil {
		t.Fatal(err)
	}
	var first rpc.Event
	if err := json.Unmarshal(notice.Params, &first); err != nil || notice.Method != rpc.MethodEvent || first.ID != "2.0.0.0" {
		t.Fatalf("replay: %+v %+v %v", notice, first, err)
	}
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &chat, testMessage(3, chat, "live"), "new:1:3"))
	if err := decoder.Decode(&notice); err != nil {
		t.Fatal(err)
	}
	var live rpc.Event
	if err := json.Unmarshal(notice.Params, &live); err != nil || live.ID != "3.0.0.0" {
		t.Fatalf("live: %+v %v", live, err)
	}
}

func TestSubscriberPreventsIdleStop(t *testing.T) {
	d := &daemon{hub: rpc.NewHub()}
	d.lastActivity.Store(time.Now().Add(-time.Hour).UnixNano())
	sub := d.hub.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go watchIdle(ctx, func() { close(stopped) }, slog.New(slog.DiscardHandler), d, 10*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("active subscriber did not prevent idle stop")
	default:
	}
	sub.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("idle stop did not resume")
	}
}
