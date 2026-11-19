package daemon

import (
	"context"
	"errors"
	"testing"

	"tg-cli/internal/rpc"
)

// A canceled caller gets its own error, not an offline answer.
func TestAudit03AcquireWithACanceledContext(t *testing.T) {
	g := &telegramAdmission{client: &fakeTelegram{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := g.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire = %v, want context.Canceled", err)
	}
	if len(g.leases) != 0 {
		t.Fatalf("a lease was registered for a canceled caller: %d", len(g.leases))
	}
}

// Without a client the answer is offline, and no lease is taken.
func TestAudit03AcquireWithoutAClientIsOffline(t *testing.T) {
	g := &telegramAdmission{}
	_, _, _, err := g.acquire(context.Background())
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeOffline {
		t.Fatalf("acquire = %v, want the offline error", err)
	}
	if len(g.leases) != 0 {
		t.Fatalf("leases = %d", len(g.leases))
	}
}
