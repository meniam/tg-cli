package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func audit07OpenStore(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestAudit07_IdempotencyReplayAfterCompletion is the main scenario of the
// contract: a repeated request after the first one finished gets the saved
// result back and does not reserve again.
func TestAudit07_IdempotencyReplayAfterCompletion(t *testing.T) {
	db := audit07OpenStore(t)
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	rec := IdempotencyRecord{ChatID: 42, Key: "key", Method: "send", ParamsHash: []byte("hash"), CreatedAt: now}
	if _, inserted, err := db.ReserveIdempotency(ctx, rec); err != nil || !inserted {
		t.Fatalf("reserve: %v %v", inserted, err)
	}
	result := json.RawMessage(`{"id":1}`)
	if ok, err := db.CompleteIdempotency(ctx, 42, "key", "send", rec.ParamsHash, result); err != nil || !ok {
		t.Fatalf("complete: %v %v", ok, err)
	}

	got, inserted, err := db.ReserveIdempotency(ctx, rec)
	if err != nil || inserted {
		t.Fatalf("replay: inserted=%v err=%v", inserted, err)
	}
	if string(got.Result) != `{"id":1}` || got.Method != "send" || string(got.ParamsHash) != "hash" || got.ChatID != 42 || got.Key != "key" {
		t.Fatalf("replay returned %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("the record changed its creation time: %v", got.CreatedAt)
	}

	// A second completion must not overwrite the saved result.
	ok, err := db.CompleteIdempotency(ctx, 42, "key", "send", rec.ParamsHash, json.RawMessage(`{"id":2}`))
	if err != nil || ok {
		t.Fatalf("second completion: ok=%v err=%v", ok, err)
	}
	if got, _, _ := db.GetIdempotency(ctx, 42, "key"); string(got.Result) != `{"id":1}` {
		t.Fatalf("the saved result was overwritten: %s", got.Result)
	}
	// The same request with another hash sees the saved record, not a new one.
	other := rec
	other.ParamsHash = []byte("different")
	got, inserted, err = db.ReserveIdempotency(ctx, other)
	if err != nil || inserted || string(got.ParamsHash) != "hash" || string(got.Result) != `{"id":1}` {
		t.Fatalf("conflicting replay: %+v %v %v", got, inserted, err)
	}
}

func TestAudit07_IdempotencyRefusals(t *testing.T) {
	db := audit07OpenStore(t)
	ctx := context.Background()
	for name, rec := range map[string]IdempotencyRecord{
		"no key":    {ChatID: 1, Method: "send", ParamsHash: []byte("h")},
		"no method": {ChatID: 1, Key: "k", ParamsHash: []byte("h")},
		"no hash":   {ChatID: 1, Key: "k", Method: "send"},
	} {
		if _, inserted, err := db.ReserveIdempotency(ctx, rec); err == nil || inserted {
			t.Errorf("%s: accepted (inserted=%v err=%v)", name, inserted, err)
		}
	}
	if _, found, err := db.GetIdempotency(ctx, 1, "absent"); err != nil || found {
		t.Errorf("an absent key: found=%v err=%v", found, err)
	}

	rec := IdempotencyRecord{ChatID: 1, Key: "k", Method: "send", ParamsHash: []byte("h")}
	if _, inserted, err := db.ReserveIdempotency(ctx, rec); err != nil || !inserted {
		t.Fatalf("reserve: %v %v", inserted, err)
	}
	if ok, err := db.CompleteIdempotency(ctx, 1, "k", "send", rec.ParamsHash, json.RawMessage(`{broken`)); err == nil || ok {
		t.Errorf("invalid JSON result: ok=%v err=%v", ok, err)
	}
	if ok, err := db.CompleteIdempotency(ctx, 1, "k", "forward", rec.ParamsHash, json.RawMessage(`{}`)); err != nil || ok {
		t.Errorf("another method completed the record: ok=%v err=%v", ok, err)
	}
	if ok, err := db.CompleteIdempotency(ctx, 1, "absent", "send", rec.ParamsHash, json.RawMessage(`{}`)); err != nil || ok {
		t.Errorf("an absent record completed: ok=%v err=%v", ok, err)
	}
	if got, _, _ := db.GetIdempotency(ctx, 1, "k"); got.Result != nil {
		t.Errorf("a refused completion left a result: %s", got.Result)
	}
}

// TestAudit07_IdempotencyKeyIsPerChat keeps the same key in two chats apart.
func TestAudit07_IdempotencyKeyIsPerChat(t *testing.T) {
	db := audit07OpenStore(t)
	ctx := context.Background()
	first := IdempotencyRecord{ChatID: 1, Key: "same", Method: "send", ParamsHash: []byte("a")}
	second := IdempotencyRecord{ChatID: 2, Key: "same", Method: "send", ParamsHash: []byte("b")}
	for _, rec := range []IdempotencyRecord{first, second} {
		if _, inserted, err := db.ReserveIdempotency(ctx, rec); err != nil || !inserted {
			t.Fatalf("chat %d: inserted=%v err=%v", rec.ChatID, inserted, err)
		}
	}
	if ok, err := db.CompleteIdempotency(ctx, 1, "same", "send", first.ParamsHash, json.RawMessage(`{"chat":1}`)); err != nil || !ok {
		t.Fatalf("complete: %v %v", ok, err)
	}
	if got, _, _ := db.GetIdempotency(ctx, 2, "same"); got.Result != nil || string(got.ParamsHash) != "b" {
		t.Fatalf("chat 2 saw the record of chat 1: %+v", got)
	}
}
