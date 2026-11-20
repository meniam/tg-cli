package daemon

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// sourcesFixture seeds the chats the source tests use and keeps the worker
// goroutine from running, so the tests look at the queue:
//
//	1001 personal chat (@annabel)        1002 supergroup with a mark (no video)
//	1003 supergroup without a mark       1004 personal chat that access.json closes
//	1005 bot                             1006 supergroup with a mark and video
func sourcesFixture(t *testing.T, cfg VoiceTranscriptionConfig) *voiceFixture {
	t.Helper()
	f := newVoiceFixture(t, cfg)
	f.holdWorker()
	ctx := context.Background()
	chats := []store.Chat{
		{ID: 1001, Type: "user", Title: "Ann", Username: strPtr("annabel")},
		{ID: 1002, Type: "supergroup", Title: "Marked"},
		{ID: 1003, Type: "supergroup", Title: "Plain"},
		{ID: 1004, Type: "user", Title: "Closed"},
		{ID: 1005, Type: "bot", Title: "Bot"},
		{ID: 1006, Type: "supergroup", Title: "Marked with video"},
	}
	if err := f.db.UpsertChats(ctx, chats); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetVoiceChat(ctx, 1002, false, voiceT0); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetVoiceChat(ctx, 1006, true, voiceT0); err != nil {
		t.Fatal(err)
	}
	writeAccess(t, f.accessPath, `{"block":[1004]}`)
	return f
}

// event feeds one message event of the stream to the daemon, the way the
// Telegram client does.
func (f *voiceFixture) event(typ string, chat int64, id int, media string, out bool) {
	f.t.Helper()
	msg := rpc.Message{ID: id, ChatID: chat, Date: voiceT0.UTC().Format(time.RFC3339), MediaType: media, Out: out}
	f.d.handleEvent(testEvent(f.t, typ, &chat, msg, fmt.Sprintf("%s:%d:%d", typ, chat, id)))
}

func (f *voiceFixture) noJob(chat int64, id int) {
	f.t.Helper()
	if j, err := f.db.GetVoiceJob(context.Background(), chat, id); !errors.Is(err, store.ErrNotFound) {
		f.t.Fatalf("job %d/%d exists: %+v %v", chat, id, j, err)
	}
}

