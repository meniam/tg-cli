package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each rejected value names its own reason and carries the usage exit code.
func TestAudit03LoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct{ name, content, want string }{
		{"flood wait not a duration", "[behavior]\nflood_autowait = 'soon'", "behavior.flood_autowait must be a non-negative duration"},
		{"flood wait negative", "[behavior]\nflood_autowait = '-5s'", "behavior.flood_autowait must be a non-negative duration"},
		{"percent above 100", "[behavior]\nthrottle_percent = 101", "behavior.throttle_percent must be between 1 and 100"},
		{"percent negative", "[behavior]\nthrottle_percent = -1", "behavior.throttle_percent must be between 1 and 100"},
		{"percent NaN", "[behavior]\nthrottle_percent = nan", "behavior.throttle_percent must be between 1 and 100"},
		{"default profile syntax", "default_profile = 'Bad Name'", "invalid default_profile"},
		{"profile client syntax", "[profiles.default]\nclient = 'Bad Client'", "invalid profiles client"},
		{"profile device blank", "[profiles.default]\ndevice = '   '", "invalid profiles device"},
		{"profile device control", "[profiles.default]\ndevice = \"a\\u0007b\"", "invalid profiles device"},
		{"profile device too long", "[profiles.default]\ndevice = '" + strings.Repeat("d", 65) + "'", "invalid profiles device"},
		{"profile transport", "[profiles.default]\ntransport = 'udp'", "invalid profiles transport"},
		{"transport with proxy", "[profiles.default]\ntransport = 'wss'\nproxy = 'tg://proxy?server=example.org&port=443&secret=dddddddddddddddddddddddddddddddddd'", "invalid profiles transport and proxy combination"},
		{"file name with a slash", "[files]\ndatabase = 'dir/store.db'", "invalid or conflicting files name"},
		{"file name with dots", "[files]\ndatabase = 'a..b.db'", "invalid or conflicting files name"},
		{"file name starts with a dot", "[files]\ndatabase = '.hidden'", "invalid or conflicting files name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code() != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want a code 2 error containing %q", err, tc.want)
			}
		})
	}
}

// UpdateProfile refuses a key that no profile has.
func TestAudit03UpdateProfileRejectsUnknownKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	_, err := UpdateProfile(path, "default", map[string]string{"colour": "red"}, false)
	var configErr *Error
	if !errors.As(err, &configErr) || configErr.Code() != 2 || !strings.Contains(err.Error(), "unknown profile key") {
		t.Fatalf("UpdateProfile = %v, want unknown profile key", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the config was created by a refused update: %v", statErr)
	}
}
