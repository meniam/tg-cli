package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		name, content string
		missing       bool
		wantHome      string
		wantError     string
	}{
		{name: "missing", missing: true},
		{name: "unknown", content: "unknown = 1", wantError: "unknown config key at line 1"},
		{name: "home tilde", content: `home = "~/data"`, wantHome: filepath.Join(home, "data")},
		{name: "home tilde alone", content: `home = "~"`, wantHome: home},
		{name: "other user", content: `home = "~other/data"`, wantError: "home must not use ~user"},
		{name: "relative", content: `home = "relative"`, wantError: "home must be an absolute path"},
		{name: "duplicate files", content: "[files]\nsession = 'store.db'", wantError: "invalid or conflicting files name"},
		{name: "reserved file", content: "[files]\naccess = 'store.db-wal'", wantError: "invalid or conflicting files name"},
		{name: "reserved database record", content: "[files]\ndatabase = 'data-dir'", wantError: "invalid or conflicting files name"},
		{name: "reserved database lock", content: "[files]\ndatabase = 'db.lock'", wantError: "invalid or conflicting files name"},
		{name: "reserved session record", content: "[files]\nsession = 'data-dir'", wantError: "invalid or conflicting files name"},
		{name: "reserved default profile", content: "default_profile = 'db'", wantError: "default_profile: the name db is reserved"},
		{name: "proxy invalid", content: "[profiles.default]\nproxy = 'socks5://user:secret@example:invalid'", wantError: "invalid profiles proxy"},
		{name: "proxy malformed TOML", content: "[profiles.default]\nproxy = 'socks5://user:secret@example", wantError: "invalid TOML at line"},
		{name: "bad percent", content: "[behavior]\nthrottle_percent = 0", wantError: "behavior.throttle_percent must be between 1 and 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Load(path)
			if (err != nil) != (tc.wantError != "") {
				t.Fatalf("Load error = %v", err)
			}
			if err != nil {
				var configErr *Error
				if !errors.As(err, &configErr) || configErr.Code() != 2 {
					t.Fatalf("Load error = %v, want a *config.Error with code 2", err)
				}
				if !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Load error = %q, want it to contain %q", err, tc.wantError)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("error exposes proxy secret")
				}
				return
			}
			if tc.wantHome == "" {
				home, err := DefaultHome()
				if err != nil {
					t.Fatal(err)
				}
				tc.wantHome = home
			}
			if c.Home != tc.wantHome || c.DefaultProfile != "default" || c.Files.Database != "store.db" || c.Behavior.ThrottlePercent != 60 || c.Behavior.FloodAutoWait != "10s" {
				t.Fatalf("unexpected config: %+v", c)
			}
		})
	}
}

func TestPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[profiles.default]\nproxy = 'socks5://user:secret@localhost:1080'"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode         os.FileMode
		bad, warning bool
	}{{0o600, false, false}, {0o644, false, true}, {0o620, true, false}, {0o602, true, false}} {
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if (err != nil) != tc.bad {
			t.Fatalf("mode %o: error %v", tc.mode, err)
		}
		if tc.bad {
			e, ok := err.(*Error)
			if !ok || e.Code() != 2 {
				t.Fatalf("mode %o: error %v", tc.mode, err)
			}
		} else if (len(c.Warnings) > 0) != tc.warning {
			t.Fatalf("mode %o: warnings %v", tc.mode, c.Warnings)
		}
	}
}

// TestReservedProfileName keeps "db" out of profiles: on Linux the profile
// directory would coincide with the directory of profile databases.
func TestReservedProfileName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[profiles.db]\nclient = 'desktop'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	e, ok := err.(*Error)
	if !ok || e.Code() != 2 || !strings.Contains(e.Message, "reserved") {
		t.Fatalf("Load error = %v", err)
	}
	if _, err := UpdateProfile(filepath.Join(t.TempDir(), "config.toml"), reservedProfile, map[string]string{"client": "desktop"}, false); err == nil {
		t.Fatal("UpdateProfile accepted the reserved name")
	}
}

func TestLoadRejectsConfigSymlinks(t *testing.T) {
	for _, exists := range []bool{true, false} {
		t.Run(fmt.Sprintf("target=%t", exists), func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target.toml")
			path := filepath.Join(dir, "config.toml")
			if exists {
				if err := os.WriteFile(target, []byte("[behavior]\nthrottle_percent = 50\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("config symlink accepted")
			}
			if err := WithLegacyConfigLock(path, func(*LockedLegacyConfig) error { return nil }); err == nil {
				t.Fatal("migration accepted config symlink")
			}
		})
	}
}
