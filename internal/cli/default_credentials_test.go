package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/sessionstr"
)

func TestDefaultClientCredentialPriority(t *testing.T) {
	for _, tc := range []struct {
		name    string
		own     bool
		flag    string
		stored  string
		appID   int
		want    int
		fetches int
	}{
		{"default desktop", false, "", "", 11111, exitcode.OK, 1},
		{"own application", true, "", "", 12345, exitcode.OK, 0},
		{"explicit flag", false, "ios", "", 22222, exitcode.OK, 1},
		{"flag conflicts with environment", true, "ios", "", 0, exitcode.Usage, 0},
		{"stored preset", false, "", "ios", 22222, exitcode.OK, 1},
		{"own application overrides stored", true, "", "ios", 12345, exitcode.OK, 0},
		{"flag overrides stored", false, "desktop", "ios", 11111, exitcode.OK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if !tc.own {
				e.usePreset()
			}
			if tc.stored != "" {
				raw, _ := json.Marshal(tc.stored)
				e.setProfileSettings("default", map[string]json.RawMessage{"profile.client": raw})
			}
			previous := runDaemon
			defer func() { runDaemon = previous }()
			var got daemon.TelegramConfig
			var preset string
			called := false
			runDaemon = func(ctx context.Context, opts daemon.Options) error {
				return daemon.WithProfileDatabase(opts.Paths, func(db daemon.ProfileDatabase) error {
					if err := opts.Prepare(ctx, db, &opts); err != nil {
						return err
					}
					got = opts.TelegramConfig
					preset = opts.ClientPreset
					called = true
					return nil
				})
			}
			args := []string{"daemon", "start", "--foreground"}
			if tc.flag != "" {
				args = append(args, "--client", tc.flag)
			}
			r := e.run(args...)
			if r.code != tc.want {
				t.Fatalf("exit=%d want=%d: %s", r.code, tc.want, r.stdout)
			}
			if tc.want == exitcode.OK && (!called || got.AppID != tc.appID) {
				t.Fatalf("credentials: called=%t app ID=%d", called, got.AppID)
			}
			if tc.name == "default desktop" && preset != "desktop" {
				t.Fatalf("default session preset=%q", preset)
			}
			if tc.name == "own application" && preset != "" {
				t.Fatalf("own application session preset=%q", preset)
			}
			if tc.want != exitcode.OK && called {
				t.Fatal("client created on conflict")
			}
			if e.fetchCalls != tc.fetches {
				t.Fatalf("preset fetches=%d want=%d", e.fetchCalls, tc.fetches)
			}
		})
	}
}

func TestOfflineSessionExportUsesEffectiveDefaultPreset(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("own=%t", own), func(t *testing.T) {
			e := newEnv(t)
			if !own {
				e.usePreset()
			}
			paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			raw := audit02SessionFixture(t, 42)
			if err := daemon.WithProfileLock(paths, func() error { return daemon.WriteSession(paths, raw) }); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "session.txt")
			r := e.run("session", "export", "--out", output, "--yes")
			if r.code != exitcode.OK {
				t.Fatalf("export: %d %s", r.code, r.stdout)
			}
			encoded, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := sessionstr.Decode(string(encoded))
			if err != nil {
				t.Fatal(err)
			}
			want := "desktop"
			if own {
				want = ""
			}
			if decoded.Client != want {
				t.Fatalf("preset=%q want=%q", decoded.Client, want)
			}
		})
	}
}
