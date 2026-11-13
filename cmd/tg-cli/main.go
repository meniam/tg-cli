// Command tg-cli is a console client for a personal Telegram account.
// "tg-cli" is a working name. This file only wires the pieces together.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"tg-cli/internal/cli"
	"tg-cli/internal/daemon"
	"tg-cli/internal/tgclient"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first interrupt restore the default behavior, so that a second
	// Ctrl-C ends the process even if something blocks.
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := cli.Execute(ctx, os.Args[1:], cli.DefaultDeps(newTelegram))
	stop()
	os.Exit(code)
}

func newTelegram(cfg daemon.TelegramConfig) (daemon.Telegram, error) {
	return tgclient.New(tgclient.Config{
		AppID:       cfg.AppID,
		AppHash:     cfg.AppHash,
		SessionPath: cfg.SessionPath,
		DeviceModel: cfg.DeviceModel,
	}), nil
}
