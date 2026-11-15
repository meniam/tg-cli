package cli

import (
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tg-cli/internal/daemon"
)

func TestNetworkSettingsPersistence(t *testing.T) {
	dir := t.TempDir()
	paths := daemon.Paths{Dir: dir, Transport: filepath.Join(dir, "transport"), Proxy: filepath.Join(dir, "proxy")}
	var err error
	a := &app{deps: Deps{Getenv: func(string) string { return "" }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("transport", "", "")
	cmd.Flags().String("proxy", "", "")
	_ = cmd.Flags().Set("transport", "wss")
	_ = cmd.Flags().Set("proxy", "socks5://alice:secret-marker@localhost:1080")
	s, err := a.networkSettings(cmd, paths, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Proxy.Redacted() != "socks5://localhost:1080" {
		t.Fatal(s)
	}
	st, err := os.Stat(paths.Proxy)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("wrong permissions: %v %v", st, err)
	}
	cmd2 := &cobra.Command{}
	cmd2.Flags().String("transport", "", "")
	cmd2.Flags().String("proxy", "", "")
	s, err = a.networkSettings(cmd2, paths, false)
	if err != nil || s.Transport != "wss" || s.Proxy == nil {
		t.Fatalf("settings not restored: %v %v", s, err)
	}
	_ = cmd2.Flags().Set("proxy", "none")
	if _, err = a.networkSettings(cmd2, paths, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.Proxy)
	if err != nil || strings.TrimSpace(string(data)) != "none" {
		t.Fatalf("proxy was not cleared: %v", err)
	}
}
func TestNetworkBadProxyDoesNotExposeSecret(t *testing.T) {
	dir := t.TempDir()
	paths := daemon.Paths{Dir: dir, Transport: filepath.Join(dir, "transport"), Proxy: filepath.Join(dir, "proxy")}
	var err error
	a := &app{deps: Deps{Getenv: func(string) string { return "" }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("transport", "", "")
	cmd.Flags().String("proxy", "", "")
	_ = cmd.Flags().Set("proxy", "tg://proxy?server=x&port=443&secret=secret-marker")
	_, err = a.networkSettings(cmd, paths, true)
	if err == nil || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("unsafe error: %v", err)
	}
	if _, err := os.Stat(paths.Proxy); !os.IsNotExist(err) {
		t.Fatal("invalid settings were saved")
	}
}
