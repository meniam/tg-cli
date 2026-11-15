package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyLockLifecycle(t *testing.T) {
	var locks keyLocks
	release, err := locks.lock(context.Background(), 1, "key")
	if err != nil {
		t.Fatal(err)
	}
	other, err := locks.lock(context.Background(), 1, "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	otherChat, err := locks.lock(context.Background(), 2, "key")
	if err != nil {
		t.Fatal(err)
	}
	otherChat()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		unlock, err := locks.lock(ctx, 1, "key")
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not cancel")
	}
	waiting := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(waiting)
		unlock, err := locks.lock(context.Background(), 1, "key")
		if err == nil {
			close(acquired)
			unlock()
		}
	}()
	<-waiting
	select {
	case <-acquired:
		t.Fatal("same key acquired while held")
	default:
	}
	release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire")
	}
	deadline := time.After(time.Second)
	for {
		locks.mu.Lock()
		n := len(locks.entries)
		locks.mu.Unlock()
		if n == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("entries retained")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
