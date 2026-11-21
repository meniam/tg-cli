package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// backgroundSyncApp builds an app over a config.toml text and an environment.
func backgroundSyncApp(t *testing.T, configText string, env map[string]string) *app {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return &app{config: cfg, deps: Deps{Getenv: func(key string) string { return env[key] }}}
}

func writeConfig(t *testing.T, e *testEnv, text string) {
	t.Helper()
	path := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonBackgroundSyncDefaults(t *testing.T) {
	got, err := backgroundSyncApp(t, "", nil).daemonBackgroundSync()
	if err != nil {
		t.Fatal(err)
	}
	want := daemon.BackgroundSyncConfig{
		WindowStart: 8 * time.Hour, WindowEnd: 20 * time.Hour,
		IntervalMin: 40 * time.Minute, IntervalMax: 3 * time.Hour,
		PagePauseMin: 2 * time.Second, PagePauseMax: 8 * time.Second,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v (disabled)", got, want)
	}
}

func TestDaemonBackgroundSyncEnvironmentOverridesFile(t *testing.T) {
	file := "[background_sync]\nenabled = false\nwindow_start = \"09:00\"\nwindow_end = \"21:00\"\ninterval_min = \"1h\"\ninterval_max = \"2h\"\npage_pause_min = \"3s\"\npage_pause_max = \"5s\"\n"
	for _, tc := range []struct {
		name string
		env  map[string]string
		want daemon.BackgroundSyncConfig
	}{
		{"file only", nil, daemon.BackgroundSyncConfig{
			WindowStart: 9 * time.Hour, WindowEnd: 21 * time.Hour,
			IntervalMin: time.Hour, IntervalMax: 2 * time.Hour,
			PagePauseMin: 3 * time.Second, PagePauseMax: 5 * time.Second,
		}},
		{"every variable", map[string]string{
			"TG_CLI_BACKGROUND_SYNC_ENABLED":        " true ",
			"TG_CLI_BACKGROUND_SYNC_WINDOW_START":   "07:15",
			"TG_CLI_BACKGROUND_SYNC_WINDOW_END":     "22:45",
			"TG_CLI_BACKGROUND_SYNC_INTERVAL_MIN":   "10m",
			"TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX":   "20m",
			"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN": "1s",
			"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MAX": "2s",
		}, daemon.BackgroundSyncConfig{
			Enabled:     true,
			WindowStart: 7*time.Hour + 15*time.Minute, WindowEnd: 22*time.Hour + 45*time.Minute,
			IntervalMin: 10 * time.Minute, IntervalMax: 20 * time.Minute,
			PagePauseMin: time.Second, PagePauseMax: 2 * time.Second,
		}},
		{"one variable keeps the rest of the file", map[string]string{"TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX": "90m"}, daemon.BackgroundSyncConfig{
			WindowStart: 9 * time.Hour, WindowEnd: 21 * time.Hour,
			IntervalMin: time.Hour, IntervalMax: 90 * time.Minute,
			PagePauseMin: 3 * time.Second, PagePauseMax: 5 * time.Second,
		}},
		{"blank variable is ignored", map[string]string{"TG_CLI_BACKGROUND_SYNC_ENABLED": "  ", "TG_CLI_BACKGROUND_SYNC_WINDOW_END": " "}, daemon.BackgroundSyncConfig{
			WindowStart: 9 * time.Hour, WindowEnd: 21 * time.Hour,
			IntervalMin: time.Hour, IntervalMax: 2 * time.Hour,
			PagePauseMin: 3 * time.Second, PagePauseMax: 5 * time.Second,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backgroundSyncApp(t, file, tc.env).daemonBackgroundSync()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDaemonBackgroundSyncEnabledOverridesFileBothWays(t *testing.T) {
	on, err := backgroundSyncApp(t, "", map[string]string{"TG_CLI_BACKGROUND_SYNC_ENABLED": "true"}).daemonBackgroundSync()
	if err != nil || !on.Enabled {
		t.Fatalf("enabled = %v, err = %v, want the variable to turn it on", on.Enabled, err)
	}
	off, err := backgroundSyncApp(t, "[background_sync]\nenabled = true\n", map[string]string{"TG_CLI_BACKGROUND_SYNC_ENABLED": "false"}).daemonBackgroundSync()
	if err != nil || off.Enabled {
		t.Fatalf("enabled = %v, err = %v, want the variable to turn it off", off.Enabled, err)
	}
}

// TestDaemonBackgroundSyncInvalidEnvironment checks every rule through the
// environment: each wrong value is a usage error, and a single-value error names
// its variable.
func TestDaemonBackgroundSyncInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"enabled word", map[string]string{"TG_CLI_BACKGROUND_SYNC_ENABLED": "yes"}, "TG_CLI_BACKGROUND_SYNC_ENABLED must be true or false"},
		{"enabled number", map[string]string{"TG_CLI_BACKGROUND_SYNC_ENABLED": "1"}, "TG_CLI_BACKGROUND_SYNC_ENABLED must be true or false"},
		{"window start format", map[string]string{"TG_CLI_BACKGROUND_SYNC_WINDOW_START": "8:00"}, "TG_CLI_BACKGROUND_SYNC_WINDOW_START must be a time of day"},
		{"window end hour", map[string]string{"TG_CLI_BACKGROUND_SYNC_WINDOW_END": "24:00"}, "TG_CLI_BACKGROUND_SYNC_WINDOW_END must be a time of day"},
		{"interval min zero", map[string]string{"TG_CLI_BACKGROUND_SYNC_INTERVAL_MIN": "0s"}, "TG_CLI_BACKGROUND_SYNC_INTERVAL_MIN must be a positive duration"},
		{"interval max word", map[string]string{"TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX": "later"}, "TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX must be a positive duration"},
		{"page pause min negative", map[string]string{"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN": "-1s"}, "TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN must be a positive duration"},
		{"window reversed", map[string]string{"TG_CLI_BACKGROUND_SYNC_WINDOW_START": "21:00"}, "window_start must be earlier than background_sync.window_end"},
		{"interval reversed", map[string]string{"TG_CLI_BACKGROUND_SYNC_INTERVAL_MIN": "4h"}, "interval_min must not exceed background_sync.interval_max"},
		{"page pause below one second", map[string]string{"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN": "500ms"}, "page_pause_min must be at least 1s"},
		{"page pause reversed", map[string]string{"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MAX": "1s"}, "page_pause_min must not exceed background_sync.page_pause_max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := backgroundSyncApp(t, "", tc.env).daemonBackgroundSync()
			var commandErr *cmdError
			if !errors.As(err, &commandErr) || commandErr.exit != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want usage containing %q", err, tc.want)
			}
		})
	}
}

