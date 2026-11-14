package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const IdempotencyRetention = 7 * 24 * time.Hour

// IdempotencyRecord holds a pending or completed request. A nil Result is pending.
type IdempotencyRecord struct {
	ChatID      int64
	Key, Method string
	ParamsHash  []byte
	Result      json.RawMessage
	CreatedAt   time.Time
}

// ReserveIdempotency inserts a pending record or returns the existing record.
func (db *DB) ReserveIdempotency(ctx context.Context, record IdempotencyRecord) (IdempotencyRecord, bool, error) {
	if record.Key == "" || record.Method == "" || len(record.ParamsHash) == 0 {
		return IdempotencyRecord{}, false, errors.New("idempotency key, method and parameter hash are required")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = db.now()
	}
	result, err := db.write.ExecContext(ctx, `INSERT INTO idempotency(chat_id,key,method,params_hash,result,created_at) VALUES(?,?,?,?,NULL,?) ON CONFLICT(chat_id,key) DO NOTHING`, record.ChatID, record.Key, record.Method, record.ParamsHash, record.CreatedAt.Unix())
	if err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("reserve idempotency: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if n == 1 {
		record.Result = nil
		record.CreatedAt = time.Unix(record.CreatedAt.Unix(), 0).UTC()
		return record, true, nil
	}
	existing, found, err := db.GetIdempotency(ctx, record.ChatID, record.Key)
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if !found {
		return IdempotencyRecord{}, false, errors.New("idempotency record disappeared")
	}
	return existing, false, nil
}

func (db *DB) GetIdempotency(ctx context.Context, chatID int64, key string) (IdempotencyRecord, bool, error) {
	var r IdempotencyRecord
	var created int64
	var result []byte
	err := db.read.QueryRowContext(ctx, `SELECT chat_id,key,method,params_hash,result,created_at FROM idempotency WHERE chat_id=? AND key=?`, chatID, key).Scan(&r.ChatID, &r.Key, &r.Method, &r.ParamsHash, &result, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, fmt.Errorf("read idempotency: %w", err)
	}
	r.Result = result
	r.CreatedAt = time.Unix(created, 0).UTC()
	return r, true, nil
}

// CompleteIdempotency only completes the matching pending reservation.
func (db *DB) CompleteIdempotency(ctx context.Context, chatID int64, key, method string, hash []byte, result json.RawMessage) (bool, error) {
	if !json.Valid(result) {
		return false, errors.New("idempotency result must be valid JSON")
	}
	res, err := db.write.ExecContext(ctx, `UPDATE idempotency SET result=? WHERE chat_id=? AND key=? AND method=? AND params_hash=? AND result IS NULL`, []byte(result), chatID, key, method, hash)
	if err != nil {
		return false, fmt.Errorf("complete idempotency: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (db *DB) PruneIdempotency(ctx context.Context, now time.Time) (int64, error) {
	res, err := db.write.ExecContext(ctx, `DELETE FROM idempotency WHERE created_at < ?`, now.Add(-IdempotencyRetention).Unix())
	if err != nil {
		return 0, fmt.Errorf("prune idempotency: %w", err)
	}
	return res.RowsAffected()
}
