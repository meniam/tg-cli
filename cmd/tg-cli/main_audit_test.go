package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"tg-cli/internal/daemon"
	"tg-cli/internal/netconf"
)

func TestAudit01_StateKVOpenFailureIsReturned(t *testing.T) {
	// The directory of the database does not exist, so store.Open cannot create it.
	kv := &stateKV{path: filepath.Join(t.TempDir(), "missing", "store.db")}
	ctx := context.Background()
	if value, found, err := kv.Get(ctx, "updates.state.42"); err == nil || found || value != nil {
		t.Errorf("Get: value=%s found=%v err=%v, want an error", value, found, err)
	}
	if err := kv.Set(ctx, "updates.state.42", json.RawMessage(`{"Pts":7}`)); err == nil {
		t.Error("Set: want an error")
	}
	if kv.db != nil {
		t.Error("a failed open must not leave a database behind")
	}
	if err := kv.Close(); err != nil {
		t.Errorf("Close after a failed open: %v", err)
	}
	if err := (&stateKV{}).Close(); err != nil {
		t.Errorf("Close of a never opened state: %v", err)
	}
}

// audit01Config builds a client config that cannot reach the real Telegram: the
// only proxy is a closed loopback port, and the tests below never start a login.
func audit01Config(t *testing.T, statePath string) daemon.TelegramConfig {
	t.Helper()
	proxy, err := netconf.ParseProxy("socks5://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	return daemon.TelegramConfig{
		Network:     netconf.Settings{Transport: netconf.TCP, Proxy: proxy},
		AppID:       1,
		AppHash:     "synthetic-hash",
		SessionPath: filepath.Join(t.TempDir(), "session"),
		StatePath:   statePath,
	}
}

func TestAudit01_ManagedTelegramRunClosesState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "store.db")
	built, err := newTelegram(audit01Config(t, statePath))
	if err != nil {
		t.Fatal(err)
	}
	managed, ok := built.(*managedTelegram)
	if !ok || managed.state == nil {
		t.Fatalf("newTelegram returned %T, want *managedTelegram with a state", built)
	}
	if err := managed.state.Set(context.Background(), "updates.state.42", json.RawMessage(`{"Pts":7}`)); err != nil {
		t.Fatal(err)
	}
	if managed.state.db == nil {
		t.Fatal("the state database was not opened")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run must end at once and must not wait for a network
	if err := managed.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Logf("Run returned %v", err)
	}
	if managed.state.db != nil {
		t.Error("Run left the state database open")
	}
}

// Without a StatePath the client must keep its update cursors in memory. A typed
// nil *stateKV stored in the StateKV interface is not nil, so the client would call
// a nil receiver the first time it reads a cursor.
func TestAudit01_NoStatePathLeavesTheClientStateKVNil(t *testing.T) {
	built, err := newTelegram(audit01Config(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	managed := built.(*managedTelegram)
	if managed.state != nil {
		t.Fatalf("state = %+v, want nil", managed.state)
	}
	field := reflect.ValueOf(managed.Client).Elem().FieldByName("stateKV")
	if !field.IsValid() {
		t.Fatal("tgclient.Client no longer has a stateKV field: update this test")
	}
	if !field.IsNil() {
		t.Fatal("newTelegram stores a typed nil *stateKV in tgclient.Config.StateKV when StatePath is empty: the client would believe it has persistent state")
	}
}
