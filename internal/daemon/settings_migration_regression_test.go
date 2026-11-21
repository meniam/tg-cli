package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func migrationTestHome(t *testing.T) (string, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "tg-migrate-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(root, "config.toml")
}
func writeMigrationSource(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func migrationSettings(t *testing.T, configPath, profile string) (map[string]json.RawMessage, bool) {
	t.Helper()
	db, err := store.Open(filepath.Join(filepath.Dir(configPath), "db", profile, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	values, err := db.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	marked, err := db.SettingsMigrated(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return values, marked
}

func TestSettingsMigrationProfilesBackupAndRetry(t *testing.T) {
	home, path := migrationTestHome(t)
	content := "# Keep this comment\ndefault_profile = \"default\"\n[behavior]\nthrottle_percent = 37\n[profiles.default]\nclient = \"desktop\"\n[profiles.work]\nclient = \"ios\"\n"
	writeMigrationSource(t, path, content)
	sources := map[string]string{"default": `{"allow":[11,22],"block":[22],"write":[11]}`, "work": `{"block":[33],"admin":[44]}`}
	for name, data := range sources {
		writeMigrationSource(t, filepath.Join(home, name, "access.json"), data)
	}
	ctx := context.Background()
	result, err := MigrateSettings(ctx, path, home, "default")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Migrated || result.Backup == "" {
		t.Fatalf("migration result: %+v", result)
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{result.Backup, 0700}, {filepath.Join(result.Backup, "config.toml"), 0600}, {filepath.Join(result.Backup, "default"), 0700}, {filepath.Join(result.Backup, "default", "access.json"), 0600}, {filepath.Join(result.Backup, "work", "access.json"), 0600}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatalf("backup mode for %s: %v %v", item.path, info, err)
		}
	}
	saved, err := os.ReadFile(filepath.Join(result.Backup, "config.toml"))
	if err != nil || string(saved) != content {
		t.Fatalf("backup config differs: %v", err)
	}
	for name, source := range sources {
		saved, err := os.ReadFile(filepath.Join(result.Backup, name, "access.json"))
		if err != nil || string(saved) != source {
			t.Fatalf("backup %s differs: %v", name, err)
		}
		if _, err := os.Lstat(filepath.Join(home, name, "access.json")); !os.IsNotExist(err) {
			t.Fatalf("legacy file remains: %v", err)
		}
		values, marked := migrationSettings(t, path, name)
		if !marked || string(values["behavior.throttle_percent"]) != "37" {
			t.Fatalf("profile %s values=%v marked=%v", name, values, marked)
		}
		want := `"desktop"`
		if name == "work" {
			want = `"ios"`
		}
		if string(values["profile.client"]) != want {
			t.Fatalf("profile %s client=%s", name, values["profile.client"])
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(source), &fields); err != nil {
			t.Fatal(err)
		}
		for key, value := range fields {
			if !bytes.Equal(values["access."+key], value) {
				t.Fatalf("profile %s lost %s", name, key)
			}
		}
	}
	cleaned, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(cleaned), "# Keep this comment") || strings.Contains(string(cleaned), "[behavior]") || strings.Contains(string(cleaned), "[profiles.") {
		t.Fatalf("config cleanup: %s %v", cleaned, err)
	}
	before := map[string][]byte{}
	for name := range sources {
		p := filepath.Join(filepath.Dir(path), "db", name, "store.db")
		before[name], err = os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
	}
	again, err := MigrateSettings(ctx, path, home, "default")
	if err != nil || again.Migrated || again.Backup != "" {
		t.Fatalf("retry: %+v %v", again, err)
	}
	afterConfig, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(cleaned, afterConfig) {
		t.Fatal("retry changed config")
	}
	for name, expected := range before {
		actual, err := os.ReadFile(filepath.Join(filepath.Dir(path), "db", name, "store.db"))
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatalf("retry changed database %s: %v", name, err)
		}
	}
}

func TestSettingsMigrationInvalidSourceIsAtomic(t *testing.T) {
	for _, tc := range []struct{ name, config, access, source string }{
		{"access", "# keep\n[behavior]\nthrottle_percent = 55\n", `{"allow":[0],"unknown":"private-content"}`, "access"},
		{"config", "# private-content\n[behavior]\nthrottle_percent = 999\n", `{"write":[42]}`, "config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, path := migrationTestHome(t)
			accessPath := filepath.Join(home, "default", "access.json")
			writeMigrationSource(t, path, tc.config)
			writeMigrationSource(t, accessPath, tc.access)
			_, err := MigrateSettings(context.Background(), path, home, "default")
			if err == nil || strings.Contains(err.Error(), "private-content") {
				t.Fatalf("invalid source error: %v", err)
			}
			want := path
			if tc.source == "access" {
				want = accessPath
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("missing source name: %v", err)
			}
			for p, want := range map[string]string{path: tc.config, accessPath: tc.access} {
				got, e := os.ReadFile(p)
				if e != nil || string(got) != want {
					t.Fatalf("source changed: %s: %v", p, e)
				}
			}
			dbPath := filepath.Join(filepath.Dir(path), "db", "default", "store.db")
			if _, e := os.Stat(dbPath); e == nil {
				values, marked := migrationSettings(t, path, "default")
				if marked || len(values) != 0 {
					t.Fatalf("partial import: %v %t", values, marked)
				}
			} else if !os.IsNotExist(e) {
				t.Fatal(e)
			}
		})
	}
}

