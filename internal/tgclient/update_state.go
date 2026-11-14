package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/gotd/td/telegram/updates"
)

// StateKV is the small persistence boundary used by the updates manager.
// store.DB is adapted to this interface at the composition root.
type StateKV interface {
	Get(context.Context, string) (json.RawMessage, bool, error)
	Set(context.Context, string, json.RawMessage) error
}

type updateStateStorage struct {
	kv StateKV
	mu sync.Mutex
}

var _ updates.StateStorage = (*updateStateStorage)(nil)

func userStateKey(userID int64) string { return fmt.Sprintf("updates.state.%d", userID) }
func channelStateKey(userID, channelID int64) string {
	return fmt.Sprintf("updates.channel.%d.%d", userID, channelID)
}
func channelIndexKey(userID int64) string { return fmt.Sprintf("updates.channels.%d", userID) }

func (s *updateStateStorage) GetState(ctx context.Context, userID int64) (updates.State, bool, error) {
	raw, found, err := s.kv.Get(ctx, userStateKey(userID))
	if err != nil || !found {
		return updates.State{}, found, err
	}
	var state updates.State
	if err := json.Unmarshal(raw, &state); err != nil {
		return updates.State{}, false, fmt.Errorf("decode update state: %w", err)
	}
	return state, true, nil
}

func (s *updateStateStorage) SetState(ctx context.Context, userID int64, state updates.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := s.kv.Set(ctx, userStateKey(userID), raw); err != nil {
		return err
	}
	return s.kv.Set(ctx, channelIndexKey(userID), json.RawMessage(`{}`))
}

func (s *updateStateStorage) changeState(ctx context.Context, userID int64, change func(*updates.State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, found, err := s.GetState(ctx, userID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("update state does not exist")
	}
	change(&state)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.kv.Set(ctx, userStateKey(userID), raw)
}

func (s *updateStateStorage) SetPts(ctx context.Context, userID int64, pts int) error {
	return s.changeState(ctx, userID, func(v *updates.State) { v.Pts = pts })
}
func (s *updateStateStorage) SetQts(ctx context.Context, userID int64, qts int) error {
	return s.changeState(ctx, userID, func(v *updates.State) { v.Qts = qts })
}
func (s *updateStateStorage) SetDate(ctx context.Context, userID int64, date int) error {
	return s.changeState(ctx, userID, func(v *updates.State) { v.Date = date })
}
func (s *updateStateStorage) SetSeq(ctx context.Context, userID int64, seq int) error {
	return s.changeState(ctx, userID, func(v *updates.State) { v.Seq = seq })
}
func (s *updateStateStorage) SetDateSeq(ctx context.Context, userID int64, date, seq int) error {
	return s.changeState(ctx, userID, func(v *updates.State) { v.Date = date; v.Seq = seq })
}

func (s *updateStateStorage) channels(ctx context.Context, userID int64) (map[string]int, error) {
	raw, found, err := s.kv.Get(ctx, channelIndexKey(userID))
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]int{}, nil
	}
	var values map[string]int
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode channel index: %w", err)
	}
	if values == nil {
		values = map[string]int{}
	}
	return values, nil
}

func (s *updateStateStorage) GetChannelPts(ctx context.Context, userID, channelID int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels, err := s.channels(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	pts, found := channels[strconv.FormatInt(channelID, 10)]
	return pts, found, nil
}

func (s *updateStateStorage) SetChannelPts(ctx context.Context, userID, channelID int64, pts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found, err := s.GetState(ctx, userID); err != nil {
		return err
	} else if !found {
		return errors.New("update state does not exist")
	}
	channels, err := s.channels(ctx, userID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(pts)
	if err != nil {
		return err
	}
	if err := s.kv.Set(ctx, channelStateKey(userID, channelID), raw); err != nil {
		return err
	}
	channels[strconv.FormatInt(channelID, 10)] = pts
	raw, err = json.Marshal(channels)
	if err != nil {
		return err
	}
	return s.kv.Set(ctx, channelIndexKey(userID), raw)
}

func (s *updateStateStorage) ForEachChannels(ctx context.Context, userID int64, f func(context.Context, int64, int) error) error {
	s.mu.Lock()
	channels, err := s.channels(ctx, userID)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(channels))
	for key := range channels {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid channel index: %w", err)
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if err := f(ctx, id, channels[strconv.FormatInt(id, 10)]); err != nil {
			return err
		}
	}
	return nil
}
