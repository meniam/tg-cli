// Package ctxlock provides a context-aware lock.
package ctxlock

import (
	"context"
	"sync"
)

// Lock is a zero-value usable mutual exclusion lock.
type Lock struct {
	once  sync.Once
	token chan struct{}
}

// Acquire waits for the lock or cancellation.
func (l *Lock) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.once.Do(func() { l.token = make(chan struct{}, 1); l.token <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.token:
		if err := ctx.Err(); err != nil {
			l.token <- struct{}{}
			return err
		}
		return nil
	}
}

// Release releases a held lock.
func (l *Lock) Release() { l.token <- struct{}{} }
