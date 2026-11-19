package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func migrationFixture(t *testing.T, files map[string]string) (string, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	dir := filepath.Join(home, "default")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(t.TempDir(), "config.toml"), home, dir
}
func TestMigrateProfiles(t *testing.T) {
	path, home, dir := migrationFixture(t, map[string]string{"client": "desktop\n", "device": "MacBook\n", "transport": "tcp\n", "proxy": "socks5://user:secret@localhost:1080\n", "session.json": "session placeholder", "store.db": "database placeholder"})
	session := filepath.Join(dir, "session.json")
	old := time.Unix(1000000000, 0)
	if err := os.Chtimes(session, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateProfiles(path, home)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["default"]
	if p.Client != "desktop" || p.Device != "MacBook" || p.Transport != "tcp" || !strings.Contains(p.Proxy, "secret") {
		t.Fatal("settings were not migrated")
	}
	for _, key := range []string{"client", "device", "transport", "proxy"} {
		if _, err := os.Stat(filepath.Join(dir, key)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy %s remains: %v", key, err)
		}
		info, err := os.Stat(filepath.Join(result.Backup, "default", key))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("backup %s: %v %v", key, info, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(result.Backup, "default"))
	if err != nil || len(entries) != 4 {
		t.Fatalf("backup entries: %v %v", entries, err)
	}
	if info, err := os.Stat(filepath.Join(result.Backup, "config.toml")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config backup: %v %v", info, err)
	}
	info, err := os.Stat(session)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("session mtime: %v %v", info, err)
	}
	b, err := os.ReadFile(session)
	if err != nil || string(b) != "session placeholder" {
		t.Fatal("session changed")
	}
	// Move the mtime far into the past: a rewrite, even with the same bytes,
	// would bring it back to the present on any file system.
	past := time.Unix(1000000000, 0)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	again, err := MigrateProfiles(path, home)
	if err != nil || again.Backup != "" {
		t.Fatalf("second migration: %+v %v", again, err)
	}
	next, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !next.ModTime().Equal(past) {
		t.Fatal("config changed on second migration")
	}
}
func TestMigrateEmptyAndNone(t *testing.T) {
	path, home, dir := migrationFixture(t, map[string]string{"device": "  \n", "proxy": "none\n"})
	result, err := MigrateProfiles(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup == "" {
		t.Fatal("no backup")
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles["default"].Proxy != "" || cfg.Profiles["default"].Transport != "" {
		t.Fatal("unexpected settings")
	}
	if _, err := os.Stat(filepath.Join(dir, "proxy")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("none proxy remains")
	}
	entries, err := os.ReadDir(filepath.Join(result.Backup, "default"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "proxy" {
		t.Fatalf("backup entries: %v %v", entries, err)
	}
}
func TestMigrateInvalidAndConflict(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		path, home, dir := migrationFixture(t, map[string]string{"client": "../bad", "device": "phone"})
		_, err := MigrateProfiles(path, home)
		var e *Error
		if !errors.As(err, &e) || e.Code() != 2 {
			t.Fatalf("error %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("config created")
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 1 {
			t.Fatalf("backup created: %v", entries)
		}
		if _, err := os.Stat(filepath.Join(dir, "device")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		path, home, dir := migrationFixture(t, map[string]string{"client": "ios", "device": "phone"})
		if err := os.WriteFile(path, []byte("[profiles.default]\nclient = 'desktop'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := MigrateProfiles(path, home)
		if err != nil || len(result.Warnings) != 1 {
			t.Fatalf("result %+v error %v", result, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "client")); err != nil {
			t.Fatal("conflicting file removed")
		}
		entries, err := os.ReadDir(filepath.Join(result.Backup, "default"))
		if err != nil || len(entries) != 1 || entries[0].Name() != "device" {
			t.Fatalf("backup %v %v", entries, err)
		}
	})
}
func TestMigrateResumeAfterDeleteFailure(t *testing.T) {
	path, home, dir := migrationFixture(t, map[string]string{"client": "desktop", "transport": "tcp"})
	_, err := migrateProfiles(path, home, func(string) error { return errors.New("injected failure") })
	if err == nil {
		t.Fatal("failure not reported")
	}
	cfg, err := Load(path)
	if err != nil || cfg.Profiles["default"].Client != "desktop" {
		t.Fatalf("config not saved: %v", err)
	}
	if _, err := MigrateProfiles(path, home); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"client", "transport"} {
		if _, err := os.Stat(filepath.Join(dir, key)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains", key)
		}
	}
}

func TestMigrateDoesNotOpenConfiguredSessionFile(t *testing.T) {
	path, home, dir := migrationFixture(t, map[string]string{"client": "session placeholder"})
	if err := os.WriteFile(path, []byte("[files]\nsession = 'client'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := MigrateProfiles(path, home)
	var e *Error
	if !errors.As(err, &e) || e.Code() != 2 {
		t.Fatalf("error %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "client"))
	if err != nil || string(b) != "session placeholder" {
		t.Fatal("configured session changed")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected backup: %v %v", entries, err)
	}
}

func TestMigrateInlineConflict(t *testing.T) {
	path, home, dir := migrationFixture(t, map[string]string{"client": "ios"})
	initial := "profiles = { default = { client = 'desktop' } }\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateProfiles(path, home)
	if err != nil || len(result.Warnings) != 1 || result.Backup != "" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "client")); err != nil {
		t.Fatal("conflicting file removed")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != initial {
		t.Fatal("config changed")
	}
}

// TestMigrateSkipsTheDatabaseDirectory keeps the db directory next to the config
// out of the profile walk: it holds databases, not profile settings.
func TestMigrateSkipsTheDatabaseDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	db := filepath.Join(home, reservedProfile)
	if err := os.MkdirAll(db, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(db, "client")
	if err := os.WriteFile(legacy, []byte("desktop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	result, err := MigrateProfiles(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup != "" || len(result.Warnings) != 0 {
		t.Fatalf("the db directory was treated as a profile: %+v", result)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy file in the db directory was removed: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config created: %v", err)
	}
}
