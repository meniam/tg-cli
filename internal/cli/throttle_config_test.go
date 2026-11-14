package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/throttle"
)

func TestDaemonThrottleConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		percent     string
		wait        string
		wantPercent float64
		wantWait    time.Duration
		wantError   string
	}{
		{"defaults", "", "", throttle.DefaultPercent, throttle.DefaultFloodAutoWait, ""},
		{"trimmed empty", "  ", "  ", throttle.DefaultPercent, throttle.DefaultFloodAutoWait, ""},
		{"minimum", "1", "", 1, throttle.DefaultFloodAutoWait, ""},
		{"maximum", "100", "", 100, throttle.DefaultFloodAutoWait, ""},
		{"fractional", "60.5", "2.5s", 60.5, 2500 * time.Millisecond, ""},
		{"zero wait", "", "0", throttle.DefaultPercent, 0, ""},
		{"zero percent", "0", "", 0, 0, "TG_CLI_THROTTLE_PERCENT"},
		{"over maximum", "101", "", 0, 0, "TG_CLI_THROTTLE_PERCENT"},
		{"not a number", "faster", "", 0, 0, "TG_CLI_THROTTLE_PERCENT"},
		{"exponent", "1e2", "", 0, 0, "TG_CLI_THROTTLE_PERCENT"},
		{"negative wait", "", "-1s", 0, 0, "TG_CLI_FLOOD_AUTOWAIT"},
		{"invalid wait", "", "later", 0, 0, "TG_CLI_FLOOD_AUTOWAIT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"TG_CLI_THROTTLE_PERCENT": tc.percent, "TG_CLI_FLOOD_AUTOWAIT": tc.wait}
			a := &app{deps: Deps{Getenv: func(key string) string { return env[key] }}}
			cfg, limiter, err := a.daemonThrottle()
			if tc.wantError != "" {
				var commandErr *cmdError
				if !errors.As(err, &commandErr) || commandErr.exit != exitcode.Usage || !strings.Contains(err.Error(), tc.wantError) || limiter != nil {
					t.Fatalf("limiter=%v error=%v, want usage for %s", limiter, err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if limiter == nil || cfg.Percent != tc.wantPercent || cfg.FloodAutoWait != tc.wantWait || limiter.FloodAutoWait() != tc.wantWait {
				t.Fatalf("percent=%v wait=%s limiter=%v, want %v and %s", cfg.Percent, cfg.FloodAutoWait, limiter, tc.wantPercent, tc.wantWait)
			}
		})
	}
}

func TestInvalidDaemonThrottleStopsBeforeClientAndLauncher(t *testing.T) {
	for _, tc := range []struct {
		name     string
		variable string
		value    string
	}{
		{"percent zero", "TG_CLI_THROTTLE_PERCENT", "0"},
		{"percent over maximum", "TG_CLI_THROTTLE_PERCENT", "101"},
		{"wait negative", "TG_CLI_FLOOD_AUTOWAIT", "-1s"},
		{"wait invalid", "TG_CLI_FLOOD_AUTOWAIT", "later"},
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
				e.env[tc.variable] = tc.value
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
				if !strings.Contains(answer, tc.variable) || strings.Contains(answer, "APIHASHVALUE") || strings.Contains(answer, "12345") {
					t.Fatalf("unexpected diagnostic: %q", answer)
				}
			})
		}
	}
}

func TestValidDaemonThrottleReachesClientFactory(t *testing.T) {
	e := newEnv(t)
	e.env["TG_CLI_THROTTLE_PERCENT"] = "42.5"
	e.env["TG_CLI_FLOOD_AUTOWAIT"] = "3s"
	var stdout, stderr safeBuffer
	deps := e.deps(&stdout, &stderr)
	var received daemon.TelegramConfig
	deps.NewTelegram = func(cfg daemon.TelegramConfig) (daemon.Telegram, error) {
		received = cfg
		return nil, errors.New("stop before daemon startup")
	}
	if code := Execute(context.Background(), []string{"daemon", "start", "--foreground"}, deps); code != exitcode.Failure {
		t.Fatalf("exit = %d, want factory failure", code)
	}
	if received.Throttle == nil || received.Throttle.FloodAutoWait() != 3*time.Second {
		t.Fatalf("client did not receive configured limiter: %+v", received.Throttle)
	}
}
