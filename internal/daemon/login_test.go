package daemon

import (
	"context"
	"errors"
	"testing"

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
