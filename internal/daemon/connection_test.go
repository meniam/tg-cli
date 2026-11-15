package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"tg-cli/internal/rpc"
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
