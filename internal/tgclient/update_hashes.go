package tgclient

import (
	"context"
	"encoding/json"
	"fmt"
)

type accessHashDelegate interface {
	SetChannelAccessHash(context.Context, int64, int64, int64) error
	GetChannelAccessHash(context.Context, int64, int64) (int64, bool, error)
	SetUserAccessHash(context.Context, int64, int64, int64) error
	GetUserAccessHash(context.Context, int64, int64) (int64, bool, error)
}

// persistentHasher keeps the existing peers.Manager hot cache and persists
// hashes needed to restore saved channel pts after a daemon restart.
type persistentHasher struct {
	delegate accessHashDelegate
	kv       StateKV
}

func hashKey(kind string, userID, targetID int64) string {
	return fmt.Sprintf("updates.access.%s.%d.%d", kind, userID, targetID)
}

func (h persistentHasher) get(ctx context.Context, kind string, userID, targetID int64, fallback func() (int64, bool, error)) (int64, bool, error) {
	raw, found, err := h.kv.Get(ctx, hashKey(kind, userID, targetID))
	if err != nil {
		return 0, false, err
	}
	if found {
		var hash int64
		if err := json.Unmarshal(raw, &hash); err != nil {
			return 0, false, fmt.Errorf("decode access hash: %w", err)
		}
		return hash, true, nil
	}
	return fallback()
}
func (h persistentHasher) set(ctx context.Context, kind string, userID, targetID, hash int64, fallback func() error) error {
	raw, err := json.Marshal(hash)
	if err != nil {
		return err
	}
	if err := h.kv.Set(ctx, hashKey(kind, userID, targetID), raw); err != nil {
		return err
	}
	return fallback()
}
func (h persistentHasher) SetChannelAccessHash(ctx context.Context, userID, channelID, hash int64) error {
	return h.set(ctx, "channel", userID, channelID, hash, func() error { return h.delegate.SetChannelAccessHash(ctx, userID, channelID, hash) })
}
func (h persistentHasher) GetChannelAccessHash(ctx context.Context, userID, channelID int64) (int64, bool, error) {
	return h.get(ctx, "channel", userID, channelID, func() (int64, bool, error) { return h.delegate.GetChannelAccessHash(ctx, userID, channelID) })
}
func (h persistentHasher) SetUserAccessHash(ctx context.Context, userID, targetID, hash int64) error {
	return h.set(ctx, "user", userID, targetID, hash, func() error { return h.delegate.SetUserAccessHash(ctx, userID, targetID, hash) })
}
func (h persistentHasher) GetUserAccessHash(ctx context.Context, userID, targetID int64) (int64, bool, error) {
	return h.get(ctx, "user", userID, targetID, func() (int64, bool, error) { return h.delegate.GetUserAccessHash(ctx, userID, targetID) })
}
