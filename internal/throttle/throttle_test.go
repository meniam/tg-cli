package throttle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

type fakeTimer struct {
	clock    *fakeClock
	deadline time.Time
	ch       chan time.Time
	stopped  bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0), timers: make(map[*fakeTimer]struct{})}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(deadline time.Time) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, deadline: deadline, ch: make(chan time.Time, 1)}
	c.timers[t] = struct{}{}
	if !t.deadline.After(c.now) {
		t.ch <- c.now
		delete(c.timers, t)
	}
	return t
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	t.stopped = true
	return active
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for t := range c.timers {
		if !t.stopped && !t.deadline.After(c.now) {
			t.ch <- c.now
			delete(c.timers, t)
		}
	}
	c.mu.Unlock()
}

func limiterForTest(t *testing.T, c *fakeClock, configure func(*Config)) *Limiter {
	t.Helper()
	cfg := DefaultConfig()
	if configure != nil {
		configure(&cfg)
	}
	l, err := New(cfg, c)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

type acquireResult struct {
	permit *Permit
	err    error
}

func acquireAsync(l *Limiter, ctx context.Context, class Class, peer string, waited *Wait) <-chan acquireResult {
	result := make(chan acquireResult, 1)
	go func() {
		p, err := l.Acquire(ctx, class, peer, waited)
		result <- acquireResult{p, err}
	}()
	return result
}

// audit10AwaitLen waits until every goroutine of the synctest bubble is blocked
// and then requires the selected queue to hold exactly n requests. Unlike a
// polling loop it cannot return before the limiter reacted to the last clock step.
func audit10AwaitLen(t *testing.T, l *Limiter, name string, queue func(*Limiter) int, n int) {
	t.Helper()
	synctest.Wait()
	l.mu.Lock()
	got := queue(l)
	l.mu.Unlock()
	if got != n {
		t.Fatalf("%s queue length = %d, want %d", name, got, n)
	}
}

func awaitQueue(t *testing.T, l *Limiter, n int) {
	t.Helper()
	audit10AwaitLen(t, l, "normal", func(l *Limiter) int { return len(l.queue) }, n)
}

func awaitBackgroundQueue(t *testing.T, l *Limiter, n int) {
	t.Helper()
	audit10AwaitLen(t, l, "background", func(l *Limiter) int { return len(l.backgroundQueue) }, n)
}

func awaitFileQueue(t *testing.T, l *Limiter, n int) {
	t.Helper()
	audit10AwaitLen(t, l, "file", func(l *Limiter) int { return len(l.fileQueue) }, n)
}

// audit10Test runs body in a synctest bubble: the fake clock and the limiter
// goroutines live inside it, so synctest.Wait is a real synchronisation point.
func audit10Test(t *testing.T, body func(t *testing.T)) {
	t.Helper()
	synctest.Test(t, body)
}

func receive(t *testing.T, result <-chan acquireResult) *Permit {
	t.Helper()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.permit
	case <-time.After(2 * time.Second):
		t.Fatal("request was not admitted")
		return nil
	}
}

func TestRatesBurstsAndPeerKeys(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, nil)
		for i := 0; i < DefaultGeneralBurst; i++ {
			p, err := l.Acquire(context.Background(), General, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			p.Release()
		}
		waited := new(Wait)
		general := acquireAsync(l, context.Background(), General, "", waited)
		awaitQueue(t, l, 1)
		clock.Advance(time.Second/18 + time.Nanosecond)
		receive(t, general).Release()
		if waited.Duration() < time.Second/18 {
			t.Fatalf("general delay = %s", waited.Duration())
		}

		private := limiterForTest(t, clock, nil)
		p, err := private.Acquire(context.Background(), PrivateSend, "alice", nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
		other, err := private.Acquire(context.Background(), PrivateSend, "bob", nil)
		if err != nil {
			t.Fatal(err)
		}
		other.Release()
		second := acquireAsync(private, context.Background(), PrivateSend, "alice", nil)
		awaitQueue(t, private, 1)
		// One second is the unscaled private rate; at 60 percent the request must still wait.
		clock.Advance(time.Second)
		awaitQueue(t, private, 1)
		clock.Advance(2*time.Second/3 + time.Nanosecond)
		receive(t, second).Release()

		group := limiterForTest(t, clock, nil)
		p, err = group.Acquire(context.Background(), GroupSend, "group-1", nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
		second = acquireAsync(group, context.Background(), GroupSend, "group-1", nil)
		awaitQueue(t, group, 1)
		// Three seconds is the unscaled group rate (20 per minute); 60 percent needs five.
		clock.Advance(4 * time.Second)
		awaitQueue(t, group, 1)
		clock.Advance(time.Second)
		receive(t, second).Release()
	})
}

