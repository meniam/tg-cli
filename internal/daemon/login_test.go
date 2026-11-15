package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

func TestCanceledInputRequestKeepsLoginReady(t *testing.T) {
	for _, step := range []struct {
		name  string
		state string
		call  func(*loginManager, context.Context) error
	}{
		{"code", rpc.AuthCodeRequired, func(m *loginManager, ctx context.Context) error {
			_, err := m.code(ctx, "12345")
			return err
		}},
		{"resend", rpc.AuthCodeRequired, func(m *loginManager, ctx context.Context) error {
			_, err := m.resend(ctx)
			return err
		}},
		{"password", rpc.AuthPasswordRequired, func(m *loginManager, ctx context.Context) error {
			_, err := m.password(ctx, "secret")
			return err
		}},
	} {
		t.Run(step.name, func(t *testing.T) {
			flowCtx, flowCancel := context.WithCancel(context.Background())
			defer flowCancel()
			f := &loginFlow{
				ctx: flowCtx, state: step.state,
				events: make(chan loginEvent, 1),
				codeCh: make(chan string), passwordCh: make(chan string), resendCh: make(chan struct{}),
			}
			m := &loginManager{flow: f}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := step.call(m, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want cancellation", err)
			}
			if got := m.state(); got != step.state {
				t.Fatalf("login state is %q, want %q for a retry", got, step.state)
			}
		})
	}
}

func TestLoginReleasesClientBeforeAuthorizationCallback(t *testing.T) {
	client := newFake()
	close(client.ready)
	gate := &telegramAdmission{client: client}
	m := newLoginManager(context.Background(), client)
	m.acquire = gate.acquire
	callback := make(chan error, 1)
	m.onAuthorized = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		callback <- gate.drain(ctx, 0)
		return nil
	}
	res, err := m.start(context.Background(), "+123")
	if err != nil || res.State != rpc.AuthCodeRequired {
		t.Fatalf("start: %+v %v", res, err)
	}
	res, err = m.code(context.Background(), client.wantCode)
	if err != nil || res.State != rpc.AuthAuthorized {
		t.Fatalf("code: %+v %v", res, err)
	}
	if err := <-callback; err != nil {
		t.Fatal(err)
	}
}
