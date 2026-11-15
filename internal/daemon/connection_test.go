package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"tg-cli/internal/rpc"
	"time"
)

type disconnectedTelegram struct {
	*fakeTelegram
	calls int
}

func (f *disconnectedTelegram) Connection() rpc.ConnectionInfo {
	return rpc.ConnectionInfo{Transport: "wss", Connection: "disconnected", DC: 2}
}
func (f *disconnectedTelegram) Self(context.Context) (*rpc.User, error) { f.calls++; return nil, nil }
func TestStatusDisconnectedUsesCachedAccount(t *testing.T) {
	f := &disconnectedTelegram{fakeTelegram: newFake()}
	d := &daemon{opts: Options{Telegram: f}, login: newLoginManager(context.Background(), f), srv: rpc.NewServer(nil)}
	d.account.set(&rpc.User{ID: 42})
	result, err := d.status(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	s := result.(rpc.StatusResult)
	if s.Connected || !s.Authorized || s.User == nil || s.User.ID != 42 || s.Connection != "disconnected" || f.calls != 0 {
		t.Fatalf("wrong offline status: %+v calls=%d", s, f.calls)
	}
}

type offlineWaiter struct {
	*disconnectedTelegram
	duration time.Duration
}

func (f *offlineWaiter) WaitConnected(_ context.Context, d time.Duration) error {
	f.duration = d
	return context.DeadlineExceeded
}
func TestOfflineRequestFailsBeforeTelegramCall(t *testing.T) {
	f := &offlineWaiter{disconnectedTelegram: &disconnectedTelegram{fakeTelegram: newFake()}}
	d := &daemon{opts: Options{Telegram: f}}
	err := d.requireAccount(rpc.WithConnectionWait(context.Background()))
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeOffline {
		t.Fatalf("wrong offline error: %v", err)
	}
	if f.calls != 0 || f.duration != 15*time.Second {
		t.Fatalf("calls=%d wait=%v", f.calls, f.duration)
	}
}
