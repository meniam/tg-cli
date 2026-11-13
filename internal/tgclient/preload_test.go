package tgclient

import (
	"context"
	"testing"
)

func TestFailedPreloadCanRetry(t *testing.T) {
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.preloadDialogs(ctx) {
		t.Fatal("canceled preload must fail")
	}
	if c.preloaded {
		t.Fatal("failed preload must leave another attempt possible")
	}
}

func TestCanceledPreloadWaitDoesNotBlock(t *testing.T) {
	c := &Client{preloadDone: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.preloadDialogs(ctx) {
		t.Fatal("canceled waiter must not claim a preload")
	}
}
