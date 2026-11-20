package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// sixOClock returns the real instant now on a clock whose local time is 06:00.
// The folder snapshot is dated by the real clock, so the age tests start from
// it; the zone keeps the whole test inside one local day.
func sixOClock() time.Time {
	// Whole seconds: the status reports the next run in whole seconds, and a
	// test moves the clock to that value.
	now := time.Now().UTC().Truncate(time.Second)
	seconds := now.Hour()*3600 + now.Minute()*60 + now.Second()
	return now.In(time.FixedZone("six", 6*3600-seconds))
}

func TestBackgroundScheduleFoldersRefreshByAge(t *testing.T) {
	cfg := schedConfig()
	cfg.WindowStart, cfg.WindowEnd = 0, 24*time.Hour
	start := sixOClock()
	env := newSchedEnv(t, cfg, start)
	for id := int64(1); id <= 4; id++ {
		env.chat(id, "user", int(100+id))
	}
	env.rnd.push(cfg.IntervalMin, cfg.IntervalMax, 40*time.Minute, 40*time.Minute, 4*time.Hour, time.Hour)
	env.start()

	// No snapshot yet: the first run reads the folders.
	env.step()
	if got := env.tg.folderCount(); got != 1 {
		t.Fatalf("folder reads after the first run = %d, want 1", got)
	}
	// The snapshot is about 1 hour 20 minutes old: no read.
	env.step()
	if got := env.tg.folderCount(); got != 1 {
		t.Fatalf("a fresh snapshot was read again: %d", got)
	}
	// 5 hours 20 minutes old: read again.
	env.step()
	if got := env.tg.folderCount(); got != 2 {
		t.Fatalf("a stale snapshot was not read: %d", got)
	}
	// A failed read is only logged, the pick goes on with the old snapshot.
	env.tg.failFolders(errors.New("folders failed"))
	st := env.step()
	if got := env.tg.folderCount(); got != 3 || st.LastResult != rpc.BackgroundSyncDone {
		t.Fatalf("after a failed read: folders=%d status=%+v", got, st)
	}
	if got := env.tg.pageChats(); len(got) != 4 {
		t.Fatalf("every run must load a chat: %v", got)
	}
}

func TestBackgroundScheduleWindowEndStopsFolderRefresh(t *testing.T) {
	env := newSchedEnv(t, schedConfig(), schedAt(0, 18, 0))
	env.chat(1, "user", 100)
	env.rnd.push(40*time.Minute, 3*time.Hour, 60*time.Minute)
	entered := make(chan struct{})
	env.tg.onFolders = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	env.start()
	runAt := schedAt(0, 19, 0)
	awaitCond(t, "the first timer", func() bool { return env.clock.hasTimerAt(runAt) })
	env.clock.AdvanceTo(runAt)
	<-entered
	if st := env.status(); st.State != rpc.BackgroundSyncRunning || st.CurrentChatID != nil {
		t.Fatalf("refreshing: %+v", st)
	}
	st := env.jump(schedAt(0, 20, 0))
	if st.LastResult != rpc.BackgroundSyncSkipped || len(env.tg.pageCalls()) != 0 {
		t.Fatalf("a refresh cut by the window end: %+v pages=%v", st, env.tg.pageCalls())
	}
}

func (e *schedEnv) awaitStopped() {
	e.t.Helper()
	e.cancel()
	stopped := make(chan struct{})
	go func() { e.d.background.wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		e.t.Fatal("the scheduler goroutine did not stop")
	}
}

// TestBackgroundScheduleStopJoinsGoroutine ends the scheduler in each place it
// can wait: for the next run, for the folders and for a load. The goroutine
// ends without a result, and wait returns, so Run can join it.
func TestBackgroundScheduleStopJoinsGoroutine(t *testing.T) {
	t.Run("waiting for the next run", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.start()
		next := time.Unix(*env.status().NextRunAt, 0)
		awaitCond(t, "the timer", func() bool { return env.clock.hasTimerAt(next) })
		env.awaitStopped()
		if st := env.status(); st.LastResult != "" {
			t.Fatalf("stop recorded a result: %+v", st)
		}
	})
	t.Run("reading the folders", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		entered := make(chan struct{})
		env.tg.onFolders = func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		env.start()
		env.clock.AdvanceTo(time.Unix(*env.status().NextRunAt, 0))
		<-entered
		env.awaitStopped()
		if st := env.status(); st.LastResult != "" || len(env.tg.pageCalls()) != 0 {
			t.Fatalf("stop during the refresh: %+v", st)
		}
	})
	t.Run("loading a chat", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		blocked := make(chan struct{})
		env.tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
			close(blocked)
			<-ctx.Done()
			return rpc.HistoryPage{}, ctx.Err()
		}
		env.start()
		env.clock.AdvanceTo(time.Unix(*env.status().NextRunAt, 0))
		<-blocked
		env.awaitStopped()
		if st := env.status(); st.LastResult != "" {
			t.Fatalf("stop during the load: %+v", st)
		}
	})
}