func TestOfflineSettingsRefusesProtectedWriteAndBusyProfile(t *testing.T) {
	home, path := migrationTestHome(t)
	ctx := context.Background()
	if _, err := MigrateSettings(ctx, path, home, "default"); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(rpc.SettingsSetParams{Key: "access.block", Value: []byte(`[42]`)})
	_, err := OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsSet, params)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeWriteDisabled {
		t.Fatalf("unprotected write: %v", err)
	}
	values, _ := migrationSettings(t, path, "default")
	if len(values) != 0 {
		t.Fatalf("refused write changed settings: %v", values)
	}
	paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	release, err := acquireLock(paths.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsSet, params)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("busy profile: %v", err)
	}
}

func TestMigratedSettingsRejectLateSources(t *testing.T) {
	for _, kind := range []string{"access", "config"} {
		t.Run(kind, func(t *testing.T) {
			home, path := migrationTestHome(t)
			ctx := context.Background()
			writeMigrationSource(t, path, "# unchanged\n")
			if _, err := MigrateSettings(ctx, path, home, "default"); err != nil {
				t.Fatal(err)
			}
			paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			target := paths.Access
			if kind == "access" {
				writeMigrationSource(t, target, `{"block":[42]}`)
			} else {
				target = "behavior"
				writeMigrationSource(t, path, "# unchanged\n[behavior]\nthrottle_percent = 55\n")
			}
			_, err = MigrateSettings(ctx, path, home, "default")
			var legacy *LegacySettingsError
			if !errors.As(err, &legacy) || legacy.ExitCode() != 2 || !strings.Contains(err.Error(), target) {
				t.Fatalf("late %s accepted: %v", kind, err)
			}
			raw, _ := json.Marshal(rpc.SettingsSetParams{Key: "behavior.throttle_percent", Value: []byte(`60`)})
			_, err = OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsSet, raw)
			if !errors.As(err, &legacy) || legacy.ExitCode() != 2 || !strings.Contains(err.Error(), target) {
				t.Fatalf("offline accepted %s: %v", kind, err)
			}
			db, err := store.Open(paths.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			d := &daemon{store: db, opts: Options{Paths: paths, ConfigPath: path}}
			d.refreshAccess(ctx)
			if p := d.policy(); !p.Closed() || p.Allowed(42) {
				t.Fatalf("late %s did not close policy", kind)
			}
			guardErr := d.guardRequest(rpc.Request{Method: rpc.MethodSettingsSet})
			var rpcErr *rpc.Error
			if !errors.As(guardErr, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams {
				t.Fatalf("running daemon accepted %s: %v", kind, guardErr)
			}
			wantSource := paths.Access
			if kind == "config" {
				wantSource = path
			}
			if !strings.Contains(rpcErr.Message, wantSource) {
				t.Fatalf("running daemon omitted source: %s", rpcErr.Message)
			}
			values, err := db.Settings(ctx)
			if err != nil || len(values) != 0 {
				t.Fatalf("late source imported: %v %v", values, err)
			}
		})
	}
}

func TestSettingsMigrationBackupFailureRollsBack(t *testing.T) {
	home, path := migrationTestHome(t)
	source := filepath.Join(home, "default", "access.json")
	configText := "# keep\n[behavior]\nthrottle_percent = 41\n"
	accessText := `{"write":[42]}`
	writeMigrationSource(t, path, configText)
	writeMigrationSource(t, source, accessText)
	paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := WithProfileDatabase(paths, func(ProfileDatabase) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0700) })
	_, err = MigrateSettings(context.Background(), path, home, "default")
	if err == nil {
		t.Fatal("migration succeeded without backup")
	}
	diagnostic := strings.ReplaceAll(err.Error(), path, "<source>")
	if strings.Contains(diagnostic, accessText) || strings.Contains(diagnostic, "42") {
		t.Fatalf("error exposes value: %v", err)
	}
	for p, want := range map[string]string{path: configText, source: accessText} {
		got, e := os.ReadFile(p)
		if e != nil || string(got) != want {
			t.Fatalf("source changed: %s: %v", p, e)
		}
	}
	values, marked := migrationSettings(t, path, "default")
	if marked || len(values) != 0 {
		t.Fatalf("partial migration: %v %t", values, marked)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("backup error must identify source file: %v", err)
	}
}

