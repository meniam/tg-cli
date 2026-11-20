package tgclient

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

func syntheticEvents(t *testing.T, update tg.UpdateClass) []rpc.Event {
	t.Helper()
	c := New(Config{AppID: 1, AppHash: "test"})
	var events []rpc.Event
	c.SetEventSink(func(event rpc.Event) { events = append(events, event) })
	batch := &tg.Updates{Updates: []tg.UpdateClass{update}, Users: []tg.UserClass{user(1, "Ann", "", "ann", false)}, Chats: []tg.ChatClass{&tg.Chat{ID: 7, Title: "Family"}, channel(8, "News", "news", true, false)}}
	if err := c.updateDispatcher().Handle(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestGotdUpdatesBecomeEvents(t *testing.T) {
	newMessage := &tg.Message{ID: 11, PeerID: &tg.PeerUser{UserID: 1}, Date: 1700000000, Message: "hello", FromID: &tg.PeerUser{UserID: 1}}
	channelMessage := &tg.Message{ID: 12, PeerID: &tg.PeerChannel{ChannelID: 8}, Date: 1700000001, Message: "channel"}
	groupMessage := &tg.Message{ID: 13, PeerID: &tg.PeerChat{ChatID: 7}, Date: 1700000002, Message: "group"}
	edit := &tg.Message{ID: 14, PeerID: &tg.PeerChannel{ChannelID: 8}, Date: 1700000003, Message: "edited"}
	edit.SetEditDate(1700000010)
	cases := []struct {
		name   string
		update tg.UpdateClass
		typ    string
		chat   *int64
		key    string
		field  string
	}{
		{"new private", &tg.UpdateNewMessage{Message: newMessage}, rpc.EventMessageNew, int64p(1), "new:1:11", "hello"},
		{"new channel", &tg.UpdateNewChannelMessage{Message: channelMessage}, rpc.EventMessageNew, int64p(-1000000000008), "new:-1000000000008:12", "channel"},
		{"new group", &tg.UpdateNewMessage{Message: groupMessage}, rpc.EventMessageNew, int64p(-7), "new:-7:13", "group"},
		{"edit channel", &tg.UpdateEditChannelMessage{Message: edit}, rpc.EventMessageEdit, int64p(-1000000000008), "edit:-1000000000008:14:1700000010", "edited"},
		{"edit private", &tg.UpdateEditMessage{Message: newMessage}, rpc.EventMessageEdit, int64p(1), "edit:1:11:1700000000", "hello"},
		{"delete private", &tg.UpdateDeleteMessages{Messages: []int{9, 2}}, rpc.EventMessageDelete, nil, "del:0:2,9", `"ids":[2,9]`},
		{"delete channel", &tg.UpdateDeleteChannelMessages{ChannelID: 8, Messages: []int{12}}, rpc.EventMessageDelete, int64p(-1000000000008), "del:-1000000000008:12", `"ids":[12]`},
		{"reaction", &tg.UpdateMessageReactions{Peer: &tg.PeerChat{ChatID: 7}, MsgID: 13, Reactions: tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 2}}}}, rpc.EventReaction, int64p(-7), "react:-7:13:", `"emoji":"👍"`},
		{"read inbox", &tg.UpdateReadHistoryInbox{Peer: &tg.PeerUser{UserID: 1}, MaxID: 11}, rpc.EventRead, int64p(1), "", `"outbox":false`},
		{"read history outbox", &tg.UpdateReadHistoryOutbox{Peer: &tg.PeerChat{ChatID: 7}, MaxID: 13}, rpc.EventRead, int64p(-7), "", `"outbox":true`},
		{"read channel inbox", &tg.UpdateReadChannelInbox{ChannelID: 8, MaxID: 12}, rpc.EventRead, int64p(-1000000000008), "", `"outbox":false`},
		{"read outbox", &tg.UpdateReadChannelOutbox{ChannelID: 8, MaxID: 12}, rpc.EventRead, int64p(-1000000000008), "", `"outbox":true`},
		{"typing user", &tg.UpdateUserTyping{UserID: 1, Action: &tg.SendMessageTypingAction{}}, rpc.EventTyping, int64p(1), "", `"user_id":1`},
		{"typing group", &tg.UpdateChatUserTyping{ChatID: 7, FromID: &tg.PeerUser{UserID: 1}, Action: &tg.SendMessageTypingAction{}}, rpc.EventTyping, int64p(-7), "", `"user_id":1`},
		{"typing channel", &tg.UpdateChannelUserTyping{ChannelID: 8, FromID: &tg.PeerUser{UserID: 1}, Action: &tg.SendMessageTypingAction{}}, rpc.EventTyping, int64p(-1000000000008), "", `"user_id":1`},
		{"status", &tg.UpdateUserStatus{UserID: 1, Status: &tg.UserStatusOnline{Expires: 1700000100}}, rpc.EventStatus, nil, "", `"online":true`},
		{"status offline", &tg.UpdateUserStatus{UserID: 1, Status: &tg.UserStatusOffline{WasOnline: 1700000000}}, rpc.EventStatus, nil, "", `"online":false`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := syntheticEvents(t, tc.update)
			if len(events) != 1 {
				t.Fatalf("events=%+v", events)
			}
			event := events[0]
			if event.Type != tc.typ || !sameID(event.ChatID, tc.chat) || !audit10KeyMatches(tc.typ, event.DedupKey, tc.key) || !strings.Contains(string(event.Data), tc.field) {
				t.Errorf("event=%+v data=%s", event, event.Data)
			}
			if event.ID != "" {
				t.Errorf("unexpected cursor %q", event.ID)
			}
		})
	}
	encoded, err := json.Marshal(syntheticEvents(t, &tg.UpdateNewMessage{Message: newMessage})[0])
	if err != nil || strings.Contains(string(encoded), "dedup") || strings.Contains(string(encoded), "new:1:11") {
		t.Errorf("dedup key leaked to wire: %s %v", encoded, err)
	}
	channelEvent := syntheticEvents(t, &tg.UpdateNewChannelMessage{Message: channelMessage})[0]
	if !strings.Contains(string(channelEvent.Data), `"name":"News"`) {
		t.Errorf("channel author missing from %s", channelEvent.Data)
	}
	editedEvent := syntheticEvents(t, &tg.UpdateEditChannelMessage{Message: edit})[0]
	if editedEvent.Time != formatDate(1700000010) {
		t.Errorf("edit event time=%s", editedEvent.Time)
	}
	if events := syntheticEvents(t, &tg.UpdateWebPage{}); len(events) != 0 {
		t.Errorf("unknown update produced events: %+v", events)
	}
}

