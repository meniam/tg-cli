package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/config"
	"tg-cli/internal/rpc"
)

// newChatFixture is a daemon with the scheduled load switched on and a fake
// Telegram whose pages never come, so a queued job stays visible.
func newChatFixture(t *testing.T) (*daemon, *syncTestClock) {
	t.Helper()
	d, tg, _, clock, _ := syncFixture(t)
	d.background = newBackgroundSync(d, BackgroundSyncConfig{PagePauseMin: historySyncPause, PagePauseMax: historySyncPause}, clock, nil)
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	return d, clock
}

func newChatEvent(t *testing.T, d *daemon, chat int64, id int) {
	t.Helper()
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &chat, testMessage(id, chat, "hello"), fmt.Sprintf("new:%d:%d", chat, id)))
}

func TestNewChatQueuesTheLatestMessagesOnItsFirstMessage(t *testing.T) {
	d, _ := newChatFixture(t)
	newChatEvent(t, d, 501, 1)
	job := d.sync.snapshot(501)
	if job == nil || job.Source != rpc.HistorySyncBackground || job.Messages != config.DefaultNewChatMessages {
		t.Fatalf("job of a new chat: %+v", job)
	}
	// The second message of the same chat queues nothing more, and neither does
	// a chat that already holds messages.
	d.sync.cancelAll()
	newChatEvent(t, d, 501, 2)
	if job := d.sync.snapshot(501); job == nil || job.State != rpc.HistorySyncCancelled {
		t.Fatalf("a known chat was queued again: %+v", job)
	}
}

func TestNewChatLoadFollowsTheSettingAndTheScheduler(t *testing.T) {
	d, _ := newChatFixture(t)
	d.liveMu.Lock()
	d.liveValues["new_chat_sync.messages"] = json.RawMessage(`250`)
	d.liveMu.Unlock()
	newChatEvent(t, d, 601, 1)
	if job := d.sync.snapshot(601); job == nil || job.Messages != 250 {
		t.Fatalf("configured depth: %+v", job)
	}
	d.liveMu.Lock()
	d.liveValues["new_chat_sync.messages"] = json.RawMessage(`0`)
	d.liveMu.Unlock()
	newChatEvent(t, d, 602, 1)
	if d.sync.snapshot(602) != nil {
		t.Fatal("0 must turn the load off")
	}
	// Without the scheduled load the Daemon makes no history request of its own.
	d.liveMu.Lock()
	d.liveValues["new_chat_sync.messages"] = json.RawMessage(`100`)
	d.liveMu.Unlock()
	d.background = nil
	newChatEvent(t, d, 603, 1)
	if d.sync.snapshot(603) != nil {
		t.Fatal("the load ran with the scheduled load switched off")
	}
}

func TestNewChatLoadSkipsAClosedChatAndAChatWithProgress(t *testing.T) {
	d, tg, _, clock, accessPath := syncFixture(t)
	d.background = newBackgroundSync(d, BackgroundSyncConfig{PagePauseMin: historySyncPause, PagePauseMax: historySyncPause}, clock, nil)
	tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
		<-ctx.Done()
		return rpc.HistoryPage{}, ctx.Err()
	}
	// A chat that already has load progress is not new.
	if _, err := d.store.SaveHistoryPage(context.Background(), 701, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	newChatEvent(t, d, 701, 1)
	if d.sync.snapshot(701) != nil {
		t.Fatal("a chat with load progress was treated as new")
	}
	// A chat that access.json closes never reaches the queue.
	writeAccess(t, accessPath, `{"block":[702]}`)
	newChatEvent(t, d, 702, 1)
	if d.sync.snapshot(702) != nil {
		t.Fatal("a closed chat was queued")
	}
}
