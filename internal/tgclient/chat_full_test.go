package tgclient

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"tg-cli/internal/throttle"
)

// TestChatFullOneBackgroundRequest covers ChatFull: one Telegram request per chat,
// made as background traffic of the account throttle, parsed by the same code as
// chat info. The fake invoker records the request and the admission the middleware
// asked for.
func TestChatFullOneBackgroundRequest(t *testing.T) {
	channel := channel(9, "News", "news", true, false)
	channel.Photo = &tg.ChatPhotoEmpty{}
	channel.Restricted = true
	channel.Date = 1700000000
	channel.SetRestrictionReason([]tg.RestrictionReason{{Platform: "ios", Reason: "porno", Text: "Not available"}})

	var mu sync.Mutex
	var requests []string
	var backgrounds []bool
	next := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		mu.Lock()
		requests = append(requests, typeName(input))
		backgrounds = append(backgrounds, throttle.BackgroundFromContext(ctx))
		mu.Unlock()
		switch input.(type) {
		case *tg.ChannelsGetFullChannelRequest:
			return respond(output, &tg.MessagesChatFull{
				FullChat: &tg.ChannelFull{ID: 9, About: "About the news", ParticipantsCount: 42, SlowmodeSeconds: 30, ChatPhoto: &tg.PhotoEmpty{}},
				Chats:    []tg.ChatClass{channel},
			})
		}
		return errors.New("unexpected request " + typeName(input))
	})
	clock := newThrottleClock()
	client := chatFullClient(t, next, limiterWithClock(t, clock, nil), channel)

	ctx := context.Background()
	// The peer must be known before the call, so the call itself makes one request.
	info, err := client.ChatFull(ctx, -1000000000009)
	if err != nil {
		t.Fatalf("chat full: %v", err)
	}
	if info.About != "About the news" || info.Members != 42 || info.ID != -1000000000009 {
		t.Errorf("info = %+v", info)
	}
	if !info.Restricted || len(info.RestrictionReason) != 1 || info.CreatedAt != 1700000000 {
		t.Errorf("flags = %+v", info)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %v, want one", requests)
	}
	if !backgrounds[0] {
		t.Errorf("the request was not marked as background traffic")
	}
}

// TestChatFullOfABasicGroup covers the other branch of the parser: a basic group
// reads its description and size from messages.getFullChat, one request, marked as
// background traffic.
func TestChatFullOfABasicGroup(t *testing.T) {
	group := &tg.Chat{ID: 4, Title: "Family", Date: 1600000000, Photo: &tg.ChatPhotoEmpty{}}
	var mu sync.Mutex
	var requests int
	var background bool
	next := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		mu.Lock()
		requests++
		background = throttle.BackgroundFromContext(ctx)
		mu.Unlock()
		if _, ok := input.(*tg.MessagesGetFullChatRequest); !ok {
			return errors.New("unexpected request " + typeName(input))
		}
		return respond(output, &tg.MessagesChatFull{
			FullChat: &tg.ChatFull{About: "Family chat", Participants: &tg.ChatParticipants{}},
			Chats:    []tg.ChatClass{group},
		})
	})
	client := chatFullClient(t, next, limiterWithClock(t, newThrottleClock(), nil), nil, group)
	info, err := client.ChatFull(context.Background(), -4)
	if err != nil {
		t.Fatalf("chat full: %v", err)
	}
	if info.About != "Family chat" || info.Type != "group" || info.CreatedAt != 1600000000 {
		t.Errorf("info = %+v", info)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
	if !background {
		t.Errorf("the request was not marked as background traffic")
	}
}

// chatFullClient builds a client whose peer cache already knows the fixtures, so a
// call by id resolves without a Telegram lookup of its own.
func chatFullClient(t *testing.T, next telegram.InvokeFunc, limiter *throttle.Limiter, raw ...tg.ChatClass) *Client {
	t.Helper()
	api := tg.NewClient(throttleMiddleware(limiter).Handle(next))
	c := New(Config{})
	c.tg = telegram.NewClient(telegram.TestAppID, telegram.TestAppHash, telegram.Options{})
	c.throttle = limiter
	mgr := (peers.Options{Storage: &peers.InmemoryStorage{}}).Build(api)
	if err := mgr.Apply(context.Background(), nil, raw); err != nil {
		t.Fatalf("fill the peer cache: %v", err)
	}
	ready := make(chan struct{})
	close(ready)
	c.mgr, c.mgrReady = mgr, true
	return c
}

// typeName names the request type without a reflection import in the assertion.
func typeName(input bin.Encoder) string {
	switch input.(type) {
	case *tg.ChannelsGetFullChannelRequest:
		return "channels.getFullChannel"
	case *tg.MessagesGetFullChatRequest:
		return "messages.getFullChat"
	}
	return "other"
}