// audit10KeyMatches compares the dedup key exactly: an empty key must stay empty,
// because a repeated non-empty key makes the daemon drop later events as duplicates.
// Only reaction keys end in a hash, so they are compared by prefix.
func audit10KeyMatches(typ, got, want string) bool {
	if typ == rpc.EventReaction {
		return strings.HasPrefix(got, want)
	}
	return got == want
}

func int64p(v int64) *int64 { return &v }
func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestGapCallbacksAndSlowSink(t *testing.T) {
	c := New(Config{AppID: 1, AppHash: "test"})
	var events []rpc.Event
	c.SetEventSink(func(event rpc.Event) { events = append(events, event) })
	cfg := c.updateConfig()
	if cfg.BeforeRun != updates.BeforeRunBuffer || !cfg.StopOnApplyError {
		t.Errorf("manager error/buffer policy: %+v", cfg)
	}
	cfg.OnChannelTooLong(8)
	cfg.OnChannelGap(updates.ChannelGap{ChannelID: 8})
	cfg.OnChannelInaccessible(8)
	cfg.OnTooLong()
	if len(events) != 4 {
		t.Fatalf("gap events=%+v", events)
	}
	wantReasons := []string{"channel_too_long", "channel_gap", "channel_inaccessible", "too_long"}
	for i, event := range events {
		if event.Type != rpc.EventGap || event.DedupKey != "" {
			t.Errorf("gap %d=%+v", i, event)
		}
		var data rpc.GapData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Reason != wantReasons[i] {
			t.Errorf("gap %d reason = %q (err %v), want %q", i, data.Reason, err, wantReasons[i])
		}
		if i < 3 && !sameID(event.ChatID, int64p(-1000000000008)) {
			t.Errorf("channel gap=%+v", event)
		}
		if i == 3 && event.ChatID != nil {
			t.Errorf("common gap=%+v", event)
		}
	}
	// A sink that replaces itself would deadlock if the conversion lock were held
	// during the callback. The timeout is only a safety net for that regression.
	finished := make(chan struct{})
	c.SetEventSink(func(rpc.Event) { c.SetEventSink(nil); close(finished) })
	deleted := make(chan error, 1)
	go func() { deleted <- c.deleteEvent(0, []int{1}) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("sink callback held the conversion lock")
	}
	if err := <-deleted; err != nil {
		t.Fatalf("deleteEvent: %v", err)
	}
}

