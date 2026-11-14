package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestIdempotencyLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	rec := IdempotencyRecord{ChatID: 42, Key: "key", Method: "send", ParamsHash: []byte("hash"), CreatedAt: now}
	got, inserted, err := db.ReserveIdempotency(ctx, rec)
	if err != nil || !inserted || got.Result != nil {
		t.Fatalf("reserve: %+v %v %v", got, inserted, err)
	}
	other := rec
	other.ParamsHash = []byte("different")
	got, inserted, err = db.ReserveIdempotency(ctx, other)
	if err != nil || inserted || string(got.ParamsHash) != "hash" {
		t.Fatalf("conflict: %+v %v %v", got, inserted, err)
	}
	ok, err := db.CompleteIdempotency(ctx, 42, "key", "send", other.ParamsHash, json.RawMessage(`{"id":1}`))
	if err != nil || ok {
		t.Fatalf("mismatch: %v %v", ok, err)
	}
	ok, err = db.CompleteIdempotency(ctx, 42, "key", "send", rec.ParamsHash, json.RawMessage(`{"id":1}`))
	if err != nil || !ok {
		t.Fatalf("complete: %v %v", ok, err)
	}
	got, found, err := db.GetIdempotency(ctx, 42, "key")
	if err != nil || !found || string(got.Result) != `{"id":1}` {
		t.Fatalf("read: %+v %v %v", got, found, err)
	}
	n, err := db.PruneIdempotency(ctx, now.Add(IdempotencyRetention))
	if err != nil || n != 0 {
		t.Fatalf("boundary: %d %v", n, err)
	}
	n, err = db.PruneIdempotency(ctx, now.Add(IdempotencyRetention+time.Second))
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
}
