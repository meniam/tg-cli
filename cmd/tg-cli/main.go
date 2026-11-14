// Command tg is a console client for a personal Telegram account. The binary is
// installed as "tg"; the module and this directory keep the working name
// "tg-cli". This file only wires the pieces together.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"tg-cli/internal/cli"
	"tg-cli/internal/daemon"
	"tg-cli/internal/store"
	"tg-cli/internal/tgclient"
)

// stateKV opens the profile database only after the daemon owns its lock.
// It uses a separate pool because the Telegram client is assembled before Run
// opens the Daemon's database; both pools belong to the same process.
type stateKV struct {
	mu   sync.Mutex
	path string
	db   *store.DB
}

func (s *stateKV) open() (*store.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		db, err := store.Open(s.path)
		if err != nil {
			return nil, err
		}
		s.db = db
	}
	return s.db, nil
}
func (s *stateKV) Get(ctx context.Context, key string) (json.RawMessage, bool, error) {
	db, err := s.open()
	if err != nil {
		return nil, false, err
	}
	return db.GetAccountState(ctx, key)
}
func (s *stateKV) Set(ctx context.Context, key string, value json.RawMessage) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	return db.SetAccountState(ctx, key, value)
}
func (s *stateKV) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

type managedTelegram struct {
	*tgclient.Client
	state *stateKV
}

func (m *managedTelegram) Run(ctx context.Context) error {
	if m.state != nil {
		defer m.state.Close()
	}
	return m.Client.Run(ctx)
}

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
	var state *stateKV
	if cfg.StatePath != "" {
		state = &stateKV{path: cfg.StatePath}
	}
	client := tgclient.New(tgclient.Config{
		AppID:       cfg.AppID,
		AppHash:     cfg.AppHash,
		SessionPath: cfg.SessionPath,
		DeviceModel: cfg.DeviceModel,
		Throttle:    cfg.Throttle,
		StateKV:     state,
	})
	return &managedTelegram{Client: client, state: state}, nil
}
