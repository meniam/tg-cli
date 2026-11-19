package daemon

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestTelegramAdmissionDrainsAndCancels(t *testing.T) {
	g := &telegramAdmission{client: &fakeTelegram{}}
	ctx, _, release, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	drained := g.block()
	if _, _, _, err := g.acquire(context.Background()); err == nil {
		t.Fatal("admitted while blocked")
	}
	select {
	case <-drained:
		t.Fatal("drained with active lease")
	default:
	}
	g.cancelLeases()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("lease context was not canceled")
	}
	select {
	case <-drained:
		t.Fatal("cancellation must not release the lease")
	default:
	}
	release()
	release()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("lease did not drain")
	}
	g.unblock(&fakeTelegram{})
	_, _, release, err = g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestTelegramAdmissionConcurrentBlock(t *testing.T) {
	g := &telegramAdmission{client: &fakeTelegram{}}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, release, err := g.acquire(context.Background())
			if err == nil {
				release()
			}
		}()
	}
	drained := g.block()
	wg.Wait()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("leases did not drain")
	}
}

func TestTelegramAdmissionDrainWaitsAfterCancellation(t *testing.T) {
	g := &telegramAdmission{client: &fakeTelegram{}}
	ctx, _, release, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.drain(context.Background(), 0) }()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("operation was not canceled")
	}
	// The lease is canceled but still held. Yield the processor many times so
	// that a drain which returns too early has every chance to do it before the
	// release; the loop counts scheduler turns, not wall-clock time.
	for i := 0; i < 1000; i++ {
		select {
		case err := <-done:
			t.Fatalf("drain returned before release: %v", err)
		default:
			runtime.Gosched()
		}
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not complete")
	}
}

func TestTelegramAdmissionDrainContextCancellation(t *testing.T) {
	g := &telegramAdmission{client: &fakeTelegram{}}
	_, _, release, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.drain(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