func (f *voiceFixture) counts() store.VoiceQueueCounts {
	f.t.Helper()
	c, err := f.db.VoiceQueueCounts(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// untouched fails when a source reached Telegram: sources only touch the queue
// and the database.
func (f *voiceFixture) untouched() {
	f.t.Helper()
	if f.tg.downloadCount() != 0 || f.tg.transcribed() != 0 || f.tg.resolveCalls != 0 {
		f.t.Fatalf("a source called Telegram: downloads=%d transcribe=%d resolve=%d", f.tg.downloadCount(), f.tg.transcribed(), f.tg.resolveCalls)
	}
}

// TestVoiceSourceChatRule is the table of the one rule: own never, voice and
// video note need a personal chat or a mark, video needs a mark with video.
func TestVoiceSourceChatRule(t *testing.T) {
	private := store.VoiceChatRule{Private: true}
	marked := store.VoiceChatRule{Marked: true}
	markedVideo := store.VoiceChatRule{Marked: true, Video: true}
	both := store.VoiceChatRule{Private: true, Marked: true}
	for _, tc := range []struct {
		name string
		rule store.VoiceChatRule
		msg  voiceMessage
		ok   bool
	}{
		{"voice, personal", private, voiceMessage{MediaType: "voice"}, true},
		{"voice, marked", marked, voiceMessage{MediaType: "voice"}, true},
		{"voice, neither", store.VoiceChatRule{}, voiceMessage{MediaType: "voice"}, false},
		{"video note, personal", private, voiceMessage{MediaType: "video_note"}, true},
		{"video note, marked", marked, voiceMessage{MediaType: "video_note"}, true},
		{"video note, neither", store.VoiceChatRule{}, voiceMessage{MediaType: "video_note"}, false},
		{"video, personal", private, voiceMessage{MediaType: "video"}, false},
		{"video, marked without video", marked, voiceMessage{MediaType: "video"}, false},
		{"video, marked with video", markedVideo, voiceMessage{MediaType: "video"}, true},
		{"video, personal and marked with video", store.VoiceChatRule{Private: true, Marked: true, Video: true}, voiceMessage{MediaType: "video"}, true},
		{"own voice, personal", private, voiceMessage{MediaType: "voice", Out: true}, false},
		{"own voice, marked", both, voiceMessage{MediaType: "voice", Out: true}, false},
		{"own video, marked with video", markedVideo, voiceMessage{MediaType: "video", Out: true}, false},
		{"a photo", both, voiceMessage{MediaType: "photo"}, false},
		{"no media", both, voiceMessage{}, false},
	} {
		kind, ok := voiceSourceKind(tc.rule, tc.msg)
		if ok != tc.ok || (ok && kind != tc.msg.MediaType) {
			t.Errorf("%s: kind=%q ok=%v, want ok=%v", tc.name, kind, ok, tc.ok)
		}
	}
}

// TestVoiceSourceIncoming: a new message of the stream is queued after it is
// saved, by the chat rules.
func TestVoiceSourceIncoming(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	n := rpc.EventMessageNew
	f.event(n, 1001, 1, "voice", false)      // personal chat
	f.event(n, 1001, 2, "video_note", false) // personal chat
	f.event(n, 1002, 1, "voice", false)      // marked chat
	f.event(n, 1003, 1, "voice", false)      // no mark
	f.event(n, 1001, 3, "voice", true)       // own
	f.event(n, 1001, 4, "video", false)      // personal chat, video needs a mark
	f.event(n, 1002, 2, "video", false)      // mark without video
	f.event(n, 1006, 1, "video", false)      // mark with video
	f.event(n, 1004, 1, "voice", false)      // closed chat
	f.event(n, 1005, 1, "voice", false)      // a bot is not a personal chat
	f.event(n, 1001, 5, "photo", false)      // not voice
	f.event(n, 1001, 6, "", false)           // a text
	f.event(rpc.EventMessageEdit, 1001, 7, "voice", false)

	for _, want := range []struct {
		chat int64
		id   int
		kind string
	}{{1001, 1, "voice"}, {1001, 2, "video_note"}, {1002, 1, "voice"}, {1006, 1, "video"}} {
		j := f.job(want.chat, want.id)
		if j.Kind != want.kind || j.Source != store.VoiceSourceIncoming || j.Priority != store.VoicePriorityIncoming || j.State != store.VoiceStateQueued {
			t.Errorf("job %d/%d = %+v", want.chat, want.id, j)
		}
	}
	for _, key := range [][2]int64{{1003, 1}, {1001, 3}, {1001, 4}, {1002, 2}, {1004, 1}, {1005, 1}, {1001, 5}, {1001, 6}, {1001, 7}} {
		f.noJob(key[0], int(key[1]))
	}
	if c := f.counts(); c.Queued != 4 {
		t.Fatalf("queued = %d, want 4", c.Queued)
	}
	// The same event twice is one job.
	f.event(n, 1001, 1, "voice", false)
	if c := f.counts(); c.Queued != 4 {
		t.Fatalf("a repeated event made a second job: %+v", c)
	}
	f.untouched()
}

// TestVoiceSourceIncomingNeedsTheSavedMessage: the job is queued after the
// message is stored, so the worker finds the row.
func TestVoiceSourceIncomingNeedsTheSavedMessage(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	f.event(rpc.EventMessageNew, 1001, 9, "voice", false)
	if _, err := f.db.GetMessage(context.Background(), 1001, 9); err != nil {
		t.Fatalf("the message is not saved: %v", err)
	}
	f.job(1001, 9)
}

// TestVoiceSourceMarkChangesTheRule: removing a mark stops new jobs, and a
// mark with video starts taking videos.
func TestVoiceSourceMarkChangesTheRule(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	ctx := context.Background()
	f.event(rpc.EventMessageNew, 1002, 1, "voice", false)
	if _, err := f.d.chatVoiceOff(ctx, params(t, rpc.ChatVoiceOffParams{Chat: "1002"})); err != nil {
		t.Fatal(err)
	}
	f.event(rpc.EventMessageNew, 1002, 2, "voice", false)
	f.noJob(1002, 2)
	if f.job(1002, 1).State != store.VoiceStateQueued {
		t.Fatal("removing a mark changed a job already queued")
	}
	if _, err := f.d.chatVoiceOn(ctx, params(t, rpc.ChatVoiceOnParams{Chat: "1002", Video: true})); err != nil {
		t.Fatal(err)
	}
	f.event(rpc.EventMessageNew, 1002, 3, "video", false)
	f.job(1002, 3)
}

// stubSyncTelegram answers history pages from a script and counts the calls.
type stubSyncTelegram struct {
	*voiceFake
	pages []rpc.HistoryPage
	calls int
}

func (s *stubSyncTelegram) HistoryPage(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
	s.calls++
	if s.calls > len(s.pages) {
		return rpc.HistoryPage{Empty: true}, nil
	}
	return s.pages[s.calls-1], nil
}

// timerCount is the number of timers the test clock waits to fire.
func (c *schedClock) timerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// runSync starts a history sync of a chat on a script of pages and waits for it
// to end. The clock of the sync starts at voiceT0 and moves only to fire the
// pause between two pages.
func (f *voiceFixture) runSync(chat int64, pages ...rpc.HistoryPage) *stubSyncTelegram {
	f.t.Helper()
	tg := &stubSyncTelegram{voiceFake: f.tg, pages: pages}
	f.d.opts.Telegram = tg
	// The sync command resolves the chat itself (it did before the voice
	// sources); that call is not counted against the sources.
	f.tg.resolved = &rpc.Chat{ID: chat, Type: "user"}
	defer func() { f.tg.resolveCalls = 0 }()
	clock := newSchedClock(voiceT0)
	f.d.sync = newHistorySyncManager(f.root, f.d, clock)
	f.t.Cleanup(f.d.sync.close)
	if _, err := f.d.historySyncStart(context.Background(), params(f.t, rpc.HistorySyncParams{Chat: strconv.FormatInt(chat, 10)})); err != nil {
		f.t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		if job := f.d.sync.snapshot(chat); job != nil {
			switch job.State {
			case rpc.HistorySyncDone:
				return tg
			case rpc.HistorySyncFailed, rpc.HistorySyncCancelled:
				f.t.Fatalf("sync ended: %+v", job)
			}
		}
		if clock.timerCount() > 0 {
			clock.AdvanceTo(clock.Now().Add(historySyncPause))
		}
		select {
		case <-deadline:
			f.t.Fatalf("the sync did not finish: %+v timers=%d", f.d.sync.snapshot(chat), clock.timerCount())
		default:
			runtime.Gosched()
		}
	}
}

func syncMedia(chat int64, id int, media string, out bool, age time.Duration) rpc.Message {
	return rpc.Message{ID: id, ChatID: chat, Date: voiceT0.Add(-age).UTC().Format(time.RFC3339), MediaType: media, Out: out, Text: "x"}
}

func historyPage(messages ...rpc.Message) rpc.HistoryPage {
	page := rpc.HistoryPage{Messages: messages, RawNextOffsetID: messages[0].ID, OldestID: messages[0].ID}
	for _, m := range messages {
		page.RawNextOffsetID = min(page.RawNextOffsetID, m.ID)
		page.OldestID = page.RawNextOffsetID
	}
	return page
}

const day = 24 * time.Hour

// TestVoiceSourceSync: the messages a history sync saves are queued by the chat
// rules when they are younger than 90 days, and nothing else is asked of Telegram.
func TestVoiceSourceSync(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	tg := f.runSync(1001,
		historyPage(
			syncMedia(1001, 12, "voice", false, 1*day),       // recent
			syncMedia(1001, 11, "video_note", false, 89*day), // inside the limit
			syncMedia(1001, 10, "voice", false, 91*day),      // too old
			syncMedia(1001, 9, "voice", true, 1*day),         // own
			syncMedia(1001, 8, "video", false, 1*day),        // video needs a mark
			syncMedia(1001, 7, "photo", false, 1*day),
		),
		historyPage(syncMedia(1001, 6, "voice", false, 400*day), syncMedia(1001, 5, "voice", false, 2*day)),
	)
	for _, id := range []int{12, 11, 5} {
		j := f.job(1001, id)
		if j.Source != store.VoiceSourceSync || j.Priority != store.VoicePrioritySync || j.State != store.VoiceStateQueued {
			t.Errorf("job %d = %+v", id, j)
		}
	}
	for _, id := range []int{10, 9, 8, 7, 6} {
		f.noJob(1001, id)
	}
	if c := f.counts(); c.Queued != 3 {
		t.Fatalf("queued = %d, want 3", c.Queued)
	}
	// Only the history pages were asked for: two with messages and the empty one.
	if tg.calls != 3 {
		t.Fatalf("history requests = %d, want 3", tg.calls)
	}
	f.untouched()
}

// TestVoiceSourceSyncMarkedChatAndDuplicates: a marked chat is queued, a second
// sync of the same messages makes no second job and keeps what the worker did.
func TestVoiceSourceSyncMarkedChatAndDuplicates(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	page := historyPage(syncMedia(1002, 2, "voice", false, day), syncMedia(1002, 1, "video", false, day))
	f.runSync(1002, page)
	f.job(1002, 2)
	f.noJob(1002, 1)
	// The job runs and ends; a second sync does not queue it again.
	f.w.recover(f.root)
	f.drain()
	if got := f.job(1002, 2); got.State != store.VoiceStateDone {
		t.Fatalf("job = %+v", got)
	}
	before := f.counts()
	f.runSync(1002, page)
	if after := f.counts(); after != before {
		t.Fatalf("a second sync changed the queue: %+v -> %+v", before, after)
	}
	// An incoming job already in the queue keeps its higher priority.
	f.event(rpc.EventMessageNew, 1002, 30, "voice", false)
	f.runSync(1002, historyPage(syncMedia(1002, 30, "voice", false, day)))
	if j := f.job(1002, 30); j.Source != store.VoiceSourceIncoming || j.Priority != store.VoicePriorityIncoming {
		t.Fatalf("the sync lowered the job: %+v", j)
	}
}

// TestVoiceSourceSyncClosedChat: a chat access.json closes gets no job, even if
// its messages reach the saving code.
func TestVoiceSourceSyncClosedChat(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	saved := []store.Message{{ChatID: 1004, ID: 1, Date: voiceT0.Unix(), MediaType: strPtr("voice")}}
	f.d.voiceSyncPage(context.Background(), 1004, saved, voiceT0)
	f.noJob(1004, 1)
	if _, err := f.d.historySyncStart(context.Background(), params(t, rpc.HistorySyncParams{Chat: "1004"})); err == nil {
		t.Fatal("a sync of a closed chat started")
	}
	// The same message in an open chat is queued.
	saved[0].ChatID = 1001
	f.d.voiceSyncPage(context.Background(), 1001, saved, voiceT0)
	f.job(1001, 1)
}

// TestVoiceSourceOffQueuesNothing is the invariant: without the setting no source
// queues a job, and none asks Telegram for anything beyond what the event or the
// sync did itself.
func TestVoiceSourceOffQueuesNothing(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.Enabled = false
	f := sourcesFixture(t, cfg)
	f.event(rpc.EventMessageNew, 1001, 1, "voice", false)
	f.event(rpc.EventMessageNew, 1002, 1, "video_note", false)
	tg := f.runSync(1001, historyPage(syncMedia(1001, 12, "voice", false, day)))
	if tg.calls != 2 {
		t.Fatalf("history requests = %d, want the page and the empty one", tg.calls)
	}
	if c := f.counts(); c != (store.VoiceQueueCounts{}) {
		t.Fatalf("the queue is not empty: %+v", c)
	}
	// The mark commands work, and a backfill is refused with the mark kept.
	if _, err := f.d.chatVoiceOn(context.Background(), params(t, rpc.ChatVoiceOnParams{Chat: "1003"})); err != nil {
		t.Fatal(err)
	}
	f.untouched()
	if f.w.started {
		t.Fatal("the worker started with the setting off")
	}
}

func chatVoiceOn(t *testing.T, f *voiceFixture, p rpc.ChatVoiceOnParams) (rpc.ChatVoiceOnResult, error) {
	t.Helper()
	v, err := f.d.chatVoiceOn(context.Background(), params(t, p))
	if err != nil {
		return rpc.ChatVoiceOnResult{}, err
	}
	return v.(rpc.ChatVoiceOnResult), nil
}

func chatVoiceList(t *testing.T, f *voiceFixture) rpc.ChatVoiceListResult {
	t.Helper()
	v, err := f.d.chatVoiceList(context.Background(), params(t, rpc.ChatVoiceListParams{}))
	if err != nil {
		t.Fatal(err)
	}
	return v.(rpc.ChatVoiceListResult)
}

// TestVoiceSourceMarks: on, off and list, the video flag of a repeated mark,
// references, and the closed and unknown chats.
func TestVoiceSourceMarks(t *testing.T) {
	f := sourcesFixture(t, voiceTestConfig())
	list := chatVoiceList(t, f)
	if !list.Enabled || len(list.Chats) != 2 || list.Chats[0].ChatID != 1002 || list.Chats[0].Title != "Marked" || list.Chats[0].Video || list.Chats[1].ChatID != 1006 || !list.Chats[1].Video {
		t.Fatalf("list = %+v", list)
	}
	f.clock.AdvanceTo(voiceT0.Add(time.Hour))
	res, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "@annabel"})
	if err != nil || res.ChatID != 1001 || res.Video || !res.Enabled || res.EnabledAt != voiceT0.Add(time.Hour).Unix() || res.Queued != 0 || res.Candidates != 0 {
		t.Fatalf("on = %+v %v", res, err)
	}
	f.clock.AdvanceTo(voiceT0.Add(2 * time.Hour))
	// A second mark with video changes the flag and keeps the first time.
	res, err = chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1001", Video: true})
	if err != nil || !res.Video || res.EnabledAt != voiceT0.Add(time.Hour).Unix() {
		t.Fatalf("second on = %+v %v", res, err)
	}
	if rule, _ := f.db.VoiceChatRule(context.Background(), 1001); !rule.Marked || !rule.Video || !rule.Private {
		t.Fatalf("rule = %+v", rule)
	}
	res, err = chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1001"})
	if err != nil || res.Video {
		t.Fatalf("on without video = %+v %v", res, err)
	}
	list = chatVoiceList(t, f)
	if len(list.Chats) != 3 || list.Chats[0].ChatID != 1001 || list.Chats[0].Username != "annabel" || list.Chats[0].Video {
		t.Fatalf("list = %+v", list)
	}
	off, err := f.d.chatVoiceOff(context.Background(), params(t, rpc.ChatVoiceOffParams{Chat: "@annabel"}))
	if err != nil || off.(rpc.ChatVoiceOffResult) != (rpc.ChatVoiceOffResult{ChatID: 1001, Removed: true}) {
		t.Fatalf("off = %+v %v", off, err)
	}
	off, err = f.d.chatVoiceOff(context.Background(), params(t, rpc.ChatVoiceOffParams{Chat: "1001"}))
	if err != nil || off.(rpc.ChatVoiceOffResult) != (rpc.ChatVoiceOffResult{ChatID: 1001, Removed: false}) {
		t.Fatalf("second off = %+v %v", off, err)
	}
	if got := chatVoiceList(t, f); len(got.Chats) != 2 {
		t.Fatalf("list after off = %+v", got)
	}
	// Errors: usage, a closed chat answers like an unknown one, an unknown chat.
	if _, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{}); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("no chat: %v", err)
	}
	if _, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1004"}); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed: %v", err)
	}
	if _, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "@nobody"}); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("unknown username: %v", err)
	}
	if _, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "999999"}); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := f.d.chatVoiceOff(context.Background(), params(t, rpc.ChatVoiceOffParams{Chat: "1004"})); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("off on a closed chat: %v", err)
	}
	if rule, _ := f.db.VoiceChatRule(context.Background(), 999999); rule.Marked {
		t.Fatal("an unknown chat was marked")
	}
	// A mark of a chat that is closed later is not listed.
	if _, err := f.db.GetVoiceChat(context.Background(), 1004); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a closed chat was marked")
	}
	if err := f.db.SetVoiceChat(context.Background(), 1004, false, voiceT0); err != nil {
		t.Fatal(err)
	}
	for _, c := range chatVoiceList(t, f).Chats {
		if c.ChatID == 1004 {
			t.Fatal("the list shows a closed chat")
		}
	}
	f.untouched()
}

