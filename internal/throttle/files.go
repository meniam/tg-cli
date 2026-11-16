package throttle

import (
	"context"
	"time"
)

const (
	fileConcurrency = 8
	fileBurst       = 8
)

func (l *Limiter) fileDelay(now time.Time) time.Duration {
	rate := BaseGeneralPerSecond * l.cfg.Percent / 100
	return needed(refill(&l.fileBucket, now, rate, fileBurst), rate)
}

func (l *Limiter) fileConsume(now time.Time) {
	rate := BaseGeneralPerSecond * l.cfg.Percent / 100
	l.fileBucket.tokens = refill(&l.fileBucket, now, rate, fileBurst) - 1
	l.fileBucket.last = now
}

func (l *Limiter) fileRefund(now time.Time) {
	rate := BaseGeneralPerSecond * l.cfg.Percent / 100
	l.fileBucket.tokens = min(fileBurst, refill(&l.fileBucket, now, rate, fileBurst)+1)
	l.fileBucket.last = now
}

func (l *Limiter) fileMayRun() bool {
	return len(l.queue) == 0 || l.active >= l.cfg.Concurrency
}

func (l *Limiter) acquireFile(ctx context.Context, waited *Wait) (*Permit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	now := l.clock.Now()
	if len(l.fileQueue) == 0 && l.fileMayRun() && l.fileActive < fileConcurrency && !now.Before(l.pausedUntil) && l.fileDelay(now) == 0 {
		l.fileConsume(now)
		l.fileActive++
		l.mu.Unlock()
		return &Permit{limiter: l, file: true}, nil
	}
	w := &waiter{ctx: ctx, class: File, ready: make(chan struct{})}
	l.fileQueue = append(l.fileQueue, w)
	if !l.fileRunning {
		l.fileRunning = true
		go l.runFiles()
	}
	l.signal()
	l.mu.Unlock()
	select {
	case <-w.ready:
		waited.add(l.clock.Now().Sub(now))
		if err := ctx.Err(); err != nil {
			l.mu.Lock()
			l.fileActive--
			l.fileRefund(l.clock.Now())
			l.signal()
			l.mu.Unlock()
			return nil, err
		}
		return &Permit{limiter: l, file: true}, nil
	case <-ctx.Done():
		l.mu.Lock()
		if w.granted {
			l.fileActive--
			l.fileRefund(l.clock.Now())
		} else {
			for i, queued := range l.fileQueue {
				if queued == w {
					l.fileQueue = append(l.fileQueue[:i], l.fileQueue[i+1:]...)
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

func (l *Limiter) runFiles() {
	for {
		l.mu.Lock()
		if len(l.fileQueue) == 0 {
			l.fileRunning = false
			l.mu.Unlock()
			return
		}
		w := l.fileQueue[0]
		if w.ctx.Err() != nil {
			l.fileQueue = l.fileQueue[1:]
			l.mu.Unlock()
			continue
		}
		now := l.clock.Now()
		delay := l.fileDelay(now)
		if pause := l.pausedUntil.Sub(now); pause > delay {
			delay = pause
		}
		if delay == 0 && l.fileActive < fileConcurrency && l.fileMayRun() {
			l.fileConsume(now)
			l.fileActive++
			l.fileQueue = l.fileQueue[1:]
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
			case <-l.fileWake:
			}
			timer.Stop()
		} else {
			<-l.fileWake
		}
	}
}
