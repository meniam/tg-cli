package daemon

import (
	"math"
	"sync"
	"time"

	"tg-cli/internal/rpc"
)

type phoneLimiter struct {
	mu    sync.Mutex
	calls []time.Time
}

func (l *phoneLimiter) take(now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-24 * time.Hour)
	first := 0
	for first < len(l.calls) && !l.calls[first].After(cutoff) {
		first++
	}
	l.calls = l.calls[first:]
	var next time.Time
	if len(l.calls) >= 20 {
		next = l.calls[0].Add(24 * time.Hour)
	}
	if len(l.calls) > 0 {
		spacing := l.calls[len(l.calls)-1].Add(3 * time.Second)
		if spacing.After(next) {
			next = spacing
		}
	}
	if now.Before(next) {
		return &rpc.Error{Code: rpc.CodeFloodWait, Message: "phone lookup limit reached", Data: map[string]any{"retry_after_seconds": int(math.Ceil(next.Sub(now).Seconds()))}}
	}
	l.calls = append(l.calls, now)
	return nil
}

// completed conservatively measures the next interval from request completion.
func (l *phoneLimiter) completed(now time.Time) {
	l.mu.Lock()
	if len(l.calls) > 0 && now.After(l.calls[len(l.calls)-1]) {
		l.calls[len(l.calls)-1] = now
	}
	l.mu.Unlock()
}
