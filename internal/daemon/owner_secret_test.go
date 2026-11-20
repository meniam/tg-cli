package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tg-cli/internal/store"
)

func TestOwnerPassphraseLifecycleAndLimit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := NewOwnerSecrets(db)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	owner.now = func() time.Time { return now }
	const old = "a valid old passphrase"
	const next = "a valid new passphrase"
	if err := owner.Verify(ctx, ""); !errors.Is(err, ErrOwnerPassphraseRequired) {
		t.Fatalf("missing phrase: %v", err)
	}
	if err := owner.Init(ctx, "short"); err == nil {
		t.Fatal("short phrase accepted")
	}
	if err := owner.Init(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := owner.Init(ctx, next); !errors.Is(err, ErrOwnerPassphraseExists) {
		t.Fatalf("second init: %v", err)
	}
	if err := owner.Verify(ctx, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := owner.Verify(ctx, "wrong phrase"); !errors.Is(err, ErrOwnerPassphraseWrong) {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	if err := owner.Verify(ctx, old); !errors.Is(err, ErrOwnerPassphraseLocked) {
		t.Fatalf("lock: %v", err)
	}
	now = now.Add(time.Minute)
	if err := owner.Verify(ctx, "wrong phrase"); !errors.Is(err, ErrOwnerPassphraseWrong) {
		t.Fatalf("sixth failure: %v", err)
	}
	now = now.Add(time.Minute)
	if err := owner.Verify(ctx, old); !errors.Is(err, ErrOwnerPassphraseLocked) {
		t.Fatalf("backoff: %v", err)
	}
	now = now.Add(time.Minute)
	if err := owner.Change(ctx, old, next); err != nil {
		t.Fatal(err)
	}
	if err := owner.Verify(ctx, old); !errors.Is(err, ErrOwnerPassphraseWrong) {
		t.Fatalf("old phrase still works: %v", err)
	}
	if err := owner.Verify(ctx, next); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerHashMalformedAndRandomSalt(t *testing.T) {
	phrase := "a valid passphrase"
	first, err := hashOwnerPassphrase(phrase)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashOwnerPassphrase(phrase)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("hash salts repeated")
	}
	for _, hash := range []string{"", first + "$x", "$argon2id$v=19$m=999999999,t=3,p=1$YQ$YQ"} {
		if ok, err := checkOwnerHash(phrase, hash); ok || err == nil {
			t.Fatal("malformed hash accepted")
		}
	}
}

func BenchmarkOwnerPassphraseVerify(b *testing.B) {
	phrase := "a valid passphrase"
	hash, err := hashOwnerPassphrase(phrase)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		valid, err := checkOwnerHash(phrase, hash)
		if err != nil || !valid {
			b.Fatal("verification failed")
		}
	}
}

func TestOwnerResetRequiresOfflineLock(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{Dir: dir, Lock: filepath.Join(dir, "daemon.lock"), DatabaseDir: filepath.Join(dir, "db"), Database: filepath.Join(dir, "db", "store.db")}
	if err := os.MkdirAll(paths.DatabaseDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.SetOwnerSecret(ctx, "a-test-hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := acquireLock(paths.Lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := ResetOwnerSecretOffline(ctx, paths); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("reset with running daemon: %v", err)
	}
	release()
	if err := ResetOwnerSecretOffline(ctx, paths); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, found, err := db.OwnerSecret(ctx); err != nil || found {
		t.Fatalf("hash still present: %v, %v", found, err)
	}
}
