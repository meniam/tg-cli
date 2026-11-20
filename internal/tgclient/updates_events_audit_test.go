package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/updates"
)

// audit10KV wraps fakeStateKV with injectable failures.
type audit10KV struct {
	fakeStateKV
	getErr, setErr error
	setCalls       int
}

func (k *audit10KV) Get(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if k.getErr != nil {
		return nil, false, k.getErr
	}
	return k.fakeStateKV.Get(ctx, key)
}

func (k *audit10KV) Set(ctx context.Context, key string, value json.RawMessage) error {
	k.setCalls++
	if k.setErr != nil {
		return k.setErr
	}
	return k.fakeStateKV.Set(ctx, key, value)
}

func TestAudit10_UpdateStateSettersAndMissingState(t *testing.T) {
	ctx := context.Background()
	s := &updateStateStorage{kv: &audit10KV{}}
	for name, call := range map[string]func() error{
		"SetPts":     func() error { return s.SetPts(ctx, 42, 1) },
		"SetQts":     func() error { return s.SetQts(ctx, 42, 1) },
		"SetDate":    func() error { return s.SetDate(ctx, 42, 1) },
		"SetSeq":     func() error { return s.SetSeq(ctx, 42, 1) },
		"SetDateSeq": func() error { return s.SetDateSeq(ctx, 42, 1, 2) },
		"SetChannel": func() error { return s.SetChannelPts(ctx, 42, 8, 1) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "update state does not exist") {
			t.Errorf("%s before SetState: %v", name, err)
		}
	}
	if err := s.SetState(ctx, 42, updates.State{Pts: 1, Qts: 2, Date: 3, Seq: 4}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		call func() error
		want updates.State
	}{
		{func() error { return s.SetQts(ctx, 42, 20) }, updates.State{Pts: 1, Qts: 20, Date: 3, Seq: 4}},
		{func() error { return s.SetDate(ctx, 42, 30) }, updates.State{Pts: 1, Qts: 20, Date: 30, Seq: 4}},
		{func() error { return s.SetSeq(ctx, 42, 40) }, updates.State{Pts: 1, Qts: 20, Date: 30, Seq: 40}},
	} {
		if err := step.call(); err != nil {
			t.Fatal(err)
		}
		if got, found, err := s.GetState(ctx, 42); err != nil || !found || got != step.want {
			t.Fatalf("state=%+v found=%v err=%v, want %+v", got, found, err, step.want)
		}
	}
}

