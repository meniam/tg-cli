package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSettingsMigrationFromPreviousVersion(t *testing.T) {
	migrations, err := listMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var version int
	for _, m := range migrations {
		if strings.HasSuffix(m.name, "_settings.sql") {
			version = m.version
		}
	}
	if version == 0 {
		t.Fatal("settings migration missing")
	}
	original := databaseAtVersion(t, version-1)
	plain, err := sql.Open("sqlite", original)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, plain, `INSERT INTO meta(key,value) VALUES('settings-test','preserved')`)
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}
	// Copy an older fixture; migration must not modify the source database.
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got, err := db.Version(); err != nil || got != newestMigrationVersion(t) {
		t.Fatalf("version = %d, %v", got, err)
	}
	var marker string
	if err := db.read.QueryRow(`SELECT value FROM meta WHERE key='settings-test'`).Scan(&marker); err != nil || marker != "preserved" {
		t.Fatalf("marker = %q, %v", marker, err)
	}
	values, err := db.Settings(context.Background())
	if err != nil || len(values) != 0 {
		t.Fatalf("settings = %v, %v", values, err)
	}
	if _, err := db.write.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('invalid','not json',1)`); err == nil {
		t.Error("invalid JSON accepted")
	}
	if _, err := db.write.Exec(`INSERT INTO owner_secret(id,hash,created_at,updated_at) VALUES(2,'hash',1,1)`); err == nil {
		t.Error("second owner ID accepted")
	}
	sourceVersion, err := versionAt(original)
	if err != nil || sourceVersion != version-1 {
		t.Fatalf("source version = %d, %v", sourceVersion, err)
	}
}

func TestSettingsRoundTripAndValidation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	changes := map[string]json.RawMessage{"behavior.throttle_percent": []byte(`70`), "access.write": []byte(`[4,-2,4]`), "background_sync.interval_min": []byte(`"4h"`), "background_sync.interval_max": []byte(`"5h"`)}
	if err := db.SetSettings(ctx, changes); err != nil {
		t.Fatal(err)
	}
	got, err := db.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]json.RawMessage{"behavior.throttle_percent": []byte(`70`), "access.write": []byte(`[-2,4]`), "background_sync.interval_min": []byte(`"4h"`), "background_sync.interval_max": []byte(`"5h"`)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %s, want %s", got, want)
	}
	for _, bad := range []map[string]json.RawMessage{{"unknown.key": []byte(`1`)}, {"access.write": []byte(`[0]`)}, {"background_sync.interval_max": []byte(`"1h"`)}} {
		if err := db.SetSettings(ctx, bad); err == nil {
			t.Errorf("accepted invalid settings %s", bad)
		}
	}
	if err := db.DeleteSetting(ctx, "background_sync.interval_max"); err == nil {
		t.Fatal("deletion broke interval pair")
	}
	if err := db.DeleteSetting(ctx, "behavior.throttle_percent"); err != nil {
		t.Fatal(err)
	}
	got, err = db.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["behavior.throttle_percent"]; ok {
		t.Fatal("deleted key still present")
	}
	if _, err := db.write.Exec(`UPDATE settings SET value='null' WHERE key='access.write'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Settings(ctx); err == nil {
		t.Fatal("invalid database value accepted")
	}
}

func TestSettingsTransactionRollback(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	schemaExec(t, db.write, `CREATE TRIGGER reject_settings BEFORE INSERT ON settings WHEN NEW.key='behavior.throttle_percent' BEGIN SELECT RAISE(ABORT, 'test failure'); END`)
	changes := map[string]json.RawMessage{"access.dialogs": []byte(`true`), "behavior.throttle_percent": []byte(`80`)}
	if err := db.ImportLegacySettings(ctx, changes); err == nil {
		t.Fatal("write succeeded despite trigger")
	}
	got, err := db.Settings(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("partial settings = %v, %v", got, err)
	}
	migrated, err := db.SettingsMigrated(ctx)
	if err != nil || migrated {
		t.Fatalf("marker = %v, %v", migrated, err)
	}
	schemaExec(t, db.write, `DROP TRIGGER reject_settings`)
	schemaExec(t, db.write, `CREATE TRIGGER reject_marker BEFORE INSERT ON settings_meta BEGIN SELECT RAISE(ABORT, 'test failure'); END`)
	if err := db.ImportLegacySettings(ctx, changes); err == nil {
		t.Fatal("marker write succeeded despite trigger")
	}
	got, err = db.Settings(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("partial settings after marker failure = %v, %v", got, err)
	}
	schemaExec(t, db.write, `DROP TRIGGER reject_marker`)
	if err := db.ImportLegacySettings(ctx, changes); err != nil {
		t.Fatal(err)
	}
	migrated, err = db.SettingsMigrated(ctx)
	if err != nil || !migrated {
		t.Fatalf("marker = %v, %v", migrated, err)
	}
	if err := db.ImportLegacySettings(ctx, changes); err == nil {
		t.Fatal("repeated import accepted")
	}
}

func TestOwnerSecretAndMigrationMarker(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, found, err := db.OwnerSecret(ctx); err != nil || found {
		t.Fatalf("unexpected secret: %v %v", found, err)
	}
	db.now = func() time.Time { return time.Unix(100, 0) }
	if err := db.SetOwnerSecret(ctx, "hash-one"); err != nil {
		t.Fatal(err)
	}
	db.now = func() time.Time { return time.Unix(200, 0) }
	if err := db.SetOwnerSecret(ctx, "hash-two"); err != nil {
		t.Fatal(err)
	}
	hash, found, err := db.OwnerSecret(ctx)
	if err != nil || !found || hash != "hash-two" {
		t.Fatalf("secret = %q, %v, %v", hash, found, err)
	}
	var created, updated int64
	if err := db.read.QueryRow(`SELECT created_at,updated_at FROM owner_secret WHERE id=1`).Scan(&created, &updated); err != nil || created != 100 || updated != 200 {
		t.Fatalf("timestamps = %d, %d, %v", created, updated, err)
	}
	values, err := db.Settings(ctx)
	if err != nil || len(values) != 0 {
		t.Fatalf("secret leaked into settings: %v, %v", values, err)
	}
	if err := db.DeleteOwnerSecret(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.OwnerSecret(ctx); err != nil || found {
		t.Fatalf("secret remains: %v, %v", found, err)
	}
	if err := db.ImportLegacySettings(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if migrated, err := db.SettingsMigrated(ctx); err != nil || !migrated {
		t.Fatalf("marker = %v, %v", migrated, err)
	}
}
