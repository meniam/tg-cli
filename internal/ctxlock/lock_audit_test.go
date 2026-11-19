package ctxlock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// audit03Ctx is a context whose Err stays nil until trip is called, and which
// reports on entered the moment Acquire asks for Done: that call happens only
// after the early Err check passed, so it marks a waiter that is blocked in
// the select.
type audit03Ctx struct {
	context.Context
	done    chan struct{}
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	err     error
}

func audit03NewCtx() *audit03Ctx {
	return &audit03Ctx{Context: context.Background(), done: make(chan struct{}), entered: make(chan struct{})}
}

func (c *audit03Ctx) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.done
}

func (c *audit03Ctx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *audit03Ctx) trip() {
	c.mu.Lock()
	c.err = context.Canceled
	c.mu.Unlock()
	close(c.done)
}

func audit03WaitEntered(t *testing.T, ctx *audit03Ctx) {
	t.Helper()
	select {
	case <-ctx.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never started waiting")
	}
}

// With the lock held, the waiter is blocked in the select; the cancellation
// arrives only after it got there.
func TestAudit03CancellationWhileWaiting(t *testing.T) {
	var lock Lock
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := audit03NewCtx()
	done := make(chan error, 1)
	go func() { done <- lock.Acquire(ctx) }()
	audit03WaitEntered(t, ctx)
	ctx.trip()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("acquire: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a canceled waiter did not return")
	}
	lock.Release()
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatalf("the lock is lost after a canceled wait: %v", err)
	}
}

// The context reports cancellation only after the token was taken: Acquire
// must give the token back, or the lock is gone for good.
type audit03LateCancelCtx struct {
	context.Context
	calls atomic.Int32
}

func (c *audit03LateCancelCtx) Done() <-chan struct{} { return nil }
func (c *audit03LateCancelCtx) Err() error {
	if c.calls.Add(1) == 1 {
		return nil
	}
	return context.Canceled
}

func TestAudit03TokenIsReturnedWhenCanceledAfterTheWait(t *testing.T) {
	var lock Lock
	ctx := &audit03LateCancelCtx{Context: context.Background()}
	if err := lock.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire = %v, want context.Canceled", err)
	}
	acquired := make(chan error, 1)
	go func() { acquired <- lock.Acquire(context.Background()) }()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the token was not returned: the lock cannot be acquired again")
	}
}

// Release hands the lock to a waiter that is already blocked on it.
func TestAudit03ReleaseHandsTheLockToAWaiter(t *testing.T) {
	var lock Lock
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	waiter := audit03NewCtx()
	acquired := make(chan error, 1)
	go func() { acquired <- lock.Acquire(waiter) }()
	audit03WaitEntered(t, waiter)
	select {
	case err := <-acquired:
		t.Fatalf("a second holder got the lock while it was held: %v", err)
	default:
	}
	lock.Release()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter did not get the lock after release")
	}
}

// Many goroutines never hold the lock together.
func TestAudit03MutualExclusionUnderLoad(t *testing.T) {
	var lock Lock
	var inside, violations atomic.Int32
	shared := 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if err := lock.Acquire(context.Background()); err != nil {
					t.Error(err)
					return
				}
				if inside.Add(1) != 1 {
					violations.Add(1)
				}
				shared++
				inside.Add(-1)
				lock.Release()
			}
		}()
	}
	wg.Wait()
	if violations.Load() != 0 || shared != 16*200 {
		t.Fatalf("violations=%d shared=%d, want 0 and %d", violations.Load(), shared, 16*200)
	}
}
