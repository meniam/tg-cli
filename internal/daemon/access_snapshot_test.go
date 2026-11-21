package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/access"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestAccessSnapshotClosedOnErrorsAndLegacyFile(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{store: db, opts: Options{Paths: Paths{Access: filepath.Join(dir, "legacy.json")}}}
	ctx := context.Background()
	if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.block": []byte(`[42]`), "access.write": []byte(`[43]`)}); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	if d.policy().Allowed(42) || !d.policy().Allowed(43) || !d.policy().WriteAllowed(43) {
		t.Fatal("database policy not applied")
	}
	if err := os.WriteFile(d.opts.Paths.Access, []byte(`{"block":[43]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	if !d.policy().Closed() || d.policy().Allowed(43) {
		t.Fatal("legacy file opened access")
	}
	if err := os.Remove(d.opts.Paths.Access); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	if d.policy().Closed() || !d.policy().Allowed(43) {
		t.Fatal("snapshot did not recover")
	}
	rawDB, err := sql.Open("sqlite", filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`UPDATE settings SET value='null' WHERE key='access.block'`); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	if !d.policy().Closed() || d.policy().Allowed(43) {
		t.Fatal("invalid stored policy did not close access")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	if !d.policy().Closed() || d.policy().Allowed(43) {
		t.Fatal("database failure kept open policy")
	}
}

func TestAccessSnapshotRejectsNewLegacyEntry(t *testing.T) {
	for _, kind := range []string{"file", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db, err := store.Open(filepath.Join(dir, "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			path := filepath.Join(dir, "access.json")
			d := &daemon{store: db, opts: Options{Paths: Paths{Access: path}}}
			d.refreshAccess(context.Background())
			if d.policy().Closed() {
				t.Fatal("initial snapshot closed")
			}
			if kind == "file" {
				err = os.WriteFile(path, []byte(`{"block":[42]}`), 0600)
			} else {
				err = os.Symlink(filepath.Join(dir, "missing"), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if p := d.policy(); !p.Closed() || p.Allowed(42) {
				t.Fatal("new legacy entry did not close access")
			}
		})
	}
}

func TestAccessSnapshotRejectsDanglingSymlinkOnStart(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := filepath.Join(dir, "access.json")
	if err := os.Symlink(filepath.Join(dir, "missing"), path); err != nil {
		t.Fatal(err)
	}
	d := &daemon{store: db, opts: Options{Paths: Paths{Access: path}}}
	d.refreshAccess(context.Background())
	if p := d.policy(); !p.Closed() || p.Allowed(42) {
		t.Fatal("dangling symlink opened access")
	}
}

func TestAccessRefreshSerializesReadAndPublication(t *testing.T) {
	d := &daemon{}
	ctx := context.Background()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		d.refreshAccessFrom(ctx, func(context.Context) (map[string]json.RawMessage, error) {
			close(firstStarted)
			<-releaseFirst
			return map[string]json.RawMessage{"access.block": []byte(`[]`)}, nil
		})
	}()
	<-firstStarted
	if d.accessRefreshMu.TryLock() {
		d.accessRefreshMu.Unlock()
		close(releaseFirst)
		<-firstDone
		t.Fatal("refresh does not hold the lock during the read")
	}
	go func() {
		defer close(secondDone)
		d.refreshAccessFrom(ctx, func(context.Context) (map[string]json.RawMessage, error) {
			return nil, errors.New("unavailable")
		})
	}()
	close(releaseFirst)
	<-firstDone
	<-secondDone
	if p := d.policy(); !p.Closed() || p.Allowed(42) {
		t.Fatal("older snapshot replaced the later denial")
	}
}

func TestAccessFileGuardSeesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.json")
	if err := os.Symlink(filepath.Join(dir, "missing"), path); err != nil {
		t.Fatal(err)
	}
	exists, err := fileExists(path)
	if err != nil || !exists {
		t.Fatalf("dangling symlink existence = %v, %v", exists, err)
	}
}

func TestLegacyConflictDoesNotSuggestDeletingSources(t *testing.T) {
	legacy := (&LegacySettingsError{Source: "[behavior]"}).Error()
	if strings.Contains(legacy, "remov") || !strings.Contains(legacy, "backup") {
		t.Fatalf("unsafe recovery advice: %s", legacy)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "access.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := &daemon{opts: Options{Paths: Paths{Access: path}}}
	policy := access.Denied()
	d.accessSnapshot = &policy
	err := d.guardRequest(rpc.Request{Method: rpc.MethodSettingsGet})
	if err == nil || strings.Contains(err.Error(), "remov") || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("unsafe RPC recovery advice: %v", err)
	}
}
