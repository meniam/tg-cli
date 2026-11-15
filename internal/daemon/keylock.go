package daemon

import (
	"context"
	"sync"
)

type idempotencyKey struct {
	chatID int64
	key    string
}

type keyLockEntry struct {
	token chan struct{}
	refs  int
}

type keyLocks struct {
	mu      sync.Mutex
	entries map[idempotencyKey]*keyLockEntry
}

func (l *keyLocks) lock(ctx context.Context, chatID int64, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k := idempotencyKey{chatID: chatID, key: key}
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[idempotencyKey]*keyLockEntry)
	}
	e := l.entries[k]
	if e == nil {
		e = &keyLockEntry{token: make(chan struct{}, 1)}
		e.token <- struct{}{}
		l.entries[k] = e
	}
	e.refs++
	l.mu.Unlock()
	releaseRef := func() {
		l.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(l.entries, k)
		}
		l.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	case <-e.token:
		if err := ctx.Err(); err != nil {
			e.token <- struct{}{}
			releaseRef()
			return nil, err
		}
		return func() {
			e.token <- struct{}{}
			releaseRef()
		}, nil
	}
}