func TestSettingsMigrationVerificationFailureRollsBack(t *testing.T) {
	home, path := migrationTestHome(t)
	source := filepath.Join(home, "default", "access.json")
	configText := "# keep\n[behavior]\nthrottle_percent = 41\n"
	accessText := `{"write":[42]}`
	writeMigrationSource(t, path, configText)
	writeMigrationSource(t, source, accessText)
	paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := WithProfileDatabase(paths, func(ProfileDatabase) error { return nil }); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TRIGGER corrupt_migration AFTER INSERT ON settings WHEN NEW.key='behavior.throttle_percent' BEGIN UPDATE settings SET value='42' WHERE key=NEW.key; END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = MigrateSettings(context.Background(), path, home, "default")
	if err == nil {
		t.Fatal("verification failure accepted")
	}
	for p, want := range map[string]string{path: configText, source: accessText} {
		got, e := os.ReadFile(p)
		if e != nil || string(got) != want {
			t.Fatalf("source changed: %s: %v", p, e)
		}
	}
	values, marked := migrationSettings(t, path, "default")
	if marked || len(values) != 0 {
		t.Fatalf("partial migration: %v %t", values, marked)
	}
	diagnostic := strings.ReplaceAll(err.Error(), path, "<source>")
	if strings.Contains(diagnostic, "41") || strings.Contains(diagnostic, accessText) {
		t.Fatalf("error exposed values: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("verification error must identify source file: %v", err)
	}
}

func TestSettingsMigrationSecondCommitFailureRefusesRetry(t *testing.T) {
	home, path := migrationTestHome(t)
	ctx := context.Background()
	configText := "# preserve\n[behavior]\nthrottle_percent = 41\n[profiles.default]\nclient = \"desktop\"\n[profiles.work]\nclient = \"ios\"\n"
	writeMigrationSource(t, path, configText)
	for _, name := range []string{"default", "work"} {
		paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), name, DefaultFileNames())
		if err != nil {
			t.Fatal(err)
		}
		if err := WithProfileDatabase(paths, func(ProfileDatabase) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(filepath.Dir(path), "db", "work", "store.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE migration_parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE migration_child (id INTEGER REFERENCES migration_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_migration_commit AFTER INSERT ON settings BEGIN INSERT INTO migration_child(id) VALUES (123); END`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateSettings(ctx, path, home, "default")
	if err == nil {
		t.Fatal("second commit unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != configText {
		t.Fatalf("config changed on commit failure: %v", err)
	}
	if result.Backup == "" {
		t.Fatal("missing backup after commit failure")
	}
	backup, err := os.ReadFile(filepath.Join(result.Backup, "config.toml"))
	if err != nil || string(backup) != configText {
		t.Fatalf("backup: %v", err)
	}
	first, marked := migrationSettings(t, path, "default")
	if !marked || string(first["profile.client"]) != `"desktop"` || string(first["behavior.throttle_percent"]) != "41" {
		t.Fatalf("first database partially committed: %v %t", first, marked)
	}
	second, marked := migrationSettings(t, path, "work")
	if marked || len(second) != 0 {
		t.Fatalf("second database partially committed: %v %t", second, marked)
	}
	_, err = MigrateSettings(ctx, path, home, "default")
	var legacy *LegacySettingsError
	if !errors.As(err, &legacy) || legacy.ExitCode() != 2 {
		t.Fatalf("retry should refuse old source: %v", err)
	}
}

func TestSettingsMigrationErrorsNameAccessOnlySource(t *testing.T) {
	for _, failure := range []string{"backup", "verification", "verification_mixed"} {
		t.Run(failure, func(t *testing.T) {
			home, path := migrationTestHome(t)
			source := filepath.Join(home, "default", "access.json")
			configText := "# ordinary config\n"
			if failure == "verification_mixed" {
				configText += "[behavior]\nthrottle_percent = 55\n"
			}
			writeMigrationSource(t, path, configText)
			writeMigrationSource(t, source, `{"block":[42]}`)
			paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			if err := WithProfileDatabase(paths, func(ProfileDatabase) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if failure == "backup" {
				if err := os.Chmod(home, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(home, 0700) })
			} else {
				raw, err := sql.Open("sqlite", paths.Database)
				if err != nil {
					t.Fatal(err)
				}
				_, err = raw.Exec(`CREATE TRIGGER corrupt_access AFTER INSERT ON settings WHEN NEW.key='access.block' BEGIN UPDATE settings SET value='[43]' WHERE key=NEW.key; END`)
				if err != nil {
					t.Fatal(err)
				}
				if err := raw.Close(); err != nil {
					t.Fatal(err)
				}
			}
			_, err = MigrateSettings(context.Background(), path, home, "default")
			if err == nil || !strings.Contains(err.Error(), source) {
				t.Fatalf("wrong source: %v", err)
			}
			diagnostic := strings.ReplaceAll(err.Error(), source, "<source>")
			if strings.Contains(diagnostic, "42") || strings.Contains(diagnostic, "43") {
				t.Fatalf("error exposed IDs: %v", err)
			}
			values, marked := migrationSettings(t, path, "default")
			if marked || len(values) != 0 {
				t.Fatalf("partial import: %v %t", values, marked)
			}
		})
	}
}

func TestSettingsMigrationRejectsConflictingStoredTOML(t *testing.T) {
	home, path := migrationTestHome(t)
	ctx := context.Background()
	text := "[behavior]\nexpect_user = \"@owner\"\n"
	writeMigrationSource(t, path, text)
	paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := WithProfileDatabase(paths, func(db ProfileDatabase) error {
		return db.SetSettings(ctx, map[string]json.RawMessage{"behavior.expect_user": []byte(`""`)})
	}); err != nil {
		t.Fatal(err)
	}
	_, err = MigrateSettings(ctx, path, home, "default")
	var input *MigrationInputError
	if !errors.As(err, &input) || input.Source != path {
		t.Fatalf("conflicting source accepted: %v", err)
	}
	actual, e := os.ReadFile(path)
	if e != nil || string(actual) != text {
		t.Fatalf("legacy source changed: %v", e)
	}
	values, marked := migrationSettings(t, path, "default")
	if marked || string(values["behavior.expect_user"]) != `""` {
		t.Fatalf("database changed: %v %t", values, marked)
	}
}

func TestSettingsMigrationDiscoversLegacyHomeProfile(t *testing.T) {
	for _, kind := range []string{"access", "database"} {
		t.Run(kind, func(t *testing.T) {
			home, path := migrationTestHome(t)
			text := "[behavior]\nexpect_user = \"@owner\"\n"
			writeMigrationSource(t, path, text)
			source := filepath.Join(home, "work", "access.json")
			if kind == "access" {
				writeMigrationSource(t, source, `{"block":[42]}`)
			} else {
				if err := os.MkdirAll(filepath.Join(home, "work"), 0700); err != nil {
					t.Fatal(err)
				}
				db, err := store.Open(filepath.Join(home, "work", "store.db"))
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := MigrateSettings(context.Background(), path, home, "default"); err != nil {
				t.Fatal(err)
			}
			values, marked := migrationSettings(t, path, "work")
			if !marked || string(values["behavior.expect_user"]) != `"@owner"` {
				t.Fatalf("legacy profile lost settings: %v %t", values, marked)
			}
			if kind == "access" && string(values["access.block"]) != "[42]" {
				t.Fatalf("legacy policy lost: %v", values)
			}
		})
	}
}

func TestOfflineOwnerFailuresPersistAcrossCalls(t *testing.T) {
	home, path := migrationTestHome(t)
	ctx := context.Background()
	phrase := "valid owner passphrase"
	marshal := func(v any) json.RawMessage {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if _, err := OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsTokenInit, marshal(rpc.SettingsTokenInitParams{Passphrase: phrase})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, err := OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsSet, marshal(rpc.SettingsSetParams{Key: "access.block", Value: []byte(`[42]`), Passphrase: "wrong"}))
		var e *rpc.Error
		if !errors.As(err, &e) || e.Code != rpc.CodeWriteDisabled || !strings.Contains(e.Message, "wrong") {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	db, err := store.Open(filepath.Join(filepath.Dir(path), "db", "default", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	hash, found, err := db.OwnerSecret(ctx)
	if err != nil || !found {
		t.Fatalf("owner secret unavailable: %v", err)
	}
	if err := db.SetOwnerSecret(ctx, "invalid test hash"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsSet, marshal(rpc.SettingsSetParams{Key: "access.block", Value: []byte(`[42]`), Passphrase: phrase}))
	var e *rpc.Error
	if !errors.As(err, &e) || e.Code != rpc.CodeWriteDisabled || !strings.Contains(e.Message, "locked") {
		t.Fatalf("sixth attempt was not locked before hash: %v", err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(path), "db", "default", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetOwnerSecret(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if err := db.SetOwnerAttemptState(ctx, 5, time.Now().Add(-time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := OfflineSettingsWrite(ctx, path, home, "default", rpc.MethodSettingsSet, marshal(rpc.SettingsSetParams{Key: "access.block", Value: []byte(`[42]`), Passphrase: phrase})); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(path), "db", "default", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failures, until, err := db.OwnerAttemptState(ctx)
	if err != nil || failures != 0 || until != 0 {
		t.Fatalf("success did not clear lockout: %d %d %v", failures, until, err)
	}
	if err := db.SetOwnerAttemptState(ctx, 5, time.Now().Add(time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(path), "db"), "default", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := ResetOwnerSecretOfflineWithConfig(ctx, paths, path); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failures, until, err = db.OwnerAttemptState(ctx)
	if err != nil || failures != 0 || until != 0 {
		t.Fatalf("reset did not clear lockout: %d %d %v", failures, until, err)
	}
}

func TestRestoreMigrationSourcesContinuesAfterConflict(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	if err := os.WriteFile(first, []byte("new owner data"), 0600); err != nil {
		t.Fatal(err)
	}
	err := restoreRemovedSources([]removedSource{{path: first, data: []byte("old"), mode: 0600}, {path: second, data: []byte("preserved"), mode: 0600}})
	if err == nil || !strings.Contains(err.Error(), first) {
		t.Fatalf("conflict was not reported: %v", err)
	}
	one, e := os.ReadFile(first)
	if e != nil || string(one) != "new owner data" {
		t.Fatalf("new owner file changed: %v", e)
	}
	two, e := os.ReadFile(second)
	if e != nil || string(two) != "preserved" {
		t.Fatalf("remaining source not restored: %v", e)
	}
	err = restoreRemovedSources([]removedSource{{path: first, data: []byte("old"), mode: 0600}, {path: second, data: []byte("old"), mode: 0600}})
	if err == nil || !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), second) {
		t.Fatalf("restore errors not combined: %v", err)
	}
}

func TestSettingsMigrationConcurrentSourceChange(t *testing.T) {
	for _, tc := range []struct{ source, when string }{
		{"access", "before"}, {"access", "staged"}, {"access", "both"},
		{"config", "before"}, {"config", "staged"}, {"config", "recreated"},
	} {
		t.Run(tc.source+"/"+tc.when, func(t *testing.T) {
			home, cfg := migrationTestHome(t)
			path := cfg
			old, newer, newest := "[behavior]\nthrottle_percent=40\n", "[behavior]\nthrottle_percent=25\n", "[behavior]\nthrottle_percent=10\n"
			if tc.source == "access" {
				path = filepath.Join(home, "default", "access.json")
				old, newer, newest = "{}", `{"block":[42]}`, `{"block":[42,43]}`
			}
			writeMigrationSource(t, path, old)
			var staged string
			save := func(name, content string) {
				tmp := name + ".editor-tmp"
				writeMigrationSource(t, tmp, content)
				if err := os.Rename(tmp, name); err != nil {
					t.Fatal(err)
				}
			}
			rename := func(from, to string) error {
				if from != path {
					return os.Rename(from, to)
				}
				staged = to
				if fi, err := os.Stat(filepath.Dir(to)); err != nil || fi.Mode().Perm() != 0700 {
					t.Fatalf("staging directory permissions: %v %v", fi, err)
				}
				if tc.when == "before" {
					save(from, newer)
				}
				if err := os.Rename(from, to); err != nil {
					return err
				}
				if tc.when == "staged" || tc.when == "both" {
					save(to, newer)
				}
				if tc.when == "both" || tc.when == "recreated" {
					save(from, newest)
				}
				return nil
			}
			ctx := context.Background()
			result, err := migrateSettings(ctx, cfg, home, "default", rename)
			if err == nil || !strings.Contains(err.Error(), "source changed during the move") || !strings.Contains(err.Error(), path) {
				t.Fatalf("expected conflict at %s, got %v", path, err)
			}
			want := newer
			if tc.when == "both" || tc.when == "recreated" {
				want = newest
				retained := newer
				if tc.when == "recreated" {
					retained = old
				}
				data, e := os.ReadFile(staged)
				if e != nil || string(data) != retained || !strings.Contains(err.Error(), staged) {
					t.Fatalf("retained source %s: %q, %v, conflict %v", staged, data, e, err)
				}
			}
			data, e := os.ReadFile(path)
			if e != nil || string(data) != want {
				t.Fatalf("latest source: %q, %v", data, e)
			}
			if rel, e := filepath.Rel(result.Backup, staged); e != nil || !filepath.IsLocal(rel) {
				t.Fatalf("staged outside backup: %s", staged)
			}
			_, err = MigrateSettings(ctx, cfg, home, "default")
			var legacy *LegacySettingsError
			if !errors.As(err, &legacy) || !strings.Contains(err.Error(), path) {
				t.Fatalf("retry missed Q8: %v", err)
			}
		})
	}
}

func TestSettingsMigrationRetainsChangedSourceType(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home, cfg := migrationTestHome(t)
			path := filepath.Join(home, "default", "access.json")
			writeMigrationSource(t, path, `{"block":[77]}`)
			target := filepath.Join(home, "editor-source")
			writeMigrationSource(t, target, `{"block":[77]}`)
			_, err := migrateSettings(t.Context(), cfg, home, "default", func(from, to string) error {
				if err := os.Remove(from); err != nil {
					return err
				}
				if kind == "symlink" {
					if err := os.Symlink(target, from); err != nil {
						return err
					}
				} else if err := os.Mkdir(from, 0700); err != nil {
					return err
				}
				return os.Rename(from, to)
			})
			var changed *changedSource
			if !errors.As(err, &changed) {
				t.Fatalf("wanted source conflict: %v", err)
			}
			entry, err := os.Lstat(path)
			if err != nil || (kind == "directory" && !entry.IsDir()) || (kind == "symlink" && entry.Mode()&os.ModeSymlink == 0) {
				t.Fatalf("lost %s: %v %v", kind, entry, err)
			}
			_, err = MigrateSettings(t.Context(), cfg, home, "default")
			var legacy *LegacySettingsError
			if !errors.As(err, &legacy) || legacy.Source != path {
				t.Fatalf("retry missed Q8: %v", err)
			}
		})
	}
}
