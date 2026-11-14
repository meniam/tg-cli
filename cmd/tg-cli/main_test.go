package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStateKVUsesProfileDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	kv := &stateKV{path: path}
	ctx := context.Background()
	if value, found, err := kv.Get(ctx, "updates.state.42"); err != nil || found || value != nil {
		t.Errorf("missing value: %s %v %v", value, found, err)
	}
	if err := kv.Set(ctx, "updates.state.42", json.RawMessage(`{"Pts":7}`)); err != nil {
		t.Fatal(err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	value, found, err := kv.Get(ctx, "updates.state.42")
	if err != nil || !found || string(value) != `{"Pts":7}` {
		t.Errorf("reopened value: %s %v %v", value, found, err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
}
