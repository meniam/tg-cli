package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"tg-cli/internal/rpc"
)

func saveSnapshot(ctx context.Context, tx *sql.Tx, kind string, now int64, ttl *int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO owner_snapshots(kind,synced_at,authorization_ttl_days) VALUES(?,?,?) ON CONFLICT(kind) DO UPDATE SET synced_at=excluded.synced_at,authorization_ttl_days=excluded.authorization_ttl_days`, kind, now, ttl)
	return err
}
func (db *DB) snapshot(ctx context.Context, kind string) (*int64, *int, error) {
	var at int64
	var ttl sql.NullInt64
	err := db.read.QueryRowContext(ctx, `SELECT synced_at,authorization_ttl_days FROM owner_snapshots WHERE kind=?`, kind).Scan(&at, &ttl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if ttl.Valid {
		v := int(ttl.Int64)
		return &at, &v, nil
	}
	return &at, nil, nil
}
func marshal(v any) (string, error) { b, e := json.Marshal(v); return string(b), e }

func ownerRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func (db *DB) UpsertDialogStates(ctx context.Context, states []rpc.ChatState, filter ChatFilter) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := db.now().Unix()
	for _, s := range states {
		if !allowedID(filter, s.ChatID) {
			continue
		}
		s.SyncedAt = &now
		raw, e := marshal(s)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO dialog_state(chat_id,data,synced_at,raw) VALUES(?,?,?,?) ON CONFLICT(chat_id) DO UPDATE SET data=excluded.data,synced_at=excluded.synced_at,raw=excluded.raw`, s.ChatID, raw, now, ownerRaw(s.Raw)); err != nil {
			return err
		}
	}
	if err = saveSnapshot(ctx, tx, "dialog_state", now, nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) DialogState(ctx context.Context, id int64, filter ChatFilter) (rpc.ChatState, error) {
	var s rpc.ChatState
	if !allowedID(filter, id) {
		return s, ErrNotFound
	}
	var raw string
	err := db.read.QueryRowContext(ctx, `SELECT data FROM dialog_state WHERE chat_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(raw), &s)
	return s, err
}
func (db *DB) DialogStates(ctx context.Context, limit int, filter ChatFilter) (rpc.ChatStatesResult, error) {
	out := rpc.ChatStatesResult{States: []rpc.ChatState{}}
	var err error
	out.SyncedAt, _, err = db.snapshot(ctx, "dialog_state")
	if err != nil {
		return out, err
	}
	cond, args := filter.condition("chat_id", false)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, `SELECT data FROM dialog_state WHERE 1=1`+cond+` ORDER BY synced_at DESC,chat_id LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var s rpc.ChatState
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &s); err != nil {
			return out, err
		}
		out.States = append(out.States, s)
	}
	return out, rows.Err()
}
func (db *DB) ReplaceSessions(ctx context.Context, result rpc.SessionsResult) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_sessions`); err != nil {
		return err
	}
	now := db.now().Unix()
	for _, s := range result.Sessions {
		raw, e := marshal(s)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO account_sessions(hash,data,authorization_ttl_days,synced_at,raw) VALUES(?,?,?,?,?)`, s.Hash, raw, result.AuthorizationTTLDays, now, ownerRaw(s.Raw)); err != nil {
			return err
		}
	}
	if err = saveSnapshot(ctx, tx, "sessions", now, &result.AuthorizationTTLDays); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) Sessions(ctx context.Context) (rpc.SessionsResult, error) {
	out := rpc.SessionsResult{Sessions: []rpc.Session{}}
	var ttl *int
	var err error
	out.SyncedAt, ttl, err = db.snapshot(ctx, "sessions")
	if err != nil {
		return out, err
	}
	if ttl != nil {
		out.AuthorizationTTLDays = *ttl
	}
	rows, err := db.read.QueryContext(ctx, `SELECT hash,data FROM account_sessions ORDER BY rowid`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var s rpc.Session
		var hash int64
		if err = rows.Scan(&hash, &raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &s); err != nil {
			return out, err
		}
		s.Hash = hash
		out.Sessions = append(out.Sessions, s)
	}
	return out, rows.Err()
}
func (db *DB) saveBlocked(ctx context.Context, peers []rpc.BlockedPeer, filter ChatFilter, replace bool) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replace {
		if _, err = tx.ExecContext(ctx, `DELETE FROM blocked_peers`); err != nil {
			return err
		}
	}
	now := db.now().Unix()
	for _, p := range peers {
		if !allowedID(filter, p.PeerID) {
			continue
		}
		raw, e := marshal(p)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO blocked_peers(peer_id,data,synced_at,raw) VALUES(?,?,?,?) ON CONFLICT(peer_id) DO UPDATE SET data=excluded.data,synced_at=excluded.synced_at,raw=excluded.raw`, p.PeerID, raw, now, ownerRaw(p.Raw)); err != nil {
			return err
		}
	}
	if err = saveSnapshot(ctx, tx, "blocked", now, nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) ReplaceBlocked(ctx context.Context, peers []rpc.BlockedPeer, filter ChatFilter) error {
	return db.saveBlocked(ctx, peers, filter, true)
}
func (db *DB) UpsertBlocked(ctx context.Context, peers []rpc.BlockedPeer, filter ChatFilter) error {
	return db.saveBlocked(ctx, peers, filter, false)
}
func (db *DB) Blocked(ctx context.Context, limit int, filter ChatFilter) (rpc.BlockedResult, error) {
	out := rpc.BlockedResult{Peers: []rpc.BlockedPeer{}}
	var err error
	out.SyncedAt, _, err = db.snapshot(ctx, "blocked")
	if err != nil {
		return out, err
	}
	cond, args := filter.condition("peer_id", false)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, `SELECT data FROM blocked_peers WHERE 1=1`+cond+` ORDER BY peer_id LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var p rpc.BlockedPeer
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return out, err
		}
		out.Peers = append(out.Peers, p)
	}
	return out, rows.Err()
}
func (db *DB) ReplaceNotifyDefaults(ctx context.Context, items []rpc.NotifyDefault) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM notify_defaults`); err != nil {
		return err
	}
	now := db.now().Unix()
	for _, item := range items {
		item.SyncedAt = &now
		raw, e := marshal(item)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO notify_defaults(scope,data,synced_at,raw) VALUES(?,?,?,?)`, item.Scope, raw, now, ownerRaw(item.Raw)); err != nil {
			return err
		}
	}
	if err = saveSnapshot(ctx, tx, "notify", now, nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) NotifyDefaults(ctx context.Context) (rpc.NotifyDefaultsResult, error) {
	out := rpc.NotifyDefaultsResult{Defaults: []rpc.NotifyDefault{}}
	var err error
	out.SyncedAt, _, err = db.snapshot(ctx, "notify")
	if err != nil {
		return out, err
	}
	rows, err := db.read.QueryContext(ctx, `SELECT data FROM notify_defaults ORDER BY scope`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var item rpc.NotifyDefault
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		out.Defaults = append(out.Defaults, item)
	}
	return out, rows.Err()
}
