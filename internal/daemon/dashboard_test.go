package daemon

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
)

// dashboardTelegram reports a scripted connection, flood pause and throttle
// queue, and records every method that would be a request to Telegram.
type dashboardTelegram struct {
	*syncTelegram
	conn   rpc.ConnectionInfo
	flood  time.Duration
	queued int

	reqMu    sync.Mutex
	requests []string
}

func (t *dashboardTelegram) record(name string) {
	t.reqMu.Lock()
	t.requests = append(t.requests, name)
	t.reqMu.Unlock()
}

func (t *dashboardTelegram) requested() []string {
	t.reqMu.Lock()
	defer t.reqMu.Unlock()
	return append([]string(nil), t.requests...)
}

func (t *dashboardTelegram) Connection() rpc.ConnectionInfo { return t.conn }
func (t *dashboardTelegram) FloodWaitRemaining() time.Duration {
	return t.flood
}
func (t *dashboardTelegram) ThrottleQueued() int { return t.queued }
func (t *dashboardTelegram) Self(ctx context.Context) (*rpc.User, error) {
	t.record("Self")
	return t.syncTelegram.Self(ctx)
}
func (t *dashboardTelegram) Chats(ctx context.Context, limit int, chatType string) ([]rpc.Chat, error) {
	t.record("Chats")
	return t.syncTelegram.Chats(ctx, limit, chatType)
}
func (t *dashboardTelegram) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	t.record("Resolve")
	return t.syncTelegram.Resolve(ctx, ref)
}
func (t *dashboardTelegram) ChatInfo(ctx context.Context, ref chatref.Ref) (rpc.ChatInfo, error) {
	t.record("ChatInfo")
	return t.syncTelegram.ChatInfo(ctx, ref)
}
func (t *dashboardTelegram) History(ctx context.Context, ref chatref.Ref, q rpc.HistoryQuery) (rpc.MessagesResult, error) {
	t.record("History")
	return t.syncTelegram.History(ctx, ref, q)
}

