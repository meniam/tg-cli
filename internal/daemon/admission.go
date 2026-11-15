package daemon

import (
	"context"
	"sync"
	"time"

	"tg-cli/internal/rpc"
)

// telegramAdmission prevents a client from being replaced while an admitted
// operation still uses it. Cancellation is cooperative: a canceled operation
// remains admitted until it releases its lease.
type telegramAdmission struct {
	mu      sync.Mutex
	client  Telegram
	blocked bool
	leases  map[*telegramLease]struct{}
	drained chan struct{}
}

type telegramLease struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (g *telegramAdmission) acquire(ctx context.Context) (context.Context, Telegram, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if g.blocked || g.client == nil {
		return nil, nil, nil, rpc.NewError(rpc.CodeOffline, "telegram is offline")
	}
	ctx, cancel := context.WithCancel(ctx)
	lease := &telegramLease{cancel: cancel}
	if g.leases == nil {
		g.leases = make(map[*telegramLease]struct{})
	}
	g.leases[lease] = struct{}{}
	release := func() {
		lease.once.Do(func() {
			cancel()
			g.mu.Lock()
			delete(g.leases, lease)
			if g.blocked && len(g.leases) == 0 && g.drained != nil {
				close(g.drained)
				g.drained = nil
			}
			g.mu.Unlock()
		})
	}
	return ctx, g.client, release, nil
}

// block rejects new leases and returns a channel closed when all leases end.
// The caller serializes block/unblock with the client lifecycle.
func (g *telegramAdmission) block() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = true
	if g.drained == nil {
		g.drained = make(chan struct{})
		if len(g.leases) == 0 {
			close(g.drained)
		}
	}
	return g.drained
}

func (g *telegramAdmission) cancelLeases() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for lease := range g.leases {
		lease.cancel()
	}
}

func (g *telegramAdmission) unblock(client Telegram) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.client = client
	g.blocked = false
	g.drained = nil
}

// drain blocks new operations, then gives admitted work a grace period. After
// the grace period it requests cancellation but still waits for every lease:
// cancellation alone does not prove that an operation stopped using the client.
func (g *telegramAdmission) drain(ctx context.Context, grace time.Duration) error {
	drained := g.block()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		g.cancelLeases()
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// acquireTelegram leases the current client for a complete operation. The
// returned context must be passed to every call made under this lease.
func (d *daemon) acquireTelegram(ctx context.Context) (context.Context, Telegram, func(), error) {
	return d.admission.acquire(ctx)
}

type telegramContextKey struct{}

// telegram returns the client leased for the operation. Direct handler tests
// and startup code without a lease use the original client.
func (d *daemon) telegram(ctx context.Context) Telegram {
	if client, ok := ctx.Value(telegramContextKey{}).(Telegram); ok {
		return client
	}
	return d.opts.Telegram
}

func (g *telegramAdmission) isBlocked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked
}