func TestAudit10_UpdateStateIsolatesAccounts(t *testing.T) {
	ctx := context.Background()
	s := &updateStateStorage{kv: &audit10KV{}}
	for _, user := range []int64{42, 43} {
		if err := s.SetState(ctx, user, updates.State{Pts: int(user)}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetChannelPts(ctx, user, 8, int(user)*10); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []int64{42, 43} {
		state, _, _ := s.GetState(ctx, user)
		pts, found, err := s.GetChannelPts(ctx, user, 8)
		if state.Pts != int(user) || err != nil || !found || pts != int(user)*10 {
			t.Errorf("account %d: state=%+v channel pts=%d found=%v err=%v", user, state, pts, found, err)
		}
	}
	if _, found, err := s.GetChannelPts(ctx, 44, 8); err != nil || found {
		t.Errorf("unknown account: found=%v err=%v", found, err)
	}
}

func TestAudit10_UpdateStateStorageErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("kv down")
	t.Run("get error", func(t *testing.T) {
		s := &updateStateStorage{kv: &audit10KV{getErr: boom}}
		if _, _, err := s.GetState(ctx, 42); !errors.Is(err, boom) {
			t.Errorf("GetState: %v", err)
		}
		if _, _, err := s.GetChannelPts(ctx, 42, 8); !errors.Is(err, boom) {
			t.Errorf("GetChannelPts: %v", err)
		}
		if err := s.ForEachChannels(ctx, 42, nil); !errors.Is(err, boom) {
			t.Errorf("ForEachChannels: %v", err)
		}
		if err := s.SetPts(ctx, 42, 1); !errors.Is(err, boom) {
			t.Errorf("SetPts: %v", err)
		}
	})
	t.Run("set error", func(t *testing.T) {
		s := &updateStateStorage{kv: &audit10KV{setErr: boom}}
		if err := s.SetState(ctx, 42, updates.State{}); !errors.Is(err, boom) {
			t.Errorf("SetState: %v", err)
		}
	})
	t.Run("corrupt state", func(t *testing.T) {
		kv := &audit10KV{}
		kv.values = map[string]json.RawMessage{userStateKey(42): json.RawMessage(`{"pts":`)}
		s := &updateStateStorage{kv: kv}
		if _, found, err := s.GetState(ctx, 42); err == nil || found || !strings.Contains(err.Error(), "decode update state") {
			t.Errorf("GetState: found=%v err=%v", found, err)
		}
	})
	t.Run("corrupt channel index", func(t *testing.T) {
		kv := &audit10KV{}
		kv.values = map[string]json.RawMessage{channelIndexKey(42): json.RawMessage(`[1]`)}
		s := &updateStateStorage{kv: kv}
		if _, _, err := s.GetChannelPts(ctx, 42, 8); err == nil || !strings.Contains(err.Error(), "decode channel index") {
			t.Errorf("GetChannelPts: %v", err)
		}
		kv.values[channelIndexKey(42)] = json.RawMessage(`{"abc":1}`)
		if err := s.ForEachChannels(ctx, 42, func(context.Context, int64, int) error { return nil }); err == nil || !strings.Contains(err.Error(), "invalid channel index") {
			t.Errorf("ForEachChannels: %v", err)
		}
	})
	t.Run("callback error stops enumeration", func(t *testing.T) {
		s := &updateStateStorage{kv: &audit10KV{}}
		if err := s.SetState(ctx, 42, updates.State{}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{7, 8} {
			if err := s.SetChannelPts(ctx, 42, id, 1); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		if err := s.ForEachChannels(ctx, 42, func(context.Context, int64, int) error { calls++; return boom }); !errors.Is(err, boom) || calls != 1 {
			t.Errorf("err=%v calls=%d", err, calls)
		}
	})
}

type audit10Hasher struct {
	channelHashes, userHashes map[[2]int64]int64
	setCalls                  [][3]int64
}

func (h *audit10Hasher) SetChannelAccessHash(_ context.Context, user, id, hash int64) error {
	h.setCalls = append(h.setCalls, [3]int64{user, id, hash})
	return nil
}
func (h *audit10Hasher) GetChannelAccessHash(_ context.Context, user, id int64) (int64, bool, error) {
	hash, ok := h.channelHashes[[2]int64{user, id}]
	return hash, ok, nil
}
func (h *audit10Hasher) SetUserAccessHash(_ context.Context, user, id, hash int64) error {
	h.setCalls = append(h.setCalls, [3]int64{-user, id, hash})
	return nil
}
func (h *audit10Hasher) GetUserAccessHash(_ context.Context, user, id int64) (int64, bool, error) {
	hash, ok := h.userHashes[[2]int64{user, id}]
	return hash, ok, nil
}

func TestAudit10_PersistentHasherFallsBackAndForwardsWrites(t *testing.T) {
	ctx := context.Background()
	delegate := &audit10Hasher{
		channelHashes: map[[2]int64]int64{{42, 8}: 777},
		userHashes:    map[[2]int64]int64{{42, 1}: 888},
	}
	hasher := persistentHasher{delegate: delegate, kv: &audit10KV{}}
	if hash, found, err := hasher.GetChannelAccessHash(ctx, 42, 8); err != nil || !found || hash != 777 {
		t.Errorf("channel fallback: hash=%d found=%v err=%v", hash, found, err)
	}
	if hash, found, err := hasher.GetUserAccessHash(ctx, 42, 1); err != nil || !found || hash != 888 {
		t.Errorf("user fallback: hash=%d found=%v err=%v", hash, found, err)
	}
	if _, found, err := hasher.GetChannelAccessHash(ctx, 42, 99); err != nil || found {
		t.Errorf("unknown channel: found=%v err=%v", found, err)
	}
	if err := hasher.SetChannelAccessHash(ctx, 42, 9, 123); err != nil {
		t.Fatal(err)
	}
	if err := hasher.SetUserAccessHash(ctx, 42, 2, 456); err != nil {
		t.Fatal(err)
	}
	want := [][3]int64{{42, 9, 123}, {-42, 2, 456}}
	if len(delegate.setCalls) != 2 || delegate.setCalls[0] != want[0] || delegate.setCalls[1] != want[1] {
		t.Errorf("delegate writes = %v, want %v", delegate.setCalls, want)
	}
	if hash, found, err := hasher.GetChannelAccessHash(ctx, 43, 9); err != nil || found || hash != 0 {
		t.Errorf("another account sees the hash: hash=%d found=%v err=%v", hash, found, err)
	}
}

func TestAudit10_PersistentHasherErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("kv down")
	hasher := persistentHasher{delegate: &audit10Hasher{}, kv: &audit10KV{getErr: boom, setErr: boom}}
	if _, _, err := hasher.GetChannelAccessHash(ctx, 42, 8); !errors.Is(err, boom) {
		t.Errorf("get: %v", err)
	}
	if err := hasher.SetUserAccessHash(ctx, 42, 1, 5); !errors.Is(err, boom) {
		t.Errorf("set: %v", err)
	}
	kv := &audit10KV{}
	kv.values = map[string]json.RawMessage{hashKey("user", 42, 1): json.RawMessage(`"x"`)}
	if _, _, err := (persistentHasher{delegate: &audit10Hasher{}, kv: kv}).GetUserAccessHash(ctx, 42, 1); err == nil || !strings.Contains(err.Error(), "decode access hash") {
		t.Errorf("corrupt hash: %v", err)
	}
}

// TestAudit10_UpdateConfigWiresStateStorage checks the composition: with a state
// store configured, the updates manager persists state and access hashes
// through it; without one it keeps gotd's in-memory defaults.
func TestAudit10_UpdateConfigWiresStateStorage(t *testing.T) {
	plain := New(Config{AppID: 1, AppHash: "test"}).updateConfig()
	if plain.Storage != nil || plain.UserAccessHasher != nil {
		t.Errorf("without a state store: storage=%T user hasher=%T", plain.Storage, plain.UserAccessHasher)
	}
	kv := &audit10KV{}
	cfg := New(Config{AppID: 1, AppHash: "test", StateKV: kv}).updateConfig()
	storage, ok := cfg.Storage.(*updateStateStorage)
	if !ok || storage.kv != StateKV(kv) {
		t.Fatalf("storage = %T, want *updateStateStorage over the configured store", cfg.Storage)
	}
	hasher, ok := cfg.AccessHasher.(persistentHasher)
	if !ok || hasher.kv != StateKV(kv) {
		t.Fatalf("access hasher = %T, want persistentHasher over the configured store", cfg.AccessHasher)
	}
	if _, ok := cfg.UserAccessHasher.(persistentHasher); !ok {
		t.Fatalf("user access hasher = %T, want persistentHasher", cfg.UserAccessHasher)
	}
}
