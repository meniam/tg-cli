package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// TestHistorySyncFirstMessageDate runs a history load to its end and checks that
// the first message date reaches the job, the idle status after the job is gone
// and the chat overview. Dates come from syncMessage: 1000 plus the message id.
func TestHistorySyncFirstMessageDate(t *testing.T) {
	d, tg, db, clock, _ := syncFixture(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "group", Title: "Team"}}); err != nil {
		t.Fatal(err)
	}
	pages := []rpc.HistoryPage{syncPage(42, 12, 11, 10), syncPage(42)}
	next := 0
	offsets := make(chan int, 4)
	tg.page = func(_ context.Context, _ chatref.Ref, offset int) (rpc.HistoryPage, error) {
		offsets <- offset
		page := pages[next]
		next++
		return page, nil
	}

	startSync(t, d, 42)
	receiveOffset(t, offsets)
	clock.awaitTimer(t)
	if job := d.sync.snapshot(42); job == nil || job.Complete || job.FirstMessageDate != nil {
		t.Fatalf("a running job must not carry the date yet: %+v", job)
	}
	clock.Advance(historySyncPause)
	receiveOffset(t, offsets)
	job := awaitSyncState(t, d, 42, rpc.HistorySyncDone)
	if job.FirstMessageDate == nil || *job.FirstMessageDate != 1010 {
		t.Fatalf("job first message date = %v, want 1010", job.FirstMessageDate)
	}

	// A new start on the complete chat answers from the stored state.
	if again := startSync(t, d, 42); again.State != rpc.HistorySyncDone || again.FirstMessageDate == nil || *again.FirstMessageDate != 1010 {
		t.Fatalf("start on a complete chat: %+v", again)
	}
	// The idle status reads the same date from the database.
	idle, err := d.idleHistorySyncJob(ctx, "42")
	if err != nil || idle.FirstMessageDate == nil || *idle.FirstMessageDate != 1010 {
		t.Fatalf("idle status: %+v err=%v", idle, err)
	}

	result, err := d.chatOverview(ctx, params(t, rpc.ChatOverviewParams{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range result.(rpc.ChatOverviewResult).Chats {
		if chat.ChatID == 42 {
			if chat.HistoryFirstMessageDate == nil || *chat.HistoryFirstMessageDate != 1010 {
				t.Fatalf("overview history_first_message_date = %v, want 1010", chat.HistoryFirstMessageDate)
			}
			return
		}
	}
	t.Fatal("chat 42 is missing from the overview")
}

func TestChatOverviewGapFields(t *testing.T) {
	d, _, db, _, _ := syncFixture(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "group", Title: "Team"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{{ChatID: 42, ID: 10, Date: 10, Raw: []byte(`{}`)}}, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCatchupGap(ctx, 42, 50, 10); err != nil {
		t.Fatal(err)
	}
	v, err := d.chatOverview(ctx, params(t, rpc.ChatOverviewParams{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.(rpc.ChatOverviewResult).Chats {
		if c.ChatID == 42 {
			if c.HistoryGapFrom == nil || *c.HistoryGapFrom != 10 || c.HistoryGapTo == nil || *c.HistoryGapTo != 50 {
				t.Fatalf("gap: %+v", c)
			}
			return
		}
	}
	t.Fatal("chat missing")
}
