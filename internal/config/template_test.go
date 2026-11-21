package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTemplateLoadsWithDefaults keeps the template usable as a configuration
// file: every setting is commented out, so loading it gives the defaults.
func TestTemplateLoadsWithDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(Template()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("the template must load: %v", err)
	}
	if got.DefaultProfile != "default" || got.Files.Database != "store.db" || got.Behavior.ThrottlePercent != 60 || got.Behavior.FloodAutoWait != "10s" {
		t.Fatalf("the template does not give the defaults: %+v", got)
	}
	if len(got.Profiles) != 0 || got.Legacy.Profiles != nil {
		t.Errorf("template must not contain profile settings: %+v", got.Profiles)
	}
}

// TestTemplateKeepsComments covers the rule of the header: `tg` rewrites only the
// lines of a profile table, so every other line of the template survives an
// update byte for byte.
func TestTemplateKeepsComments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(Template()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProfile(path, "default", map[string]string{"device": "X"}, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The update appends the missing legacy table and preserves every existing comment.
	want := strings.TrimRight(Template(), "\n") + "\n\n[profiles.default]\ndevice = \"X\"\n"
	if string(got) != want {
		t.Fatalf("the update changed more than the device line:\n%s", diffLines(want, string(got)))
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles["default"].Device != "X" {
		t.Errorf("device = %q", cfg.Profiles["default"].Device)
	}
}

// diffLines renders the lines that differ, so a failure shows the change instead
// of two whole files.
func diffLines(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			b.WriteString("- " + w + "\n+ " + g + "\n")
		}
	}
	return b.String()
}
