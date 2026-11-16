package tgclient

import (
	"context"
	"errors"
	"testing"
)

func TestManagerCanceledWhileLocked(t *testing.T) {
	c := &Client{}
	if err := c.mgrMu.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.mgrMu.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.manager(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("manager: %v", err)
	}
}
