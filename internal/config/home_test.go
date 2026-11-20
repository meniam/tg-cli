package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakeDirs(home, config string) (func() (string, error), func() (string, error)) {
	return func() (string, error) { return home, nil }, func() (string, error) { return config, nil }
}

// TestDefaultHomePerSystem pins the default data directory of each system
// without depending on the system the test runs on.
func TestDefaultHomePerSystem(t *testing.T) {
	cases := []struct {
		name, goos, home, config string
		want, wantLegacy         string
	}{
		{"macOS uses ~/.config", "darwin", "/Users/a", "/Users/a/Library/Application Support", "/Users/a/.config/tg-cli", "/Users/a/Library/Application Support/tg-cli"},
		{"Linux without XDG", "linux", "/home/a", "/home/a/.config", "/home/a/.config/tg-cli", ""},
		{"Linux with XDG", "linux", "/home/a", "/data/xdg", "/data/xdg/tg-cli", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userHome, userConfig := fakeDirs(tc.home, tc.config)
			got, err := defaultHome(tc.goos, userHome, userConfig)
			if err != nil || got != tc.want {
				t.Fatalf("defaultHome = %q, %v; want %q", got, err, tc.want)
			}
			if legacy := legacyHome(tc.goos, userHome, userConfig); legacy != tc.wantLegacy {
				t.Fatalf("legacyHome = %q, want %q", legacy, tc.wantLegacy)
			}
		})
	}
}

func TestDefaultHomeReportsMissingDirectories(t *testing.T) {
	fail := func() (string, error) { return "", errors.New("unset") }
	if _, err := defaultHome("darwin", fail, fail); err == nil {
		t.Fatal("darwin: want an error when the home directory is unknown")
	}
	if _, err := defaultHome("linux", fail, fail); err == nil {
		t.Fatal("linux: want an error when the config directory is unknown")
	}
	if legacy := legacyHome("darwin", fail, fail); legacy != "" {
		t.Fatalf("legacyHome = %q, want empty when directories are unknown", legacy)
	}
}

// TestLoadMarksExplicitHome keeps the tell between a home that the file sets
// and the default: only the default may be moved from the legacy directory.
func TestLoadMarksExplicitHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	def, err := DefaultHome()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, content string
		missing       bool
		wantSet       bool
		wantHome      string
	}{
		{name: "missing file", missing: true, wantHome: def},
		{name: "no key", content: "default_profile = 'default'", wantHome: def},
		{name: "explicit", content: `home = "~/data"`, wantSet: true, wantHome: filepath.Join(home, "data")},
		{name: "explicit default value", content: `home = "` + def + `"`, wantSet: true, wantHome: def},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if c.HomeSet != tc.wantSet || c.Home != tc.wantHome {
				t.Fatalf("Home=%q HomeSet=%v, want %q %v", c.Home, c.HomeSet, tc.wantHome, tc.wantSet)
			}
		})
	}
}

func TestDefaultConfigDirIgnoresXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	got, err := DefaultConfigDir()
	if err != nil || got != filepath.Join(home, ".config", "tg-cli") {
		t.Fatalf("DefaultConfigDir = %q, %v", got, err)
	}
}
