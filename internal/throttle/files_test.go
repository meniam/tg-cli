package throttle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFileLaneAndNormalConcurrency(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		c := newFakeClock()
		l := limiterForTest(t, c, nil)
		files := make([]*Permit, fileConcurrency)
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
		// A second later the burst is refilled, so only the concurrency limit holds the ninth request.
		c.Advance(time.Second)
		awaitFileQueue(t, l, 1)
		files[0].Release()
		receive(t, ninth).Release()
		for _, p := range files[1:] {
			p.Release()
		}
	})
}

func TestFilePauseAndCanceledWaiter(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
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
		for i := 0; i < fileConcurrency; i++ {
			p, err := l.Acquire(context.Background(), File, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Release()
		}
	})
}

// TestFileLaneYieldsToQueuedNormalRequest checks fileMayRun: while a normal
// request waits for a token and a concurrency slot is free, a file request with
// a full file burst must still queue behind it.
func TestFileLaneYieldsToQueuedNormalRequest(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		c := newFakeClock()
		l := limiterForTest(t, c, nil)
		for i := 0; i < DefaultGeneralBurst; i++ {
			p, err := l.Acquire(context.Background(), General, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			p.Release()
		}
		normal := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		file := acquireAsync(l, context.Background(), File, "", nil)
		awaitFileQueue(t, l, 1)
		select {
		case <-file:
			t.Fatal("file request overtook a queued normal request")
		default:
		}
		c.Advance(time.Second/18 + time.Nanosecond)
		normalPermit := receive(t, normal)
		filePermit := receive(t, file)
		normalPermit.Release()
		filePermit.Release()
	})
}
