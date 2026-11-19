package ctxlock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// audit03Ctx is a context whose Err stays nil until trip is called. It lets a
// test choose which branch of Acquire sees the cancellation.
type audit03Ctx struct {
	context.Context
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newAudit03Ctx() *audit03Ctx {
	return &audit03Ctx{Context: context.Background(), done: make(chan struct{})}
}

func (c *audit03Ctx) Done() <-chan struct{} { return c.done }

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

// With the lock held, Acquire passes the early check and can only leave the
// wait through the cancellation.
func TestAudit03CancellationWhileWaiting(t *testing.T) {
	var lock Lock
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := newAudit03Ctx()
	done := make(chan error, 1)
	go func() { done <- lock.Acquire(ctx) }()
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

// Release hands the lock to a waiter.
func TestAudit03ReleaseHandsTheLockToAWaiter(t *testing.T) {
	var lock Lock
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	go func() { acquired <- lock.Acquire(context.Background()) }()
	select {
	case err := <-acquired:
		t.Fatalf("a second holder got the lock: %v", err)
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
