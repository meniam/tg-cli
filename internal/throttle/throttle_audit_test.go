package throttle

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
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

// audit10ArmedContext reports no error until armed. Once armed it reports
// context.Canceled to every caller except the limiter's scheduler goroutine, and
// its Done channel never closes. Acquire can therefore see the cancellation only
// through its own Err call after the scheduler has already granted the request,
// which is the refund path of Acquire. The scheduler is recognised by its stack.
type audit10ArmedContext struct {
	context.Context
	armed atomic.Bool
}

func (c *audit10ArmedContext) Err() error {
	if !c.armed.Load() || audit10CalledFromScheduler() {
		return nil
	}
	return context.Canceled
}

func (c *audit10ArmedContext) Done() <-chan struct{} { return nil }

func audit10CalledFromScheduler() bool {
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "(*Limiter).run") {
			return true
		}
		if !more {
			return false
		}
	}
}

// TestAudit10_CanceledAfterGrantSpendsNoToken grants a queued request and only
// then lets it see its cancellation. The slot must be freed and the token
// returned.
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
		ctx := &audit10ArmedContext{Context: context.Background()}
		queued := acquireAsync(l, ctx, General, "", nil)
		awaitQueue(t, l, 1)
		ctx.armed.Store(true)
		clock.Advance(time.Second)
		synctest.Wait()
		if r := <-queued; !errors.Is(r.err, context.Canceled) {
			t.Fatalf("queued request: permit=%v err=%v", r.permit, r.err)
		}
		l.mu.Lock()
		active, tokens := l.active, l.general.tokens
		l.mu.Unlock()
		if active != 0 || tokens < 1 {
			t.Fatalf("active = %d, tokens = %v; the canceled request kept its slot or token", active, tokens)
		}
	})
}
