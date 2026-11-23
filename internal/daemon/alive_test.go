package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestAliveConditionsAndPulse(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFake()
	d := &daemon{store: db, log: slog.New(slog.DiscardHandler), opts: Options{Telegram: f}}
	ctx := context.Background()
	d.account.set(&rpc.User{ID: 1})
	d.writeAlive(ctx, time.Unix(100, 0))
	if _, found, _ := db.LastAliveAt(ctx); found {
		t.Fatal("unready client wrote heartbeat")
	}
	close(f.ready)
	d.account.set(nil)
	d.writeAlive(ctx, time.Unix(101, 0))
	if _, found, _ := db.LastAliveAt(ctx); found {
		t.Fatal("unauthorized client wrote heartbeat")
	}
	d.account.set(&rpc.User{ID: 1})
	d.setAuthLost("lost")
	d.writeAlive(ctx, time.Unix(102, 0))
	if _, found, _ := db.LastAliveAt(ctx); found {
		t.Fatal("auth lost wrote heartbeat")
	}
	d.setAuthLost("")
	clock := newSyncTestClock()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); d.runAlive(runCtx, clock) }()
	select {
	case <-clock.created:
	case <-time.After(time.Second):
		t.Fatal("heartbeat timer missing")
	}
	if n, found, err := db.LastAliveAt(ctx); err != nil || !found || n != 100 {
		t.Fatalf("initial: %d %v %v", n, found, err)
	}
	clock.Advance(time.Minute)
	select {
	case <-clock.created:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not repeat")
	}
	if n, _, err := db.LastAliveAt(ctx); err != nil || n != 160 {
		t.Fatalf("pulse: %d %v", n, err)
	}
	cancel()
	<-done
	d.writeAlive(ctx, time.Unix(170, 0))
	if n, _, _ := db.LastAliveAt(ctx); n != 170 {
		t.Fatalf("final: %d", n)
	}
}

func TestAliveStartsAfterLogin(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.SetLastAliveAt(ctx, 7); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	close(f.ready)
	d := &daemon{store: db, log: slog.New(slog.DiscardHandler), opts: Options{Telegram: f}}
	clock := newSyncTestClock()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); d.runAlive(runCtx, clock) }()
	<-clock.created
	if n, _, _ := db.LastAliveAt(ctx); n != 7 {
		t.Fatalf("changed without login: %d", n)
	}
	d.account.set(&rpc.User{ID: 1})
	clock.Advance(time.Minute)
	select {
	case <-clock.created:
	case <-time.After(time.Second):
		t.Fatal("no pulse")
	}
	if n, _, _ := db.LastAliveAt(ctx); n != 160 {
		t.Fatalf("login heartbeat: %d", n)
	}
	cancel()
	<-done
}

func TestAliveStartupSnapshot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	d := &daemon{store: db}
	if err := d.loadLastAlive(ctx, time.Unix(2000, 0)); err != nil || d.catchupSince != nil {
		t.Fatalf("first startup: %v %v", d.catchupSince, err)
	}
	if err := db.SetLastAliveAt(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if err := d.loadLastAlive(ctx, time.Unix(2000, 0)); err != nil || d.downtime != 1000*time.Second || d.catchupSince == nil || d.catchupSince.Unix() != 400 {
		t.Fatalf("snapshot: %v %v %v", d.downtime, d.catchupSince, err)
	}
	if err := db.SetLastAliveAt(ctx, 1900); err != nil {
		t.Fatal(err)
	}
	if d.catchupSince.Unix() != 400 {
		t.Fatal("snapshot moved after heartbeat")
	}
}