// TestBackgroundScheduleRunStopsWithDaemon starts the real Run: with the setting
// off nothing is scheduled and Telegram sees no history or folder request; with
// it on the scheduler waits for its first run and Run joins it on shutdown.
func TestBackgroundScheduleRunStopsWithDaemon(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "off"
		if enabled {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			cfg := schedConfig()
			cfg.Enabled = enabled
			tg := &schedTelegram{syncTelegram: &syncTelegram{fakeTelegram: newFake()}}
			tg.page = func(_ context.Context, ref chatref.Ref, _ int) (rpc.HistoryPage, error) { return syncPage(ref.ID), nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			listening := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- Run(ctx, Options{Profile: "test", Paths: paths, Telegram: tg, BackgroundSync: cfg, OnListening: func() { close(listening) }})
			}()
			select {
			case <-listening:
			case err := <-finished:
				t.Fatalf("daemon exited early: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("daemon did not start")
			}
			client, err := rpc.Dial(context.Background(), "unix", paths.Socket)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var st rpc.StatusResult
			if err := client.Call(ctxTimeout(t), rpc.MethodStatus, nil, &st); err != nil {
				t.Fatal(err)
			}
			block := st.BackgroundSync
			if block.Enabled != enabled || block.Window != (rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"}) {
				t.Fatalf("status block: %+v", block)
			}
			if enabled {
				if block.State != rpc.BackgroundSyncWaitingWindow && block.State != rpc.BackgroundSyncWaitingInterval || block.NextRunAt == nil {
					t.Fatalf("enabled block: %+v", block)
				}
			} else if block.State != rpc.BackgroundSyncDisabled || block.NextRunAt != nil {
				t.Fatalf("disabled block: %+v", block)
			}
			var sd rpc.ShutdownResult
			if err := client.Call(ctxTimeout(t), rpc.MethodShutdown, nil, &sd); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("daemon did not stop")
			}
			if calls := tg.pageCalls(); len(calls) != 0 || tg.folderCount() != 0 {
				t.Fatalf("the scheduler made requests: pages=%v folders=%d", calls, tg.folderCount())
			}
		})
	}
}

func TestDaemonStatusBackgroundSyncStates(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		cfg := schedConfig()
		cfg.Enabled = false
		env := newSchedEnv(t, cfg, schedAt(0, 9, 0))
		env.start()
		st := env.status()
		if st.Enabled || st.State != rpc.BackgroundSyncDisabled || st.NextRunAt != nil || st.LastRunAt != nil || st.CurrentChatID != nil || st.LastResult != "" {
			t.Fatalf("disabled: %+v", st)
		}
	})
	t.Run("waiting for the window", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 5, 0))
		env.start()
		st := env.status()
		if !st.Enabled || st.State != rpc.BackgroundSyncWaitingWindow || !samePtr(st.NextRunAt, unixPtr(schedAt(0, 8, 0))) {
			t.Fatalf("before the window: %+v", st)
		}
		if st.Window != (rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"}) {
			t.Fatalf("window: %+v", st.Window)
		}
	})
	t.Run("waiting for the interval", func(t *testing.T) {
		from := schedAt(0, 9, 0)
		cfg := schedConfig()
		env := newSchedEnv(t, cfg, from)
		// The real random function: the next run lies in the window and inside
		// the interval bounds.
		for range 50 {
			ctx, cancel := context.WithCancel(context.Background())
			s := newBackgroundSync(env.d, cfg, env.clock, nil)
			s.start(ctx)
			st := s.status()
			cancel()
			s.wait()
			next := time.Unix(*st.NextRunAt, 0)
			if st.State != rpc.BackgroundSyncWaitingInterval || next.Before(from.Add(cfg.IntervalMin)) || next.After(from.Add(cfg.IntervalMax)) || !s.inWindow(next) {
				t.Fatalf("waiting for the interval: %+v next=%s", st, next)
			}
		}
	})
	t.Run("running", func(t *testing.T) {
		env := newSchedEnv(t, schedConfig(), schedAt(0, 9, 0))
		env.chat(1, "user", 100)
		blocked := make(chan struct{})
		env.tg.page = func(ctx context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
			close(blocked)
			<-ctx.Done()
			return rpc.HistoryPage{}, ctx.Err()
		}
		env.start()
		env.clock.AdvanceTo(time.Unix(*env.status().NextRunAt, 0))
		<-blocked
		st := env.status()
		if st.State != rpc.BackgroundSyncRunning || st.CurrentChatID == nil || *st.CurrentChatID != 1 || st.NextRunAt != nil || st.LastRunAt != nil {
			t.Fatalf("running: %+v", st)
		}
	})
}

// TestDaemonStatusBackgroundSyncJSON checks the shape of the block in the daemon
// status: it is always there, and the empty fields are null, not missing.
func TestDaemonStatusBackgroundSyncJSON(t *testing.T) {
	cfg := schedConfig()
	cfg.Enabled = false
	env := newSchedEnv(t, cfg, schedAt(0, 9, 0))
	env.d.login = newLoginManager(context.Background(), env.tg)
	result, err := env.d.status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	const want = `{"enabled":false,"window":{"start":"08:00","end":"20:00"},"state":"disabled","next_run_at":null,"last_run_at":null,"last_result":"","last_chat_id":null,"current_chat_id":null}`
	if got := string(fields["background_sync"]); got != want {
		t.Fatalf("background_sync = %s, want %s", got, want)
	}
}
