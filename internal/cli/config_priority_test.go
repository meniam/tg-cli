package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/config"
	"tg-cli/internal/exitcode"

	"github.com/spf13/cobra"
)

func TestDaemonThrottleConfigurationPriority(t *testing.T) {
	for _, tc := range []struct {
		name, configText, envPercent, envExpect, flag string
		flagSet                                       bool
		percent                                       float64
		expect                                        string
	}{
		{name: "defaults", percent: 60},
		{name: "config", configText: "[behavior]\nthrottle_percent = 35\nexpect_user = 'config'\n", percent: 35, expect: "config"},
		{name: "environment", configText: "[behavior]\nthrottle_percent = 35\nexpect_user = 'config'\n", envPercent: "45", envExpect: "env", percent: 45, expect: "env"},
		{name: "flag", configText: "[behavior]\nthrottle_percent = 35\nexpect_user = 'config'\n", envPercent: "45", envExpect: "env", flagSet: true, flag: "flag", percent: 45, expect: "flag"},
		{name: "empty flag", configText: "[behavior]\nexpect_user = 'config'\n", envExpect: "env", flagSet: true, percent: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.configText), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"TG_CLI_THROTTLE_PERCENT": tc.envPercent, "TG_CLI_EXPECT_USER": tc.envExpect}
			a := &app{config: cfg, deps: Deps{Getenv: func(k string) string { return env[k] }}}
			cmd := &cobra.Command{}
			cmd.Flags().String("expect-user", "", "")
			if tc.flagSet {
				if err := cmd.Flags().Set("expect-user", tc.flag); err != nil {
					t.Fatal(err)
				}
			}
			got, _, err := a.daemonThrottle()
			if err != nil {
				t.Fatal(err)
			}
			if got.Percent != tc.percent || a.expectedUser(cmd) != tc.expect {
				t.Fatalf("percent=%v expect=%q", got.Percent, a.expectedUser(cmd))
			}
		})
	}
}

func TestAudit01_ProfileSelectionConfigDefaultAndFlag(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("home = %q\ndefault_profile = 'configured'\n[behavior]\nflood_autowait = '3s'\n", e.home)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{config: cfg, deps: Deps{Getenv: func(k string) string { return e.env[k] }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "default", "")
	profile, paths, err := a.profile(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if profile != "configured" || paths.Dir != filepath.Join(e.home, "configured") {
		t.Fatalf("profile=%s path=%s", profile, paths.Dir)
	}
	if err := cmd.Flags().Set("profile", "chosen"); err != nil {
		t.Fatal(err)
	}
	profile, _, err = a.profile(cmd)
	if err != nil || profile != "chosen" {
		t.Fatalf("profile=%s err=%v", profile, err)
	}
}

func TestAudit01_ConfigWarningsPrintedOnceAndInvalidConfigExitsUsage(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := "[profiles.one]\nproxy = 'socks5://user:secret@localhost:1080'\n[profiles.two]\nproxy = 'socks5://user:secret@localhost:1080'\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	result := e.run("schema")
	if result.code != exitcode.OK || strings.Count(result.stderr, "warning:") != 1 || strings.Contains(result.stderr, "secret") {
		t.Fatalf("code=%d stderr=%q", result.code, result.stderr)
	}
	if err := os.WriteFile(path, []byte("unknown = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := e.run("schema"); result.code != exitcode.Usage {
		t.Fatalf("invalid config exit=%d", result.code)
	}
}
