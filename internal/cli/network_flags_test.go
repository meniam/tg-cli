package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
)

func reloadNetworkFixture(t *testing.T, a *app, paths daemon.Paths) {
	t.Helper()
	a.config.Profiles = nil
	err := daemon.WithProfileDatabase(paths, func(db daemon.ProfileDatabase) error {
		values, err := db.Settings(context.Background())
		if err != nil {
			return err
		}
		return a.applyDatabaseSettings(paths, values)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNetworkSettingsPersistence(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tgn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	paths, err := daemon.ResolvePaths(dir, filepath.Join(dir, "db"), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{config: cfg, deps: Deps{ConfigPath: path, Getenv: func(string) string { return "" }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("transport", "", "")
	cmd.Flags().String("proxy", "", "")
	audit02SetFlag(t, cmd, "transport", "wss")
	audit02SetFlag(t, cmd, "proxy", "socks5://alice:secret-marker@localhost:1080")
	s, err := a.networkSettings(cmd, paths, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Proxy.Redacted() != "socks5://localhost:1080" {
		t.Fatal(s)
	}
	st, err := os.Stat(paths.Database)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("wrong permissions: %v %v", st, err)
	}
	reloadNetworkFixture(t, a, paths)
	cmd2 := &cobra.Command{}
	cmd2.Flags().String("transport", "", "")
	cmd2.Flags().String("proxy", "", "")
	s, err = a.networkSettings(cmd2, paths, false)
	if err != nil || s.Transport != "wss" || s.Proxy == nil {
		t.Fatalf("settings not restored: %v %v", s, err)
	}
	audit02SetFlag(t, cmd2, "proxy", "none")
	if _, err = a.networkSettings(cmd2, paths, true); err != nil {
		t.Fatal(err)
	}
	err = daemon.WithProfileDatabase(paths, func(db daemon.ProfileDatabase) error {
		values, err := db.Settings(context.Background())
		if err != nil {
			return err
		}
		if string(values["profile.proxy"]) != `"none"` {
			t.Fatalf("proxy was not cleared: %s", values["profile.proxy"])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestNetworkBadProxyDoesNotExposeSecret(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tgn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	paths, err := daemon.ResolvePaths(dir, filepath.Join(dir, "db"), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{config: cfg, deps: Deps{ConfigPath: path, Getenv: func(string) string { return "" }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("transport", "", "")
	cmd.Flags().String("proxy", "", "")
	audit02SetFlag(t, cmd, "proxy", "tg://proxy?server=x&port=443&secret=secret-marker")
	_, err = a.networkSettings(cmd, paths, true)
	if err == nil || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("unsafe error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid settings were saved")
	}
}

func TestNetworkAutoSettings(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tgn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	paths, err := daemon.ResolvePaths(dir, filepath.Join(dir, "db"), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{config: cfg, deps: Deps{ConfigPath: path, Getenv: func(string) string { return "" }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("transport", "", "")
	cmd.Flags().String("proxy", "", "")
	audit02SetFlag(t, cmd, "transport", "auto")
	audit02SetFlag(t, cmd, "proxy", "http://user:password@proxy.test:8080")
	s, err := a.networkSettings(cmd, paths, true)
	if err != nil || s.Transport != "auto" || s.Proxy.Redacted() != "http://proxy.test:8080" {
		t.Fatalf("settings: %v %v", s, err)
	}
	reloadNetworkFixture(t, a, paths)
	cmd2 := &cobra.Command{}
	cmd2.Flags().String("transport", "", "")
	cmd2.Flags().String("proxy", "", "")
	got, err := a.networkSettings(cmd2, paths, false)
	if err != nil || got.Transport != "auto" || got.Proxy.Redacted() != "http://proxy.test:8080" {
		t.Fatalf("restored: %v %v", got, err)
	}
}
