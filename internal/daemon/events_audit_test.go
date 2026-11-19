package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// audit04Eventually polls a condition until it holds. The deadline is a safety net
// for a regression, not a duration the test depends on.
func audit04Eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// audit04ReplayDaemon stores four events: a message of chat 1 in topic 5, a
// message of chat 2, and a reaction in each chat.
func audit04ReplayDaemon(t *testing.T) (*daemon, *store.DB, string) {
	t.Helper()
	d, _, db, path := accessTestDaemon(t)
	d.hub = rpc.NewHub()
	if err := db.UpsertChats(context.Background(), []store.Chat{{ID: 1, Type: "group", Title: "One"}, {ID: 2, Type: "group", Title: "Two"}}); err != nil {
		t.Fatal(err)
	}
	one, two := int64(1), int64(2)
	topic := testMessage(1, one, "in a topic")
	topic.TopicID = 5
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &one, topic, "new:1:1"))
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &two, testMessage(2, two, "other chat"), "new:2:2"))
	d.handleEvent(testEvent(t, rpc.EventReaction, &one, rpc.ReactionData{ChatID: 1, MessageID: 1}, "react:1:1"))
	d.handleEvent(testEvent(t, rpc.EventReaction, &two, rpc.ReactionData{ChatID: 2, MessageID: 2}, "react:2:2"))
	if last, err := db.LastEventID(context.Background()); err != nil || last != 4 {
		t.Fatalf("the fixture stored %d events: %v", last, err)
	}
	return d, db, path
}

func audit04EventTypes(plan rpc.ReplayPlan) string {
	var types []string
	for _, event := range plan.Events {
		chat := int64(0)
		if event.ChatID != nil {
			chat = *event.ChatID
		}
		types = append(types, fmt.Sprintf("%s@%d", event.Type, chat))
	}
	return strings.Join(types, ",")
}

// TestAudit04_ReplayRejectsBadCursors covers a cursor that cannot be parsed and a
// cursor beyond the journal.
func TestAudit04_ReplayRejectsBadCursors(t *testing.T) {
	d, _, _ := audit04ReplayDaemon(t)
	for _, tc := range []struct{ name, after, text string }{
		{"not a cursor", "x", "invalid event cursor"},
		{"too few parts", "1.0.0", "invalid event cursor"},
		{"negative part", "1.-1.0.0", "invalid event cursor"},
		{"ahead of the journal", "99.0.0.0", "ahead of the journal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.replayEvents(context.Background(), rpc.SubscribeParams{After: tc.after})
			if code := rpcCode(t, err); code != rpc.CodeInvalidParams {
				t.Fatalf("code = %d, want %d (%v)", code, rpc.CodeInvalidParams, err)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Errorf("error = %v, want %q", err, tc.text)
			}
		})
	}
}

// TestAudit04_ReplayFilters covers the filters of a subscription: chats, types and
// topic, each of them on the stored journal.
func TestAudit04_ReplayFilters(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		params rpc.SubscribeParams
		want   string
	}{
		{"no filter", rpc.SubscribeParams{After: "0.0.0.0"}, "message.new@1,message.new@2,reaction@1,reaction@2"},
		{"one chat", rpc.SubscribeParams{After: "0.0.0.0", Chats: []string{"1"}}, "message.new@1,reaction@1"},
		{"types", rpc.SubscribeParams{After: "0.0.0.0", Types: []string{rpc.EventReaction}}, "reaction@1,reaction@2"},
		{"topic", rpc.SubscribeParams{After: "0.0.0.0", Topic: 5}, "message.new@1"},
		{"chat and type", rpc.SubscribeParams{After: "0.0.0.0", Chats: []string{"2"}, Types: []string{rpc.EventMessageNew}}, "message.new@2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := audit04ReplayDaemon(t)
			plan, err := d.replayEvents(ctx, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if got := audit04EventTypes(plan); got != tc.want {
				t.Errorf("events = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestAudit04_ReplayChatFilterNeedsAKnownOpenChat covers a chat filter on a chat
// that is closed by access.json or that the database does not know.
func TestAudit04_ReplayChatFilterNeedsAKnownOpenChat(t *testing.T) {
	d, _, path := audit04ReplayDaemon(t)
	ctx := context.Background()
	if _, err := d.replayEvents(ctx, rpc.SubscribeParams{Chats: []string{"999"}}); rpcCode(t, err) != rpc.CodeNotFound {
		t.Errorf("an unknown chat: %v", err)
	}
	audit04WriteAccess(t, path, `{"allow":[1]}`, time.Hour)
	_, err := d.replayEvents(ctx, rpc.SubscribeParams{Chats: []string{"2"}})
	sameRPCError(t, err, rpc.ErrChatNotFound())
	if _, err := d.replayEvents(ctx, rpc.SubscribeParams{Chats: []string{"1"}}); err != nil {
		t.Errorf("an open chat: %v", err)
	}
}

// TestAudit04_ReplayHidesEventsOfAChatClosedLater covers a chat closed after its
// events were written: the journal keeps them, the replay does not show them.
func TestAudit04_ReplayHidesEventsOfAChatClosedLater(t *testing.T) {
	d, _, path := audit04ReplayDaemon(t)
	audit04WriteAccess(t, path, `{"block":[2]}`, time.Hour)
	plan, err := d.replayEvents(context.Background(), rpc.SubscribeParams{After: "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := audit04EventTypes(plan); got != "message.new@1,reaction@1" {
		t.Errorf("events = %s, want only chat 1", got)
	}
	if id, err := rpc.CursorID(plan.Cursor); err != nil || id != 4 {
		t.Errorf("cursor %s must still reach the end of the journal: %v", plan.Cursor, err)
	}
}

// TestAudit04_ReplayCursorCarriesTheUpdateState covers the cursor of a replay when
// the account has a saved update state: every id carries pts, qts and date.
func TestAudit04_ReplayCursorCarriesTheUpdateState(t *testing.T) {
	d, db, _ := audit04ReplayDaemon(t)
	ctx := context.Background()
	if err := db.SetAccountState(ctx, "updates.state.42", []byte(`{"pts":10,"qts":2,"date":99}`)); err != nil {
		t.Fatal(err)
	}
	plan, err := d.replayEvents(ctx, rpc.SubscribeParams{After: "2.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cursor != "4.10.2.99" {
		t.Errorf("cursor = %s, want 4.10.2.99", plan.Cursor)
	}
	for _, event := range plan.Events {
		if !strings.HasSuffix(event.ID, ".10.2.99") {
			t.Errorf("event id %s does not carry the update state", event.ID)
		}
	}
	if len(plan.Events) != 2 {
		t.Errorf("events = %s, want the two after cursor 2", audit04EventTypes(plan))
	}
}