// TestInvalidBackgroundSyncStopsBeforeClientAndLauncher is the exit-code-2
// contract: a wrong value, from the file or from the environment, stops
// `daemon start` before a client is created or a process is launched.
func TestInvalidBackgroundSyncStopsBeforeClientAndLauncher(t *testing.T) {
	for _, tc := range []struct {
		name, file, variable, value string
		want                        string
	}{
		{name: "environment value", variable: "TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN", value: "soon", want: "TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN"},
		{name: "environment pair", variable: "TG_CLI_BACKGROUND_SYNC_WINDOW_START", value: "23:00", want: "window_start must be earlier"},
		{name: "file value", file: "[background_sync]\ninterval_min = \"4h\"\n", want: "interval_min must not exceed"},
	} {
		for _, foreground := range []bool{false, true} {
			name := tc.name + "/background"
			args := []string{"daemon", "start"}
			if foreground {
				name = tc.name + "/foreground"
				args = append(args, "--foreground")
			}
			t.Run(name, func(t *testing.T) {
				e := newEnv(t)
				if tc.file != "" {
					writeConfig(t, e, tc.file)
				}
				if tc.variable != "" {
					e.env[tc.variable] = tc.value
				}
				var stdout, stderr safeBuffer
				deps := e.deps(&stdout, &stderr)
				created, launched := false, false
				deps.NewTelegram = func(daemon.TelegramConfig) (daemon.Telegram, error) {
					created = true
					return nil, errors.New("should not create Telegram")
				}
				deps.LaunchDaemon = func([]string, string) (int, func() error, error) {
					launched = true
					return 0, nil, errors.New("should not launch daemon")
				}
				code := Execute(context.Background(), args, deps)
				if code != exitcode.Usage || created || launched {
					t.Fatalf("code=%d created=%t launched=%t", code, created, launched)
				}
				answer := stdout.String() + stderr.String()
				if !strings.Contains(answer, tc.want) || strings.Contains(answer, "APIHASHVALUE") {
					t.Fatalf("unexpected diagnostic: %q", answer)
				}
			})
		}
	}
}