// chatBackfillFixture adds saved messages to the chat 1002 (marked) and 1001.
func chatBackfillFixture(t *testing.T, cfg VoiceTranscriptionConfig) *voiceFixture {
	t.Helper()
	f := sourcesFixture(t, cfg)
	ctx := context.Background()
	f.seed(1002, 1, store.VoiceKindVoice, 100, 3)     // candidate
	f.seed(1002, 2, store.VoiceKindVideoNote, 100, 3) // candidate
	f.seed(1002, 3, store.VoiceKindVideo, 100, 3)     // candidate only with video
	f.seed(1002, 4, store.VoiceKindVoice, 100, 3)     // deleted
	f.seed(1002, 5, store.VoiceKindVoice, 100, 3)     // has a text
	f.seed(1002, 6, store.VoiceKindVoice, 100, 3)     // has a job
	f.seed(1002, 7, store.VoiceKindVoice, 100, 3)     // own
	f.seed(1001, 1, store.VoiceKindVoice, 100, 3)     // another chat
	if _, err := f.db.MarkMessagesDeleted(ctx, 1002, []int{4}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.PutTranscription(ctx, store.Transcription{ChatID: 1002, MessageID: 5, Text: "done", Source: store.TranscriptionSourceTelegram}); err != nil {
		t.Fatal(err)
	}
	f.queue(1002, 6, store.VoiceKindVoice, store.VoiceSourceIncoming, store.VoicePriorityIncoming)
	own, media := true, store.VoiceKindVoice
	if err := f.db.UpsertMessages(ctx, []store.Message{{ChatID: 1002, ID: 7, Date: voiceT0.Unix(), MediaType: &media, Out: &own, Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestVoiceSourceChatBackfill: --backfill queues the saved messages of the chat
// that have no text and no job, and returns the number.
func TestVoiceSourceChatBackfill(t *testing.T) {
	f := chatBackfillFixture(t, voiceTestConfig())
	res, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1002", Backfill: true})
	if err != nil || res.Candidates != 2 || res.Queued != 2 || res.Video {
		t.Fatalf("backfill = %+v %v", res, err)
	}
	for id, kind := range map[int]string{1: "voice", 2: "video_note"} {
		j := f.job(1002, id)
		if j.Kind != kind || j.Source != store.VoiceSourceBackfill || j.Priority != store.VoicePriorityBackfill || j.State != store.VoiceStateQueued {
			t.Errorf("job %d = %+v", id, j)
		}
	}
	for _, id := range []int{3, 4, 5, 7} {
		f.noJob(1002, id)
	}
	f.noJob(1001, 1)
	if j := f.job(1002, 6); j.Priority != store.VoicePriorityIncoming {
		t.Fatalf("an earlier job changed: %+v", j)
	}
	// Videos only with the flag; a repeat finds nothing else.
	res, err = chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1002", Video: true, Backfill: true})
	if err != nil || res.Candidates != 1 || res.Queued != 1 || !res.Video {
		t.Fatalf("backfill with video = %+v %v", res, err)
	}
	if j := f.job(1002, 3); j.Kind != "video" || j.Source != store.VoiceSourceBackfill {
		t.Fatalf("video job = %+v", j)
	}
	res, err = chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1002", Video: true, Backfill: true})
	if err != nil || res.Candidates != 0 || res.Queued != 0 {
		t.Fatalf("repeated backfill = %+v %v", res, err)
	}
	f.untouched()
}

// TestVoiceSourceChatBackfillRefusals: with the setting off the mark is set and
// the backfill is refused with code 2; a missing engine refuses it too.
func TestVoiceSourceChatBackfillRefusals(t *testing.T) {
	cfg := voiceTestConfig()
	cfg.Enabled = false
	f := chatBackfillFixture(t, cfg)
	res, err := chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1002", Video: true})
	if err != nil || res.Enabled || res.Queued != 0 {
		t.Fatalf("mark while off = %+v %v", res, err)
	}
	_, err = chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1002", Backfill: true})
	if writeCode(t, err) != rpc.CodeInvalidParams || errorReason(t, err) != "disabled" {
		t.Fatalf("backfill while off: %v", err)
	}
	if c := f.counts(); c.Queued != 1 { // only the job the fixture put there
		t.Fatalf("a job was queued while off: %+v", c)
	}
	if rule, _ := f.db.VoiceChatRule(context.Background(), 1002); !rule.Marked || rule.Video {
		t.Fatalf("the mark after the refusal = %+v", rule)
	}
	f.w.cfg.Enabled = true
	f.eng.setMissing("model")
	_, err = chatVoiceOn(t, f, rpc.ChatVoiceOnParams{Chat: "1002", Backfill: true})
	if writeCode(t, err) != rpc.CodeInvalidParams || errorReason(t, err) != "engine_missing" {
		t.Fatalf("backfill without the engine: %v", err)
	}
	if c := f.counts(); c.Queued != 1 {
		t.Fatalf("a job was queued without the engine: %+v", c)
	}
}

// TestVoiceSourcePersonalBackfillManyChats: `voice backfill` reads one query, so
// more than 500 chats and more than one page of messages are all seen.
func TestVoiceSourcePersonalBackfillManyChats(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.holdWorker()
	ctx := context.Background()
	const personal = 520
	var chats []store.Chat
	for i := 0; i < personal; i++ {
		chats = append(chats, store.Chat{ID: int64(2000 + i), Type: "user", Title: "p"})
	}
	chats = append(chats, store.Chat{ID: 1, Type: "user", Title: "closed"}, store.Chat{ID: -7, Type: "supergroup", Title: "group"})
	if err := f.db.UpsertChats(ctx, chats); err != nil {
		t.Fatal(err)
	}
	var msgs []store.Message
	media, own := store.VoiceKindVoice, false
	for i := 0; i < personal; i++ {
		msgs = append(msgs, store.Message{ChatID: int64(2000 + i), ID: 1, Date: voiceT0.Unix(), MediaType: &media, Out: &own, Raw: []byte(`{}`)})
	}
	// A second message in one chat, the closed chat and a group.
	msgs = append(msgs,
		store.Message{ChatID: 2000, ID: 2, Date: voiceT0.Unix(), MediaType: &media, Out: &own, Raw: []byte(`{}`)},
		store.Message{ChatID: 1, ID: 1, Date: voiceT0.Unix(), MediaType: &media, Out: &own, Raw: []byte(`{}`)},
		store.Message{ChatID: -7, ID: 1, Date: voiceT0.Unix(), MediaType: &media, Out: &own, Raw: []byte(`{}`)},
	)
	if err := f.db.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	writeAccess(t, f.accessPath, `{"block":[1]}`)
	v, err := f.d.voiceBackfill(ctx, params(t, rpc.VoiceBackfillParams{DryRun: true}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res != (rpc.VoiceBackfillResult{Enabled: true, DryRun: true, Chats: personal, Candidates: personal + 1}) {
		t.Fatalf("dry run = %+v %v", v, err)
	}
	if c := f.counts(); c != (store.VoiceQueueCounts{}) {
		t.Fatalf("a dry run queued jobs: %+v", c)
	}
	v, err = f.d.voiceBackfill(ctx, params(t, rpc.VoiceBackfillParams{}))
	if res := v.(rpc.VoiceBackfillResult); err != nil || res.Candidates != personal+1 || res.Queued != personal+1 {
		t.Fatalf("backfill = %+v %v", v, err)
	}
	if c := f.counts(); c.Queued != personal+1 {
		t.Fatalf("queued = %+v", c)
	}
	f.noJob(1, 1)
	f.noJob(-7, 1)
	f.job(2519, 1)
	f.job(2000, 2)
	f.untouched()
}
