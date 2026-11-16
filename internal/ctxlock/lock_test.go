package ctxlock

import (
	"context"
	"errors"
	"testing"
)

func TestCancellationAndRelease(t *testing.T) {
	var lock Lock
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lock.Acquire(ctx) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire: %v", err)
	}
	lock.Release()
	if err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	lock.Release()
}
