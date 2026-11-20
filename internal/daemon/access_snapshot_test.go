package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