// TestBackgroundSyncReachesDaemonOptions follows the settings from config.toml
// and the environment through `daemon start --foreground` into daemon.Options.
func TestBackgroundSyncReachesDaemonOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]json.RawMessage
		env    map[string]string
		want   daemon.BackgroundSyncConfig
	}{
		{"off by default", nil, nil, daemon.BackgroundSyncConfig{
			WindowStart: 8 * time.Hour, WindowEnd: 20 * time.Hour,
			IntervalMin: 40 * time.Minute, IntervalMax: 3 * time.Hour,
			PagePauseMin: 2 * time.Second, PagePauseMax: 8 * time.Second,
		}},
		{"database and environment", map[string]json.RawMessage{"background_sync.enabled": []byte("false"), "background_sync.window_end": []byte(`"19:30"`)}, map[string]string{
			"TG_CLI_BACKGROUND_SYNC_ENABLED":      "true",
			"TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX": "2h",
		}, daemon.BackgroundSyncConfig{
			Enabled:     true,
			WindowStart: 8 * time.Hour, WindowEnd: 19*time.Hour + 30*time.Minute,
			IntervalMin: 40 * time.Minute, IntervalMax: 2 * time.Hour,
			PagePauseMin: 2 * time.Second, PagePauseMax: 8 * time.Second,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.values != nil {
				e.setProfileSettings("default", tc.values)
			}
			for key, value := range tc.env {
				e.env[key] = value
			}
			var received daemon.Options
			called := false
			previous := runDaemon
			runDaemon = func(ctx context.Context, opts daemon.Options) error {
				called = true
				err := daemon.WithProfileDatabase(opts.Paths, func(db daemon.ProfileDatabase) error { return opts.Prepare(ctx, db, &opts) })
				received = opts
				return err
			}
			t.Cleanup(func() { runDaemon = previous })
			var stdout, stderr safeBuffer
			deps := e.deps(&stdout, &stderr)
			deps.NewTelegram = func(daemon.TelegramConfig) (daemon.Telegram, error) { return e.tg, nil }
			if code := Execute(context.Background(), []string{"daemon", "start", "--foreground"}, deps); code != exitcode.OK {
				t.Fatalf("exit %d: %s %s", code, stdout.String(), stderr.String())
			}
			if !called || received.BackgroundSync != tc.want {
				t.Fatalf("called=%t options=%+v, want %+v", called, received.BackgroundSync, tc.want)
			}
		})
	}
}

// TestConfigShowListsBackgroundSync checks that settings move from config show to the registry.
func TestConfigShowListsBackgroundSync(t *testing.T) {
	for _, tc := range []struct {
		name           string
		values         map[string]json.RawMessage
		enabled, start string
	}{
		{"defaults", nil, "false", `"08:00"`},
		{"database", map[string]json.RawMessage{"background_sync.enabled": []byte("true"), "background_sync.window_start": []byte(`"09:00"`)}, "true", `"09:00"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.values != nil {
				e.setProfileSettings("default", tc.values)
			}
			r := e.run("config", "show")
			if r.code != exitcode.OK {
				t.Fatalf("show: %d %s", r.code, r.stdout)
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(parse(t, r).Data, &data); err != nil {
				t.Fatal(err)
			}
			if _, ok := data["background_sync"]; ok {
				t.Fatal("settings in config show")
			}
			for key, want := range map[string]string{"background_sync.enabled": tc.enabled, "background_sync.window_start": tc.start, "background_sync.window_end": `"20:00"`, "background_sync.interval_min": `"40m"`, "background_sync.interval_max": `"3h"`, "background_sync.page_pause_min": `"2s"`, "background_sync.page_pause_max": `"8s"`} {
				if got := string(e.setting(key)); got != want {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}
