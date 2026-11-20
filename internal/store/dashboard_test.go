package store

import (
	"context"
	"testing"
	"time"
)

// TestDashboardStatsGroupsMatchTheBackgroundPick uses the fixture of the pick
// tests: people 1-5 (5 complete), folder chats 10, 11, 13, other chats 12, 14-18
// (18 complete). The counters must agree with the groups the scheduled load
// picks from, because both come from one group expression.
func TestDashboardStatsGroupsMatchTheBackgroundPick(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)
	ctx := context.Background()
	got, err := db.DashboardStats(ctx, ChatFilter{}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := [3]HistoryGroupCount{{Total: 5, Complete: 1}, {Total: 3}, {Total: 6, Complete: 1}}
	if got.History != want || got.Chats != 14 {
		t.Fatalf("history = %+v chats = %d, want %+v and 14", got.History, got.Chats, want)
	}
	// What is not complete is exactly what the pick returns, group by group.
	picks, err := db.ChatsForBackgroundSync(ctx, ChatFilter{}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	var open [3]int
	for _, chat := range picks {
		open[chat.Group]++
	}
	for group, count := range got.History {
		if open[group] != count.Total-count.Complete {
			t.Errorf("group %d: the pick returns %d chats, the counters leave %d incomplete", group, open[group], count.Total-count.Complete)
		}
	}
}

func TestDashboardStatsFilterAndDeletedMessages(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)
	ctx := context.Background()
	// Ten chats hold two messages each.
	all, err := db.DashboardStats(ctx, ChatFilter{}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if all.Messages != 20 {
		t.Fatalf("messages = %d, want 20", all.Messages)
	}
	schemaExec(t, db.write, `UPDATE messages SET deleted_at = 1 WHERE chat_id = 14 AND id = 500`)
	// Chat 14 (other, 2 messages) and chat 18 (other, complete) are closed.
	got, err := db.DashboardStats(ctx, ChatFilter{Block: []int64{14, 18}}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got.History[BackgroundGroupOther] != (HistoryGroupCount{Total: 4}) || got.Chats != 12 {
		t.Fatalf("closed chats are counted: history = %+v chats = %d", got.History, got.Chats)
	}
	if got.Messages != 18 {
		t.Fatalf("messages = %d, want 18 (20 minus the 2 of the closed chat 14)", got.Messages)
	}
	// A deleted message is not counted, a closed chat is not counted either.
	visible, err := db.DashboardStats(ctx, ChatFilter{}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if visible.Messages != 19 {
		t.Fatalf("messages with a deleted one = %d, want 19", visible.Messages)
	}
	hidden, err := db.DashboardStats(ctx, ChatFilter{Hide: true}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Chats != 0 || hidden.Messages != 0 || hidden.History != ([3]HistoryGroupCount{}) {
		t.Fatalf("a closed account shows %+v", hidden)
	}
}

func TestDashboardStatsEvents(t *testing.T) {
	db := openTestDB(t)
	now := time.Unix(1_800_000_000, 0)
	for _, age := range []time.Duration{10 * time.Minute, 59 * time.Minute, 61 * time.Minute, 5 * time.Hour} {
		schemaExec(t, db.write, `INSERT INTO events(type,chat_id,created_at,payload) VALUES('message.new',NULL,?,'{}')`, now.Add(-age).Unix())
	}
	got, err := db.DashboardStats(context.Background(), ChatFilter{}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.EventsSince != 2 || got.EventsTotal != 4 {
		t.Fatalf("events = %d in the hour of %d, want 2 of 4", got.EventsSince, got.EventsTotal)
	}
}