func TestFIFOAndCanceledRequestSpendsNoToken(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, func(c *Config) { c.GeneralBurst = 1; c.GeneralPerSecond = 1; c.Percent = 100 })
		first, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		first.Release()
		a := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		bctx, cancelB := context.WithCancel(context.Background())
		b := acquireAsync(l, bctx, General, "", nil)
		awaitQueue(t, l, 2)
		c := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 3)
		cancelB()
		select {
		case result := <-b:
			if result.err != context.Canceled {
				t.Fatalf("canceled request: %v", result.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canceled request stayed queued")
		}
		awaitQueue(t, l, 2)
		clock.Advance(time.Second)
		receive(t, a).Release()
		awaitQueue(t, l, 1)
		clock.Advance(time.Second)
		receive(t, c).Release()
	})
}

func TestFloodPauseBlocksAllClassesAndDoesNotShorten(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, nil)
		l.Pause(5 * time.Second)
		l.Pause(time.Second)
		generalWait := new(Wait)
		general := acquireAsync(l, context.Background(), General, "", generalWait)
		awaitQueue(t, l, 1)
		private := acquireAsync(l, context.Background(), PrivateSend, "alice", nil)
		awaitQueue(t, l, 2)
		clock.Advance(4 * time.Second)
		awaitQueue(t, l, 2)
		clock.Advance(time.Second)
		receive(t, general).Release()
		receive(t, private).Release()
		if generalWait.Duration() != 5*time.Second {
			t.Fatalf("pause wait = %s", generalWait.Duration())
		}
	})
}

func TestConcurrencyBound(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, nil)
		a, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		c := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		l.mu.Lock()
		active := l.active
		l.mu.Unlock()
		if active != 2 {
			t.Fatalf("active = %d, want 2", active)
		}
		a.Release()
		d := receive(t, c)
		l.mu.Lock()
		active = l.active
		l.mu.Unlock()
		if active != 2 {
			t.Fatalf("active = %d after release, want 2", active)
		}
		b.Release()
		d.Release()
	})
}

// TestBackgroundQueueYieldsToNormalAndRunsAlone also covers the low-priority
// upload admission: a background request waits for a normal one and runs alone.
func TestBackgroundQueueYieldsToNormalAndRunsAlone(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		l := limiterForTest(t, newFakeClock(), func(c *Config) { c.Concurrency = 1; c.GeneralBurst = 5 })
		hold, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		background := acquireAsync(l, WithBackground(context.Background()), General, "", nil)
		awaitBackgroundQueue(t, l, 1)
		normal := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		hold.Release()
		normalPermit := receive(t, normal)
		synctest.Wait()
		select {
		case result := <-background:
			result.permit.Release()
			t.Fatal("background request overtook normal request")
		default:
		}
		normalPermit.Release()
		receive(t, background).Release()

		hold, err = l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		alone := acquireAsync(l, WithBackground(context.Background()), General, "", nil)
		awaitBackgroundQueue(t, l, 1)
		hold.Release()
		receive(t, alone).Release()
	})
}

func TestBackgroundFloodGateFastRejectsNormal(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, nil)
		l.PauseBackground(120 * time.Second)
		_, err := l.Acquire(context.Background(), General, "", nil)
		var flood *FloodWaitError
		if !errors.As(err, &flood) || flood.RetryAfterSeconds != 120 {
			t.Fatalf("fast flood error = %v", err)
		}
		background := acquireAsync(l, WithBackground(context.Background()), General, "", nil)
		awaitBackgroundQueue(t, l, 1)
		clock.Advance(120 * time.Second)
		receive(t, background).Release()
	})
}

func TestBackgroundFloodGateRejectsAlreadyQueuedNormal(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		l := limiterForTest(t, newFakeClock(), func(c *Config) { c.Concurrency = 1 })
		hold, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		normal := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		l.PauseBackground(120 * time.Second)
		select {
		case result := <-normal:
			var flood *FloodWaitError
			if !errors.As(result.err, &flood) || flood.RetryAfterSeconds != 120 {
				t.Fatalf("queued flood error = %v", result.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("queued normal request was not rejected")
		}
		hold.Release()
	})
}

