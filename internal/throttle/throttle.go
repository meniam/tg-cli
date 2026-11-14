// Package throttle limits requests made by one Telegram account.
package throttle

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	BaseGeneralPerSecond = 30
	BasePrivatePerSecond = 1
	BaseGroupPerMinute   = 20
	DefaultPercent       = 60
	DefaultGeneralBurst  = 5
	DefaultPrivateBurst  = 1
	DefaultGroupBurst    = 1
	DefaultConcurrency   = 2
	DefaultFloodAutoWait = 10 * time.Second
)

// Class identifies the optional peer-specific bucket used by a request.
type Class uint8

const (
	General Class = iota
	PrivateSend
	GroupSend
)

// Config contains account limits. Use DefaultConfig and override needed fields.
type Config struct {
	GeneralPerSecond float64
	PrivatePerSecond float64
	GroupPerMinute   float64
	Percent          float64
	GeneralBurst     int
	PrivateBurst     int
	GroupBurst       int
	Concurrency      int
	FloodAutoWait    time.Duration
}

// DefaultConfig returns the agreed limits for one account.
func DefaultConfig() Config {
	return Config{
		GeneralPerSecond: BaseGeneralPerSecond,
		PrivatePerSecond: BasePrivatePerSecond,
		GroupPerMinute:   BaseGroupPerMinute,
		Percent:          DefaultPercent,
		GeneralBurst:     DefaultGeneralBurst,
		PrivateBurst:     DefaultPrivateBurst,
		GroupBurst:       DefaultGroupBurst,
		Concurrency:      DefaultConcurrency,
		FloodAutoWait:    DefaultFloodAutoWait,
	}
}

// Timer is the part of a clock timer needed by the scheduler.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock supplies time and timers. A fake clock can advance blocked requests.
type Clock interface {
	Now() time.Time
	NewTimer(time.Time) Timer
}

type systemClock struct{}
type systemTimer struct{ *time.Timer }

func (systemClock) Now() time.Time { return time.Now() }
func (systemClock) NewTimer(deadline time.Time) Timer {
	return systemTimer{time.NewTimer(time.Until(deadline))}
}
func (t systemTimer) C() <-chan time.Time { return t.Timer.C }

// Wait accumulates admission delay across attempts of one logical request.
type Wait struct{ nanoseconds atomic.Int64 }

// Duration returns time spent waiting for admission, including flood pauses.
func (w *Wait) Duration() time.Duration {
	if w == nil {
		return 0
	}
	return time.Duration(w.nanoseconds.Load())
}

func (w *Wait) add(d time.Duration) {
	if w != nil && d > 0 {
		w.nanoseconds.Add(int64(d))
	}
}

type bucket struct {
	tokens float64
	last   time.Time
}

type waiter struct {
	ctx     context.Context
	class   Class
	peer    string
	ready   chan struct{}
	granted bool
	err     error
}

// FloodWaitError reports an account pause caused by background history sync.
type FloodWaitError struct{ RetryAfterSeconds int64 }

func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("background flood wait: retry after %d seconds", e.RetryAfterSeconds)
}

// Limiter owns the priority queues, token buckets, flood gate and concurrency limit.
type Limiter struct {
	mu                 sync.Mutex
	clock              Clock
	cfg                Config
	general            bucket
	private            map[string]*bucket
	group              map[string]*bucket
	queue              []*waiter
	backgroundQueue    []*waiter
	active             int
	pausedUntil        time.Time
	pausedByBackground bool
	wake               chan struct{}
	running            bool
}

// New constructs a limiter. A nil clock uses the system clock.
func New(cfg Config, clock Clock) (*Limiter, error) {
	if cfg.Percent < 1 || cfg.Percent > 100 || math.IsNaN(cfg.Percent) || math.IsInf(cfg.Percent, 0) ||
		cfg.Concurrency < 1 || cfg.Concurrency > DefaultConcurrency ||
		cfg.GeneralBurst < 1 || cfg.PrivateBurst < 1 || cfg.GroupBurst < 1 || cfg.FloodAutoWait < 0 ||
		!validRate(cfg.GeneralPerSecond) || !validRate(cfg.PrivatePerSecond) || !validRate(cfg.GroupPerMinute) {
		return nil, errors.New("invalid throttle configuration")
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Limiter{
		clock: clock, cfg: cfg,
		general: bucket{tokens: float64(cfg.GeneralBurst), last: clock.Now()},
		private: make(map[string]*bucket), group: make(map[string]*bucket),
		wake: make(chan struct{}, 1),
	}, nil
}

func validRate(v float64) bool { return v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) }

