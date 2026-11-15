package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/netconf"
)

func TestRestartFactoryPreservesConfiguredProfile(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "tgr-config-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	path := filepath.Join(t.TempDir(), "config.toml")
	text := fmt.Sprintf("home = %q\ndefault_profile = 'work'\n[files]\ndatabase = 'archive.db'\nsession = 'custom.session'\n[behavior]\nthrottle_percent = 35\nflood_autowait = '3s'\n[profiles.work]\ndevice = 'Test device'\ntransport = 'auto'\nproxy = 'socks5://proxy.test:1080'\n", home)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls []daemon.TelegramConfig
	env := map[string]string{"TG_API_ID": "42", "TG_API_HASH": "synthetic-hash"}
	a := &app{config: loaded, deps: Deps{ConfigPath: path, Getenv: func(key string) string { return env[key] }, NewTelegram: func(cfg daemon.TelegramConfig) (daemon.Telegram, error) { calls = append(calls, cfg); return nil, nil }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "default", "")
	cmd.Flags().String("client", "", "")
	cmd.Flags().String("device", "", "")
	cmd.Flags().String("transport", "", "")
	cmd.Flags().String("proxy", "", "")
	profile, paths, err := a.profile(cmd)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.credentials(cmd, paths)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Network, err = a.networkSettings(cmd, paths, false)
	if err != nil {
		t.Fatal(err)
	}
	throttleCfg, limiter, err := a.daemonThrottle()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Throttle = limiter
	cfg.DeviceModel, err = a.deviceModel(cmd, paths)
	if err != nil {
		t.Fatal(err)
	}
	_, factory, err := a.telegramFactory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	replacement := cfg
	replacement.Network.Transport = netconf.WSS
	if _, err := factory(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("factory calls = %d", len(calls))
	}
	if profile != "work" || cfg.SessionPath != filepath.Join(home, "work", "custom.session") || cfg.StatePath != filepath.Join(home, "work", "archive.db") || cfg.DeviceModel != "Test device" || throttleCfg.Percent != 35 || cfg.Throttle.FloodAutoWait() != 3*time.Second {
		t.Fatal("profile settings were not applied")
	}
	initial := cfg
	initial.Network.Transport = netconf.TCP
	if !reflect.DeepEqual(calls[0], initial) || !reflect.DeepEqual(calls[1], replacement) || calls[1].Throttle != calls[0].Throttle {
		t.Fatal("replacement changed configured client settings")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := factory(ctx, replacement); !errors.Is(err, context.Canceled) || len(calls) != 2 {
		t.Fatal("canceled factory call constructed a client")
	}
}