func TestDisabledAutoWaitKeepsNormalQueueing(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, func(c *Config) { c.FloodAutoWait = 0 })
		l.PauseBackground(120 * time.Second)
		normal := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		clock.Advance(120 * time.Second)
		receive(t, normal).Release()
	})
}

func TestQueuedCountsWaitingRequestsOfEveryQueue(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		l := limiterForTest(t, newFakeClock(), func(c *Config) { c.Concurrency = 1; c.GeneralBurst = 5 })
		if got := l.Queued(); got != 0 {
			t.Fatalf("an idle limiter queues %d", got)
		}
		hold, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		// A request that holds a permit is not queued.
		if got := l.Queued(); got != 0 {
			t.Fatalf("a held permit counts as queued: %d", got)
		}
		normal := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		background := acquireAsync(l, WithBackground(context.Background()), General, "", nil)
		awaitBackgroundQueue(t, l, 1)
		if got := l.Queued(); got != 2 {
			t.Fatalf("queued = %d, want 2", got)
		}
		hold.Release()
		receive(t, normal).Release()
		receive(t, background).Release()
		synctest.Wait()
		if got := l.Queued(); got != 0 {
			t.Fatalf("queued after the drain = %d, want 0", got)
		}
	})
}

func TestPausedFor(t *testing.T) {
	clock := newFakeClock()
	l := limiterForTest(t, clock, nil)
	if got := l.PausedFor(); got != 0 {
		t.Fatalf("initial pause = %s", got)
	}
	l.PauseBackground(120 * time.Second)
	clock.Advance(30 * time.Second)
	if got := l.PausedFor(); got != 90*time.Second {
		t.Fatalf("remaining pause = %s", got)
	}
	clock.Advance(90 * time.Second)
	if got := l.PausedFor(); got != 0 {
		t.Fatalf("expired pause = %s", got)
	}
}

func TestLiveRateChangeKeepsElapsedTokensAndWakesQueue(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, func(c *Config) { c.GeneralPerSecond = 1; c.GeneralBurst = 1; c.Percent = 50 })
		p, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
		result := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		clock.Advance(time.Second)
		if err := l.SetPercent(100); err != nil {
			t.Fatal(err)
		}
		awaitQueue(t, l, 1)
		clock.Advance(time.Millisecond * 499)
		awaitQueue(t, l, 1)
		clock.Advance(time.Millisecond)
		receive(t, result).Release()
		if err := l.SetPercent(0); err == nil {
			t.Fatal("invalid rate accepted")
		}
		if err := l.SetFloodAutoWait(3 * time.Second); err != nil || l.FloodAutoWait() != 3*time.Second {
			t.Fatalf("flood wait: %v", err)
		}
		if err := l.SetFloodAutoWait(-time.Second); err == nil || l.FloodAutoWait() != 3*time.Second {
			t.Fatal("invalid flood wait accepted")
		}
	})
}

func TestLiveRateChangeKeepsFileBucketElapsedTokens(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, func(c *Config) { c.Percent = 10 })
		for range fileBurst {
			p, err := l.Acquire(context.Background(), File, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			p.Release()
		}
		queued := acquireAsync(l, context.Background(), File, "", nil)
		awaitFileQueue(t, l, 1)
		clock.Advance(100 * time.Millisecond)
		if err := l.SetPercent(100); err != nil {
			t.Fatal(err)
		}
		awaitFileQueue(t, l, 1)
		clock.Advance(20 * time.Millisecond)
		awaitFileQueue(t, l, 1)
		clock.Advance(4 * time.Millisecond)
		receive(t, queued).Release()
	})
}

func TestLiveFloodWaitChangeWakesQueuedRequests(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, nil)
		l.PauseBackground(10 * time.Second)
		result := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		if err := l.SetFloodAutoWait(3 * time.Second); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case r := <-result:
			var flood *FloodWaitError
			if !errors.As(r.err, &flood) || flood.RetryAfterSeconds != 10 {
				t.Fatalf("result = %+v", r)
			}
		default:
			t.Fatal("queued request was not woken")
		}
	})
}
