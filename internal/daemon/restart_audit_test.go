package daemon

import (
	"context"
	"sync"
	"testing"
	"time"
)

// audit05Timeout is a safety net for channel waits: a regression fails the
// test with a message instead of hanging until the go test timeout.
const audit05Timeout = 10 * time.Second

// audit05Recv waits for a value on ch and fails the test, naming what it was
// waiting for, if nothing arrives within audit05Timeout. Call it only from
// the test goroutine.
func audit05Recv[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(audit05Timeout):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// audit05SignalContext closes waiting the first time its Done channel is
// requested, which clientRestarter.do does only when it is about to block.
type audit05SignalContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *audit05SignalContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

// audit05ReadyProbe reports the moment Ready is consulted, which for the
// supervisor means the caller has picked this client and is waiting on it.
type audit05ReadyProbe struct {
	*restartFake
	consulted chan struct{}
}

func (p *audit05ReadyProbe) Ready() <-chan struct{} {
	select {
	case <-p.consulted:
	default:
		close(p.consulted)
	}
	return p.restartFake.Ready()
}

// audit05WaitRefs blocks until the key lock entry has want holders and
// waiters together, so a test can act only after a goroutine has queued.
func audit05WaitRefs(t testing.TB, locks *keyLocks, key idempotencyKey, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), audit05Timeout)
	defer cancel()
	for {
		locks.mu.Lock()
		entry := locks.entries[key]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		locks.mu.Unlock()
		if refs == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("key lock refs = %d, want %d", refs, want)
		case <-time.After(time.Millisecond):
		}
	}
}
