package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	before := "# header\n[profiles.default]\nclient = 'desktop' # keep\n\nproxy = 'none' # keep proxy\n\n[profiles.other]\nclient = 'ios'\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProfile(path, "default", map[string]string{"proxy": "socks5://user:pass@localhost:1080"}, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(before, "'none'", `"socks5://user:pass@localhost:1080"`, 1)
	if string(b) != want {
		t.Fatalf("unexpected file: %q", b)
	}
	old := time.Unix(1000000000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProfile(path, "default", map[string]string{"proxy": "socks5://user:pass@localhost:1080"}, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("mtime changed: %v %v", info, err)
	}
	if _, err := UpdateProfile(path, "new", map[string]string{"client": "desktop"}, false); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), want) || !strings.Contains(string(b), "[profiles.new]\nclient = \"desktop\"\n") {
		t.Fatalf("unexpected appended file %q", b)
	}
}

func TestUpdateProfileRejectsDotted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	before := "profiles.default.client = 'desktop'\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateProfile(path, "default", map[string]string{"client": "ios"}, false)
	if e, ok := err.(*Error); !ok || e.Code() != 2 {
		t.Fatalf("error %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != before {
		t.Fatalf("file changed %q, %v", b, err)
	}
}

func TestUpdateProfileInlineAndMissingKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, input := range []string{"profiles = { default = { client = 'desktop' } }\n", "[profiles]\ndefault = { client = 'desktop' }\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := UpdateProfile(path, "default", map[string]string{"client": "ios"}, false)
		if e, ok := err.(*Error); !ok || e.Code() != 2 {
			t.Fatalf("error %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != input {
			t.Fatalf("file changed: %q %v", b, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	input := "[profiles.default]\nclient = '' # explicit empty\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProfile(path, "default", map[string]string{"client": "desktop"}, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != input {
		t.Fatalf("existing empty value changed: %q %v", b, err)
	}
}

func TestUpdateProfileWithRollback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, existing := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "config.toml")
		before := "# keep\n[profiles.default]\nclient = 'desktop'\n"
		if existing {
			if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		sentinel := errors.New("action failed")
		_, err := UpdateProfileWith(path, "default", map[string]string{"client": "ios"}, false, func() error { return sentinel })
		if !errors.Is(err, sentinel) {
			t.Fatalf("error %v", err)
		}
		b, err := os.ReadFile(path)
		if existing {
			if err != nil || string(b) != before {
				t.Fatalf("rollback %q %v", b, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("new config remains: %q %v", b, err)
		}
	}
}