func TestReactionKeyIgnoresCountOrder(t *testing.T) {
	first := syntheticEvents(t, &tg.UpdateMessageReactions{Peer: &tg.PeerChat{ChatID: 7}, MsgID: 13, Reactions: tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "a"}, Count: 1}, {Reaction: &tg.ReactionEmoji{Emoticon: "b"}, Count: 2}}}})
	second := syntheticEvents(t, &tg.UpdateMessageReactions{Peer: &tg.PeerChat{ChatID: 7}, MsgID: 13, Reactions: tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "b"}, Count: 2}, {Reaction: &tg.ReactionEmoji{Emoticon: "a"}, Count: 1}}}})
	if len(first) != 1 || len(second) != 1 || first[0].DedupKey != second[0].DedupKey {
		t.Errorf("reaction keys differ: %+v vs %+v", first, second)
	}
}

type fakeStateKV struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func (f *fakeStateKV) Get(_ context.Context, key string) (json.RawMessage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[key]
	return value, ok, nil
}
func (f *fakeStateKV) Set(_ context.Context, key string, value json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]json.RawMessage{}
	}
	f.values[key] = append(json.RawMessage(nil), value...)
	return nil
}

func TestStateStorageUsesAccountKeysAndEnumeratesChannels(t *testing.T) {
	ctx := context.Background()
	kv := &fakeStateKV{}
	s := &updateStateStorage{kv: kv}
	if _, found, err := s.GetState(ctx, 42); err != nil || found {
		t.Fatalf("missing state: %v %v", found, err)
	}
	if err := s.SetState(ctx, 42, updates.State{Pts: 1, Qts: 2, Date: 3, Seq: 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPts(ctx, 42, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDateSeq(ctx, 42, 6, 7); err != nil {
		t.Fatal(err)
	}
	state, found, err := s.GetState(ctx, 42)
	if err != nil || !found || state.Pts != 5 || state.Qts != 2 || state.Date != 6 || state.Seq != 7 {
		t.Errorf("state=%+v found=%v err=%v", state, found, err)
	}
	for _, id := range []int64{8, 7} {
		if err := s.SetChannelPts(ctx, 42, id, int(id+10)); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	indexed := map[int64]int{}
	stored := map[int64]int{}
	if err := s.ForEachChannels(ctx, 42, func(ctx context.Context, id int64, pts int) error {
		ids = append(ids, id)
		indexed[id] = pts
		got, found, err := s.GetChannelPts(ctx, 42, id)
		if err == nil && !found {
			t.Errorf("channel %d is indexed but GetChannelPts reports it missing", id)
		}
		stored[id] = got
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := map[int64]int{7: 17, 8: 18}
	if !slices.Equal(ids, []int64{7, 8}) || !maps.Equal(indexed, want) || !maps.Equal(stored, want) {
		t.Errorf("channels=%v index=%v GetChannelPts=%v, want %v", ids, indexed, stored, want)
	}
	if _, ok := kv.values[userStateKey(42)]; !ok {
		t.Error("user key missing")
	}
	if _, ok := kv.values[channelStateKey(42, 8)]; !ok {
		t.Error("channel key missing")
	}
	if err := s.SetState(ctx, 42, updates.State{}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.GetChannelPts(ctx, 42, 8); err != nil || found {
		t.Errorf("reset channel: found=%v err=%v", found, err)
	}
}

type fakeHasher struct{}

func (fakeHasher) SetChannelAccessHash(context.Context, int64, int64, int64) error { return nil }
func (fakeHasher) GetChannelAccessHash(context.Context, int64, int64) (int64, bool, error) {
	return 0, false, nil
}
func (fakeHasher) SetUserAccessHash(context.Context, int64, int64, int64) error { return nil }
func (fakeHasher) GetUserAccessHash(context.Context, int64, int64) (int64, bool, error) {
	return 0, false, nil
}

func TestAccessHashesSurviveHasherReplacement(t *testing.T) {
	ctx := context.Background()
	kv := &fakeStateKV{}
	first := persistentHasher{delegate: fakeHasher{}, kv: kv}
	if err := first.SetChannelAccessHash(ctx, 42, 8, 123); err != nil {
		t.Fatal(err)
	}
	if err := first.SetUserAccessHash(ctx, 42, 1, 456); err != nil {
		t.Fatal(err)
	}
	second := persistentHasher{delegate: fakeHasher{}, kv: kv}
	if hash, found, err := second.GetChannelAccessHash(ctx, 42, 8); err != nil || !found || hash != 123 {
		t.Errorf("channel hash=%d found=%v err=%v", hash, found, err)
	}
	if hash, found, err := second.GetUserAccessHash(ctx, 42, 1); err != nil || !found || hash != 456 {
		t.Errorf("user hash=%d found=%v err=%v", hash, found, err)
	}
}
