package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	releaseDB, err := LockDatabase(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := ResetOwnerSecretOffline(ctx, paths); !errors.Is(err, ErrDatabaseInUse) {
		releaseDB()
		t.Fatalf("reset with busy database: %v", err)
	}
	releaseDB()
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

func TestOwnerResetErrorsHidePaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbDir := filepath.Join(dir, "private-db")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatal(err)
	}
	paths := Paths{Dir: dir, Lock: filepath.Join(dir, "daemon.lock"), DatabaseDir: dbDir, Database: filepath.Join(dbDir, "private-store.db")}
	err := ResetOwnerSecretOffline(ctx, paths)
	if err == nil || strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "private-store") {
		t.Fatalf("missing database error leaks path: %v", err)
	}
	db, err := store.Open(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DatabaseBindingFile(paths), []byte(filepath.Join(dir, "other")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err = ResetOwnerSecretOffline(ctx, paths)
	if err == nil || strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "private-db") {
		t.Fatalf("binding error leaks path: %v", err)
	}
}

func TestConcurrentOwnerInitAndLockedHashCheck(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	first, second := NewOwnerSecrets(db), NewOwnerSecrets(db)
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for _, owner := range []*OwnerSecrets{first, second} {
		go func(o *OwnerSecrets) { <-start; outcomes <- o.Init(ctx, "valid initial passphrase") }(owner)
	}
	close(start)
	a, b := <-outcomes, <-outcomes
	if !((a == nil && errors.Is(b, ErrOwnerPassphraseExists)) || (b == nil && errors.Is(a, ErrOwnerPassphraseExists))) {
		t.Fatalf("concurrent initialization: %v, %v", a, b)
	}
	now := time.Unix(1000, 0)
	first.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if err := first.Verify(ctx, "incorrect phrase"); !errors.Is(err, ErrOwnerPassphraseWrong) {
			t.Fatal(err)
		}
	}
	if err := db.SetOwnerSecret(ctx, "corrupted hash"); err != nil {
		t.Fatal(err)
	}
	if err := first.Verify(ctx, "valid initial passphrase"); !errors.Is(err, ErrOwnerPassphraseLocked) {
		t.Fatalf("hash examined before lock: %v", err)
	}
	now = now.Add(time.Minute)
	if err := first.Verify(ctx, "valid initial passphrase"); err == nil || strings.Contains(err.Error(), "corrupted hash") || strings.Contains(err.Error(), "valid initial passphrase") {
		t.Fatalf("hash failure exposed a secret: %v", err)
	}
}