// FloodAutoWait is the configured maximum automatic flood wait.
func (l *Limiter) FloodAutoWait() time.Duration { return l.cfg.FloodAutoWait }

// PausedFor returns the remaining account-wide flood pause.
func (l *Limiter) PausedFor() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(0, l.pausedUntil.Sub(l.clock.Now()))
}

// Permit holds one concurrency slot. Release must be called after the attempt.
type Permit struct {
	limiter *Limiter
	once    sync.Once
}

// Release frees the concurrency slot. It is safe to call more than once.
func (p *Permit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		l := p.limiter
		l.mu.Lock()
		l.active--
		l.signal()
		l.mu.Unlock()
	})
}

// Acquire waits for the account gate, tokens and a concurrency slot. Normal
// requests precede background requests; each queue remains FIFO. A canceled
// queued request spends no token.
func (l *Limiter) Acquire(ctx context.Context, class Class, peer string, waited *Wait) (*Permit, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if class > GroupSend || (class != General && peer == "") {
		return nil, errors.New("invalid throttle request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	now := l.clock.Now()
	background := BackgroundFromContext(ctx)
	if !background {
		if err := l.backgroundFloodError(now); err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	if len(l.queue) == 0 && (!background || len(l.backgroundQueue) == 0) && l.active < l.cfg.Concurrency && !now.Before(l.pausedUntil) && l.delay(now, class, peer) == 0 {
		l.consume(now, class, peer)
		l.active++
		l.mu.Unlock()
		return &Permit{limiter: l}, nil
	}
	w := &waiter{ctx: ctx, class: class, peer: peer, ready: make(chan struct{})}
	if background {
		l.backgroundQueue = append(l.backgroundQueue, w)
	} else {
		l.queue = append(l.queue, w)
	}
	if !l.running {
		l.running = true
		go l.run()
	}
	l.signal()
	l.mu.Unlock()
	select {
	case <-w.ready:
		waited.add(l.clock.Now().Sub(now))
		if err := ctx.Err(); err != nil {
			if w.granted {
				l.mu.Lock()
				l.active--
				l.refund(l.clock.Now(), class, peer)
				l.signal()
				l.mu.Unlock()
			}
			return nil, err
		}
		if w.err != nil {
			return nil, w.err
		}
		return &Permit{limiter: l}, nil
	case <-ctx.Done():
		l.mu.Lock()
		if w.granted {
			l.active--
			l.refund(l.clock.Now(), class, peer)
		} else {
			queue := &l.queue
			if background {
				queue = &l.backgroundQueue
			}
			for i, queued := range *queue {
				if queued == w {
					*queue = append((*queue)[:i], (*queue)[i+1:]...)
					break
				}
			}
		}
		l.signal()
		l.mu.Unlock()
		waited.add(l.clock.Now().Sub(now))
		return nil, ctx.Err()
	}
}

func (l *Limiter) backgroundFloodError(now time.Time) error {
	if l.cfg.FloodAutoWait == 0 || !l.pausedByBackground || !now.Before(l.pausedUntil) {
		return nil
	}
	remaining := l.pausedUntil.Sub(now)
	if remaining <= l.cfg.FloodAutoWait {
		return nil
	}
	seconds := int64(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	return &FloodWaitError{RetryAfterSeconds: seconds}
}

// Pause extends the account-wide gate by d from now; a shorter pause never
// moves an existing deadline backwards. In-flight attempts are not stopped.
func (l *Limiter) Pause(d time.Duration) {
	l.pause(d, false)
}

// PauseBackground marks a flood gate created by low-priority history sync.
func (l *Limiter) PauseBackground(d time.Duration) {
	l.pause(d, true)
}

func (l *Limiter) pause(d time.Duration, background bool) {
	if d <= 0 {
		return
	}
	l.mu.Lock()
	until := l.clock.Now().Add(d)
	if until.After(l.pausedUntil) {
		l.pausedUntil = until
		l.pausedByBackground = background
	}
	l.signal()
	l.mu.Unlock()
}

func (l *Limiter) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// run exists only while requests are queued, so an idle limiter owns no goroutine.
func (l *Limiter) run() {
	for {
		l.mu.Lock()
		if len(l.queue) == 0 && len(l.backgroundQueue) == 0 {
			l.running = false
			l.mu.Unlock()
			return
		}
		background := len(l.queue) == 0
		queue := &l.queue
		if background {
			queue = &l.backgroundQueue
		}
		w := (*queue)[0]
		if w.ctx.Err() != nil {
			*queue = (*queue)[1:]
			l.mu.Unlock()
			continue
		}
		now := l.clock.Now()
		if !background {
			if err := l.backgroundFloodError(now); err != nil {
				*queue = (*queue)[1:]
				w.err = err
				close(w.ready)
				l.mu.Unlock()
				continue
			}
		}
		var delay time.Duration
		if now.Before(l.pausedUntil) {
			delay = l.pausedUntil.Sub(now)
		}
		if tokenDelay := l.delay(now, w.class, w.peer); tokenDelay > delay {
			delay = tokenDelay
		}
		if delay == 0 && l.active < l.cfg.Concurrency {
			l.consume(now, w.class, w.peer)
			l.active++
			*queue = (*queue)[1:]
			w.granted = true
			close(w.ready)
			l.mu.Unlock()
			continue
		}
		l.mu.Unlock()
		if delay > 0 {
			timer := l.clock.NewTimer(now.Add(delay))
			select {
			case <-timer.C():
			case <-l.wake:
			}
			timer.Stop()
		} else {
			<-l.wake
		}
	}
}

func (l *Limiter) rates() (float64, float64, float64) {
	p := l.cfg.Percent / 100
	return l.cfg.GeneralPerSecond * p, l.cfg.PrivatePerSecond * p, l.cfg.GroupPerMinute * p / 60
}

func refill(b *bucket, now time.Time, rate float64, burst int) float64 {
	return min(float64(burst), b.tokens+max(0, now.Sub(b.last).Seconds())*rate)
}

func needed(tokens, rate float64) time.Duration {
	if tokens >= 1 {
		return 0
	}
	return time.Duration(math.Ceil((1 - tokens) / rate * float64(time.Second)))
}

func (l *Limiter) peerBucket(class Class, peer string) (*bucket, float64, int) {
	_, privateRate, groupRate := l.rates()
	var buckets map[string]*bucket
	rate, burst := privateRate, l.cfg.PrivateBurst
	if class == GroupSend {
		buckets, rate, burst = l.group, groupRate, l.cfg.GroupBurst
	} else {
		buckets = l.private
	}
	b := buckets[peer]
	if b == nil {
		b = &bucket{tokens: float64(burst), last: l.clock.Now()}
		buckets[peer] = b
	}
	return b, rate, burst
}

func (l *Limiter) delay(now time.Time, class Class, peer string) time.Duration {
	generalRate, _, _ := l.rates()
	d := needed(refill(&l.general, now, generalRate, l.cfg.GeneralBurst), generalRate)
	if class != General {
		b, rate, burst := l.peerBucket(class, peer)
		if peerDelay := needed(refill(b, now, rate, burst), rate); peerDelay > d {
			d = peerDelay
		}
	}
	return d
}

func (l *Limiter) consume(now time.Time, class Class, peer string) {
	generalRate, _, _ := l.rates()
	l.general.tokens = refill(&l.general, now, generalRate, l.cfg.GeneralBurst) - 1
	l.general.last = now
	if class != General {
		b, rate, burst := l.peerBucket(class, peer)
		b.tokens = refill(b, now, rate, burst) - 1
		b.last = now
	}
}

func (l *Limiter) refund(now time.Time, class Class, peer string) {
	generalRate, _, _ := l.rates()
	l.general.tokens = min(float64(l.cfg.GeneralBurst), refill(&l.general, now, generalRate, l.cfg.GeneralBurst)+1)
	l.general.last = now
	if class != General {
		b, rate, burst := l.peerBucket(class, peer)
		b.tokens = min(float64(burst), refill(b, now, rate, burst)+1)
		b.last = now
	}
}
