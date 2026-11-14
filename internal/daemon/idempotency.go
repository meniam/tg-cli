package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

var validKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type idempotencySession struct {
	d           *daemon
	chatID      int64
	key, method string
	hash        []byte
}

func (d *daemon) beginIdempotency(ctx context.Context, chatID int64, key, method string, params any, dry bool) (*idempotencySession, json.RawMessage, bool, error) {
	if key == "" {
		return nil, nil, false, nil
	}
	if !validKey.MatchString(key) {
		return nil, nil, false, rpc.NewError(rpc.CodeInvalidParams, "invalid idempotency key")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, nil, false, err
	}
	sum := sha256.Sum256(body)
	d.idempotencyMu.Lock()
	session := &idempotencySession{d: d, chatID: chatID, key: key, method: method, hash: sum[:]}
	var record store.IdempotencyRecord
	var found bool
	if dry {
		record, found, err = d.store.GetIdempotency(ctx, chatID, key)
	} else {
		record, _, err = d.store.ReserveIdempotency(ctx, store.IdempotencyRecord{ChatID: chatID, Key: key, Method: method, ParamsHash: sum[:]})
		found = err == nil
	}
	if err != nil {
		d.idempotencyMu.Unlock()
		return nil, nil, false, err
	}
	if found && (record.Method != method || !bytes.Equal(record.ParamsHash, sum[:])) {
		d.idempotencyMu.Unlock()
		return nil, nil, false, rpc.NewError(rpc.CodeInvalidParams, "key was used with different parameters")
	}
	if dry {
		d.idempotencyMu.Unlock()
		return nil, nil, found && record.Result != nil, nil
	}
	if record.Result != nil {
		d.idempotencyMu.Unlock()
		return nil, record.Result, true, nil
	}
	return session, nil, false, nil
}
func (s *idempotencySession) unlock() {
	if s != nil {
		s.d.idempotencyMu.Unlock()
	}
}
func (s *idempotencySession) complete(ctx context.Context, result any) error {
	if s == nil {
		return nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	ok, err := s.d.store.CompleteIdempotency(saveCtx, s.chatID, s.key, s.method, s.hash, body)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("idempotency reservation was not completed")
	}
	return nil
}
func (d *daemon) pruneIdempotency(ctx context.Context) {
	prune := func() {
		if _, err := d.store.PruneIdempotency(ctx, time.Now()); err != nil && ctx.Err() == nil {
			d.log.Warn("idempotency cleanup failed")
		}
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