// dashboardFixture is a daemon whose database holds:
//
//	people 1 (complete), 2          folders 10 (include in a folder)
//	others 11, 12 (complete)        messages 1: two, 2: one, 10: one, 11: one, 12: one
//
// and four events, three of them inside the last hour.
func dashboardFixture(t *testing.T) (*daemon, *dashboardTelegram, *store.DB, string) {
	t.Helper()
	d, base, db, _, accessPath := syncFixture(t)
	tg := &dashboardTelegram{syncTelegram: base, conn: rpc.ConnectionInfo{Connection: "ready", Transport: "tcp", DC: 2, Reconnects: 3}}
	d.opts.Telegram = tg
	d.opts.Profile = "work"
	d.full = newChatFullSyncManager(context.Background(), d, nil)
	t.Cleanup(d.full.close)
	d.account.set(&rpc.User{ID: 42, Username: "me", FirstName: "Me"})
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{
		{ID: 1, Type: "user", Title: "Ann"}, {ID: 2, Type: "user", Title: "Bob"},
		{ID: 10, Type: "channel", Title: "Folder channel"},
		{ID: 11, Type: "group", Title: "Other"}, {ID: 12, Type: "supergroup", Title: "Done other"},
	}); err != nil {
		t.Fatal(err)
	}
	pages := []struct {
		chat     int64
		ids      []int
		complete bool
	}{{1, []int{1, 2}, true}, {2, []int{3}, false}, {10, []int{4}, false}, {11, []int{5}, false}, {12, []int{6}, true}}
	for _, p := range pages {
		var messages []store.Message
		for _, id := range p.ids {
			messages = append(messages, store.Message{ChatID: p.chat, ID: id, Date: int64(1000 + id), Raw: []byte(`{}`)})
		}
		if _, err := db.SaveHistoryPage(ctx, p.chat, messages, nil, p.complete); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ReplaceFolders(ctx, []rpc.Folder{{ID: 1, Kind: "filter", Include: []rpc.FolderPeer{{ID: 10}}}}, store.ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, age := range []time.Duration{time.Minute, 20 * time.Minute, 59 * time.Minute, 3 * time.Hour} {
		if _, _, err := db.AddEvent(ctx, store.Event{Type: "message.new", CreatedAt: now.Add(-age), Payload: json.RawMessage(`{}`), DedupKey: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	return d, tg, db, accessPath
}

func callDashboard(t *testing.T, d *daemon) rpc.Dashboard {
	t.Helper()
	got, err := d.dashboard(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := got.(rpc.Dashboard)
	if !ok {
		t.Fatalf("dashboard returned %T", got)
	}
	return out
}

func TestDashboardAnswersEveryBlock(t *testing.T) {
	d, tg, db, accessPath := dashboardFixture(t)
	d.startedAt = time.Unix(1_700_000_000, 0)
	tg.flood = 90*time.Second + time.Millisecond
	tg.queued = 3
	writeAccess(t, accessPath, `{"write":[10]}`)
	proxy, err := netconf.ParseProxy("socks5://test-user:fake-password@localhost:1080")
	if err != nil {
		t.Fatal(err)
	}
	cfg := throttle.DefaultConfig()
	cfg.Percent, cfg.FloodAutoWait = 45, 7*time.Second
	limiter, err := throttle.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.opts.TelegramConfig.Network.Proxy = proxy
	d.opts.TelegramConfig.Throttle = limiter
	d.opts.SemanticSearch.Batch = 12
	d.opts.VoiceTranscription.Threads = 6

	got := callDashboard(t, d)

	if got.Profile != "work" || got.PID != os.Getpid() || got.StartedAt != 1_700_000_000 {
		t.Errorf("process block: %+v", got)
	}
	if got.Account.User == nil || got.Account.User.ID != 42 || got.Account.User.Username != "me" || !got.Account.Authorized {
		t.Errorf("account: %+v", got.Account)
	}
	if got.Connection != (rpc.DashboardConnection{Connection: "ready", Transport: "tcp", DC: 2, Reconnects: 3}) {
		t.Errorf("connection: %+v", got.Connection)
	}
	if got.Access.Enabled || got.Access.Write != 1 || got.Access.Allow != 0 || got.Access.Block != 0 {
		t.Errorf("access: %+v", got.Access)
	}
	info, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	version, err := db.Version()
	if err != nil {
		t.Fatal(err)
	}
	if got.Database.SizeBytes != info.Size() || got.Database.SizeBytes == 0 || got.Database.SchemaVersion != version ||
		got.Database.Chats != 5 || got.Database.Messages != 6 {
		t.Errorf("database: %+v (file %d bytes, schema %d)", got.Database, info.Size(), version)
	}
	wantHistory := rpc.DashboardHistory{
		Complete: 2, Total: 5,
		People:  rpc.DashboardGroup{Complete: 1, Total: 2},
		Folders: rpc.DashboardGroup{Complete: 0, Total: 1},
		Others:  rpc.DashboardGroup{Complete: 1, Total: 2},
	}
	if got.History != wantHistory {
		t.Errorf("history = %+v, want %+v", got.History, wantHistory)
	}
	if !reflect.DeepEqual(got.Background, d.backgroundSyncStatus()) || got.Background.State != rpc.BackgroundSyncDisabled {
		t.Errorf("background: %+v", got.Background)
	}
	if got.Jobs.Sync == nil || len(got.Jobs.Sync) != 0 || got.Jobs.Full.State != rpc.ChatFullIdle {
		t.Errorf("jobs: %+v", got.Jobs)
	}
	if got.Throttle != (rpc.DashboardThrottle{FloodWaitSeconds: 91, Queued: 3}) {
		t.Errorf("throttle: %+v", got.Throttle)
	}
	if got.Events != (rpc.DashboardEvents{LastHour: 3, Total: 4}) {
		t.Errorf("events: %+v", got.Events)
	}
	s := got.Settings
	if s == nil || s.Proxy != "socks5://localhost:1080" || s.Throttle == nil ||
		s.Throttle.Percent != 45 || s.Throttle.FloodAutoWait != "7s" ||
		s.Semantic.Batch != 12 || s.Voice.Threads != 6 {
		t.Errorf("unexpected dashboard settings: %+v", s)
	}
}

// A closed chat is in no counter of the answer: not in the chats, the messages
// or the history groups.
func TestDashboardHidesClosedChats(t *testing.T) {
	d, _, _, accessPath := dashboardFixture(t)
	writeAccess(t, accessPath, `{"block":[1,12]}`)
	got := callDashboard(t, d)
	if !got.Access.Enabled || got.Access.Block != 2 {
		t.Errorf("access: %+v", got.Access)
	}
	if got.Database.Chats != 3 || got.Database.Messages != 3 {
		t.Errorf("database: %+v, want 3 chats and 3 messages", got.Database)
	}
	want := rpc.DashboardHistory{
		Complete: 0, Total: 3,
		People:  rpc.DashboardGroup{Total: 1},
		Folders: rpc.DashboardGroup{Total: 1},
		Others:  rpc.DashboardGroup{Total: 1},
	}
	if got.History != want {
		t.Errorf("history = %+v, want %+v", got.History, want)
	}
	text, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Ann", "Bob", "Folder channel", "Other", "Done other"} {
		if strings.Contains(string(text), title) {
			t.Errorf("the answer carries the chat title %q: %s", title, text)
		}
	}
}

// The dashboard stays available after the login is lost, like the status: the
// request passes the guard and the answer says the account is not signed in.
func TestDashboardAnswersWithoutLogin(t *testing.T) {
	d, _, _, _ := dashboardFixture(t)
	d.setAuthLost("revoked")
	if err := d.guardRequest(rpc.Request{Method: rpc.MethodDashboard}); err != nil {
		t.Fatalf("the guard refuses the dashboard after the login is lost: %v", err)
	}
	got := callDashboard(t, d)
	if got.Account.Authorized {
		t.Errorf("the lost login is reported as authorized: %+v", got.Account)
	}
	if got.Database.Chats != 5 || got.History.Total != 5 {
		t.Errorf("the local data is missing: %+v %+v", got.Database, got.History)
	}

	// No account known at all.
	d.setAuthLost("")
	d.account.set(nil)
	got = callDashboard(t, d)
	if got.Account.User != nil || got.Account.Authorized {
		t.Errorf("an unknown account: %+v", got.Account)
	}
}

// The dashboard reads the daemon and the database only: no call that is a
// request to Telegram, and a client that is not available is reported, not
// waited for.
func TestDashboardMakesNoTelegramRequest(t *testing.T) {
	d, tg, _, _ := dashboardFixture(t)
	callDashboard(t, d)
	if got := tg.requested(); len(got) != 0 {
		t.Fatalf("the dashboard asked Telegram: %v", got)
	}
}

func TestDashboardListsOnlyActiveSyncJobs(t *testing.T) {
	d, tg, _, _ := dashboardFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	tg.syncTelegram.page = func(ctx context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return syncPage(ref.ID, 900), nil
		case <-ctx.Done():
			return rpc.HistoryPage{}, ctx.Err()
		}
	}
	startSync(t, d, 2)
	<-entered
	got := callDashboard(t, d)
	if len(got.Jobs.Sync) != 1 || got.Jobs.Sync[0] != (rpc.DashboardSyncJob{ChatID: 2, State: rpc.HistorySyncRunning, Source: rpc.HistorySyncManual}) {
		t.Fatalf("running job: %+v", got.Jobs.Sync)
	}
	cancelSync(t, d, 2)
	awaitSyncState(t, d, 2, rpc.HistorySyncCancelled)
	close(release)
	if got := callDashboard(t, d); len(got.Jobs.Sync) != 0 {
		t.Fatalf("a cancelled job is listed: %+v", got.Jobs.Sync)
	}
}

// With the scheduler on, the block is the one of the daemon status.
func TestDashboardCarriesTheBackgroundBlock(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 6, 0))
	env.d.full = newChatFullSyncManager(context.Background(), env.d, nil)
	t.Cleanup(env.d.full.close)
	env.rnd.push(40*time.Minute, 3*time.Hour, 90*time.Minute)
	env.start()
	got := callDashboard(t, env.d)
	want := env.status()
	if !reflect.DeepEqual(got.Background, want) || !got.Background.Enabled || got.Background.State != rpc.BackgroundSyncWaitingWindow || got.Background.NextRunAt == nil {
		t.Fatalf("background = %+v, want %+v", got.Background, want)
	}
}

// The method is registered with the others, so a client can call it.
func TestDashboardIsRegistered(t *testing.T) {
	d, _, _, _ := accessTestDaemon(t)
	d.register()
	d.srv.SetEventSource(d.hub, d.replayEvents)
	found := false
	for _, method := range d.srv.Methods() {
		found = found || method == rpc.MethodDashboard
	}
	if !found {
		t.Fatal("the dashboard method is not registered")
	}
	// It is not an account method: the lost login and a missing connection do
	// not stop it, and it needs no Telegram lease.
	if accountMethod(rpc.MethodDashboard) || needsTelegram(rpc.MethodDashboard) {
		t.Fatal("the dashboard must work without a login and without a connection")
	}
}
