package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadBackgroundSync(t *testing.T, content string) (Config, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestBackgroundSyncDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, content := range map[string]string{"missing file": "", "template": Template()} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadBackgroundSync(t, content)
			if err != nil {
				t.Fatal(err)
			}
			values, err := cfg.BackgroundSync.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			want := BackgroundSyncValues{
				Enabled:     true,
				WindowStart: 8 * time.Hour, WindowEnd: 20 * time.Hour,
				IntervalMin: 40 * time.Minute, IntervalMax: 3 * time.Hour,
				PagePauseMin: 2 * time.Second, PagePauseMax: 8 * time.Second,
			}
			if values != want {
				t.Fatalf("values = %+v, want %+v (enabled)", values, want)
			}
		})
	}
}

func TestBackgroundSyncFromFile(t *testing.T) {
	cfg, err := loadBackgroundSync(t, `[background_sync]
enabled = true
window_start = "09:30"
window_end = "18:45"
interval_min = "1h"
interval_max = "1h"
page_pause_min = "1s"
page_pause_max = "1500ms"
`)
	if err != nil {
		t.Fatal(err)
	}
	values, err := cfg.BackgroundSync.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := BackgroundSyncValues{
		Enabled:     true,
		WindowStart: 9*time.Hour + 30*time.Minute, WindowEnd: 18*time.Hour + 45*time.Minute,
		IntervalMin: time.Hour, IntervalMax: time.Hour,
		PagePauseMin: time.Second, PagePauseMax: 1500 * time.Millisecond,
	}
	if values != want {
		t.Fatalf("values = %+v, want %+v", values, want)
	}
}

// TestBackgroundSyncRejectsInvalidValues covers every rule of the section: each
// invalid file is a usage error (code 2) that names the key.
func TestBackgroundSyncRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"window start format", `window_start = "8:00"`, "background_sync.window_start must be a time of day"},
		{"window start hour", `window_start = "24:00"`, "background_sync.window_start must be a time of day"},
		{"window start minute", `window_start = "08:60"`, "background_sync.window_start must be a time of day"},
		{"window start sign", `window_start = "+8:00"`, "background_sync.window_start must be a time of day"},
		{"window start words", `window_start = "morning"`, "background_sync.window_start must be a time of day"},
		{"window end empty", `window_end = ""`, "background_sync.window_end must be a time of day"},
		{"window equal", "window_start = \"10:00\"\nwindow_end = \"10:00\"", "window_start must be earlier than background_sync.window_end"},
		{"window reversed", "window_start = \"20:00\"\nwindow_end = \"08:00\"", "window_start must be earlier than background_sync.window_end"},
		{"interval min not a duration", `interval_min = "soon"`, "background_sync.interval_min must be a positive duration"},
		{"interval min zero", `interval_min = "0s"`, "background_sync.interval_min must be a positive duration"},
		{"interval min negative", `interval_min = "-1m"`, "background_sync.interval_min must be a positive duration"},
		{"interval max zero", `interval_max = "0"`, "background_sync.interval_max must be a positive duration"},
		{"interval reversed", "interval_min = \"3h\"\ninterval_max = \"40m\"", "interval_min must not exceed background_sync.interval_max"},
		{"page pause below one second", `page_pause_min = "999ms"`, "background_sync.page_pause_min must be at least 1s"},
		{"page pause min negative", `page_pause_min = "-2s"`, "background_sync.page_pause_min must be a positive duration"},
		{"page pause max invalid", `page_pause_max = "slow"`, "background_sync.page_pause_max must be a positive duration"},
		{"page pause reversed", "page_pause_min = \"9s\"\npage_pause_max = \"8s\"", "page_pause_min must not exceed background_sync.page_pause_max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadBackgroundSync(t, "[background_sync]\n"+tc.body+"\n")
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code() != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want a code 2 error containing %q", err, tc.want)
			}
		})
	}
}

func TestBackgroundSyncEnabledMustBeBoolean(t *testing.T) {
	_, err := loadBackgroundSync(t, "[background_sync]\nenabled = \"yes\"\n")
	var configErr *Error
	if !errors.As(err, &configErr) || configErr.Code() != 2 {
		t.Fatalf("Load error = %v, want a code 2 error", err)
	}
}

// TestBackgroundSyncSurvivesProfileUpdate shows that a profile update leaves the
// new section alone, so writing a device name keeps the scheduler settings.
func TestBackgroundSyncSurvivesProfileUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[background_sync]\nenabled = true\nwindow_end = \"19:00\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := UpdateProfile(path, "default", map[string]string{"device": "X"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BackgroundSync.Enabled || cfg.BackgroundSync.WindowEnd != "19:00" || cfg.BackgroundSync.WindowStart != "08:00" {
		t.Fatalf("section after update = %+v", cfg.BackgroundSync)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), content) {
		t.Fatalf("the update changed the section:\n%s", data)
	}
}
