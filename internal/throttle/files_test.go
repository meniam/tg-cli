package throttle

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func awaitFileQueue(t *testing.T, l *Limiter, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		l.mu.Lock()
		got := len(l.fileQueue)
		l.mu.Unlock()
		if got == n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("file queue = %d, want %d", got, n)
		default:
			runtime.Gosched()
		}
	}
}

func TestFileLaneAndNormalConcurrency(t *testing.T) {
	c := newFakeClock()
	l := limiterForTest(t, c, nil)
	files := make([]*Permit, 8)
	for i := range files {
		var err error
		files[i], err = l.Acquire(context.Background(), File, "", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	ninth := acquireAsync(l, context.Background(), File, "", nil)
	awaitFileQueue(t, l, 1)
	wait := new(Wait)
	normal, err := l.Acquire(context.Background(), General, "", wait)
	if err != nil || wait.Duration() != 0 {
		t.Fatalf("normal wait=%v err=%v", wait.Duration(), err)
	}
	normal.Release()
	files[0].Release()
	c.Advance(time.Second)
	receive(t, ninth).Release()
	for _, p := range files[1:] {
		p.Release()
	}
}

func TestFilePauseAndCanceledWaiter(t *testing.T) {
	c := newFakeClock()
	l := limiterForTest(t, c, nil)
	l.Pause(3 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	canceled := acquireAsync(l, ctx, File, "", nil)
	awaitFileQueue(t, l, 1)
	general := acquireAsync(l, context.Background(), General, "", nil)
	awaitQueue(t, l, 1)
	cancel()
	if r := <-canceled; !errors.Is(r.err, context.Canceled) {
		t.Fatalf("canceled: %v", r.err)
	}
	c.Advance(3 * time.Second)
	receive(t, general).Release()
	for i := 0; i < 8; i++ {
		p, err := l.Acquire(context.Background(), File, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Release()
	}
}
