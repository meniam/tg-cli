package throttle

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestAudit10_NewRejectsInvalidConfig(t *testing.T) {
	cases := map[string]func(*Config){
		"percent below 1":          func(c *Config) { c.Percent = 0.5 },
		"percent above 100":        func(c *Config) { c.Percent = 101 },
		"percent NaN":              func(c *Config) { c.Percent = math.NaN() },
		"percent Inf":              func(c *Config) { c.Percent = math.Inf(1) },
		"concurrency zero":         func(c *Config) { c.Concurrency = 0 },
		"concurrency above limit":  func(c *Config) { c.Concurrency = DefaultConcurrency + 1 },
		"general burst zero":       func(c *Config) { c.GeneralBurst = 0 },
		"private burst zero":       func(c *Config) { c.PrivateBurst = 0 },
		"group burst zero":         func(c *Config) { c.GroupBurst = 0 },
		"negative flood auto wait": func(c *Config) { c.FloodAutoWait = -time.Second },
		"general rate zero":        func(c *Config) { c.GeneralPerSecond = 0 },
		"private rate negative":    func(c *Config) { c.PrivatePerSecond = -1 },
		"group rate NaN":           func(c *Config) { c.GroupPerMinute = math.NaN() },
		"general rate infinite":    func(c *Config) { c.GeneralPerSecond = math.Inf(1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			mutate(&cfg)
			if l, err := New(cfg, newFakeClock()); err == nil || l != nil {
				t.Fatalf("New() = %v, %v, want an error", l, err)
			}
		})
	}
	if _, err := New(DefaultConfig(), nil); err != nil {
		t.Fatalf("default config with the system clock: %v", err)
	}
}

func TestAudit10_AcquireRejectsInvalidRequests(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name  string
		ctx   context.Context
		class Class
		peer  string
		want  error
	}{
		{"nil context", nil, General, "", nil},
		{"unknown class", context.Background(), GroupSend + 2, "x", nil},
		{"private without peer", context.Background(), PrivateSend, "", nil},
		{"group without peer", context.Background(), GroupSend, "", nil},
		{"canceled context", canceled, General, "", context.Canceled},
		{"canceled file context", canceled, File, "", context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := limiterForTest(t, newFakeClock(), nil)
			//nolint:staticcheck // a nil context is the case under test.
			permit, err := l.Acquire(tc.ctx, tc.class, tc.peer, nil)
			if err == nil || permit != nil {
				t.Fatalf("Acquire() = %v, %v, want an error", permit, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Acquire() error = %v, want %v", err, tc.want)
			}
			l.mu.Lock()
			active, queued := l.active, len(l.queue)
			l.mu.Unlock()
			if active != 0 || queued != 0 {
				t.Fatalf("rejected request left active=%d queue=%d", active, queued)
			}
		})
	}
}

func TestAudit10_ReleaseIsIdempotentAndNilSafe(t *testing.T) {
	l := limiterForTest(t, newFakeClock(), nil)
	var nilPermit *Permit
	nilPermit.Release()
	general, err := l.Acquire(context.Background(), General, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	file, err := l.Acquire(context.Background(), File, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		general.Release()
		file.Release()
	}
	l.mu.Lock()
	active, fileActive := l.active, l.fileActive
	l.mu.Unlock()
	if active != 0 || fileActive != 0 {
		t.Fatalf("active = %d, fileActive = %d, want 0 after repeated Release", active, fileActive)
	}
}

func TestAudit10_PauseBackgroundBoundaryAndRounding(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, nil)
		l.Pause(0)
		l.Pause(-time.Second)
		if got := l.PausedFor(); got != 0 {
			t.Fatalf("non-positive pause changed the gate: %s", got)
		}

		// A pause equal to FloodAutoWait is waited out, not reported as an error.
		l.PauseBackground(DefaultFloodAutoWait)
		normal := acquireAsync(l, context.Background(), General, "", nil)
		awaitQueue(t, l, 1)
		clock.Advance(DefaultFloodAutoWait)
		receive(t, normal).Release()

		// A longer pause is reported with the seconds rounded up.
		limited := limiterForTest(t, clock, nil)
		limited.PauseBackground(10500 * time.Millisecond)
		_, err := limited.Acquire(context.Background(), General, "", nil)
		var flood *FloodWaitError
		if !errors.As(err, &flood) || flood.RetryAfterSeconds != 11 {
			t.Fatalf("flood error = %v, want RetryAfterSeconds 11", err)
		}
	})
}

// TestAudit10_CanceledAfterGrantSpendsNoToken cancels a queued request in the
// same instant the clock releases it. Whichever side wins, the slot is freed
// and the token is returned, so the next request needs no wait.
func TestAudit10_CanceledAfterGrantSpendsNoToken(t *testing.T) {
	audit10Test(t, func(t *testing.T) {
		clock := newFakeClock()
		l := limiterForTest(t, clock, func(c *Config) {
			c.GeneralBurst = 1
			c.GeneralPerSecond = 1
			c.Percent = 100
		})
		first, err := l.Acquire(context.Background(), General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		first.Release()
		ctx, cancel := context.WithCancel(context.Background())
		queued := acquireAsync(l, ctx, General, "", nil)
		awaitQueue(t, l, 1)
		cancel()
		clock.Advance(time.Second)
		if r := <-queued; !errors.Is(r.err, context.Canceled) {
			t.Fatalf("queued request: permit=%v err=%v", r.permit, r.err)
		}
		waited := new(Wait)
		next, err := l.Acquire(context.Background(), General, "", waited)
		if err != nil || waited.Duration() != 0 {
			t.Fatalf("next request waited %s, err %v; the canceled one kept its token", waited.Duration(), err)
		}
		next.Release()
		l.mu.Lock()
		active := l.active
		l.mu.Unlock()
		if active != 0 {
			t.Fatalf("active = %d, want 0", active)
		}
	})
}
