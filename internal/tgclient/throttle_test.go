package tgclient

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

type throttleClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*throttleTimer]struct{}
}

type throttleTimer struct {
	clock    *throttleClock
	deadline time.Time
	ch       chan time.Time
}

func newThrottleClock() *throttleClock {
	return &throttleClock{now: time.Unix(0, 0), timers: make(map[*throttleTimer]struct{})}
}

func (c *throttleClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *throttleClock) NewTimer(deadline time.Time) throttle.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &throttleTimer{clock: c, deadline: deadline, ch: make(chan time.Time, 1)}
	if !deadline.After(c.now) {
		t.ch <- c.now
	} else {
		c.timers[t] = struct{}{}
	}
	return t
}

func (t *throttleTimer) C() <-chan time.Time { return t.ch }
func (t *throttleTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	return active
}

func (c *throttleClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for t := range c.timers {
		if !t.deadline.After(c.now) {
			t.ch <- c.now
			delete(c.timers, t)
		}
	}
	c.mu.Unlock()
}

func (c *throttleClock) awaitTimer(t *testing.T, deadline time.Time) {
	t.Helper()
	until := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		found := false
		for timer := range c.timers {
			if timer.deadline.Equal(deadline) {
				found = true
				break
			}
		}
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-until:
			t.Fatalf("timer for %s was not scheduled", deadline)
		default:
			runtime.Gosched()
		}
	}
}

func limiterWithClock(t *testing.T, clock *throttleClock, configure func(*throttle.Config)) *throttle.Limiter {
	t.Helper()
	cfg := throttle.DefaultConfig()
	if configure != nil {
		configure(&cfg)
	}
	l, err := throttle.New(cfg, clock)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func invokeResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("invocation did not finish")
		return nil
	}
}

func invokeAsync(invoke telegram.InvokeFunc, ctx context.Context, input bin.Encoder) <-chan error {
	result := make(chan error, 1)
	go func() { result <- invoke(ctx, input, nil) }()
	return result
}

func TestThrottleRequestClasses(t *testing.T) {
	user := &tg.InputPeerUser{UserID: 42, AccessHash: 777}
	channel := &tg.InputPeerChannel{ChannelID: 53, AccessHash: 999}
	cases := []struct {
		name    string
		request bin.Encoder
		class   throttle.Class
		key     string
	}{
		{"send message user", &tg.MessagesSendMessageRequest{Peer: user}, throttle.PrivateSend, "user:42"},
		{"send media self", &tg.MessagesSendMediaRequest{Peer: &tg.InputPeerSelf{}}, throttle.PrivateSend, "self"},
		{"send album chat", &tg.MessagesSendMultiMediaRequest{Peer: &tg.InputPeerChat{ChatID: 51}}, throttle.GroupSend, "chat:51"},
		{"forward destination", &tg.MessagesForwardMessagesRequest{FromPeer: user, ToPeer: channel}, throttle.GroupSend, "channel:53"},
		{"user from message", &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUserFromMessage{UserID: 42}}, throttle.PrivateSend, "user:42"},
		{"channel from message", &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerChannelFromMessage{ChannelID: 53}}, throttle.GroupSend, "channel:53"},
		{"unknown peer", &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerEmpty{}}, throttle.GroupSend, "unknown"},
		{"nil self peer", &tg.MessagesSendMessageRequest{Peer: (*tg.InputPeerSelf)(nil)}, throttle.GroupSend, "unknown"},
		{"other method", &tg.MessagesGetHistoryRequest{}, throttle.General, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, key := classifyRequest(tc.request)
			if class != tc.class || key != tc.key {
				t.Fatalf("got (%v, %q), want (%v, %q)", class, key, tc.class, tc.key)
			}
		})
	}
}

func TestThrottleFloodAutoWaitAndOneRetry(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	flood := tgerr.New(420, "FLOOD_WAIT_2")
	var calls int
	next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		if calls == 1 {
			return flood
		}
		return nil
	})
	invoke := throttleMiddleware(l).Handle(next)
	waited := new(throttle.Wait)
	ctx := throttle.WithWait(context.Background(), waited)
	result := invokeAsync(invoke, ctx, &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(2*time.Second))
	clock.Advance(2 * time.Second)
	if err := invokeResult(t, result); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if waited.Duration() != 2*time.Second {
		t.Fatalf("waited = %s", waited.Duration())
	}
}

func TestThrottleFloodOverThreshold(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	flood := tgerr.New(420, "FLOOD_WAIT_11")
	var calls int
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return flood
	}))
	if err := invoke(context.Background(), &tg.MessagesGetHistoryRequest{}, nil); err != flood {
		t.Fatalf("error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestThrottleSecondFloodStopsAfterOneRetry(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	first := tgerr.New(420, "FLOOD_WAIT_2")
	second := tgerr.New(420, "FLOOD_WAIT_3")
	var calls int
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		if calls == 1 {
			return first
		}
		if calls == 2 {
			return second
		}
		return nil
	}))
	result := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(2*time.Second))
	clock.Advance(2 * time.Second)
	if err := invokeResult(t, result); err != second {
		t.Fatalf("error = %v, want second flood", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	other := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(3*time.Second))
	clock.Advance(3 * time.Second)
	// The new request can run after the second flood pause; its result is separate.
	_ = invokeResult(t, other)
}

func TestThrottleSendFloodPausesWithoutRetry(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	flood := tgerr.New(420, "FLOOD_WAIT_2")
	var calls int
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		if calls == 1 {
			return flood
		}
		return nil
	}))
	if err := invoke(context.Background(), &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUser{UserID: 1}}, nil); err != flood {
		t.Fatalf("error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("send calls = %d, want 1", calls)
	}
	other := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(2*time.Second))
	clock.Advance(2 * time.Second)
	if err := invokeResult(t, other); err != nil {
		t.Fatalf("other error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestThrottleWriteFloodNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request bin.Encoder
	}{
		{"edit", &tg.MessagesEditMessageRequest{Peer: &tg.InputPeerUser{UserID: 1}, ID: 3}},
		{"delete", &tg.MessagesDeleteMessagesRequest{ID: []int{3}}},
		{"channel delete", &tg.ChannelsDeleteMessagesRequest{Channel: &tg.InputChannel{ChannelID: 2}, ID: []int{3}}},
		{"mark read", &tg.MessagesReadHistoryRequest{Peer: &tg.InputPeerUser{UserID: 1}, MaxID: 3}},
		{"channel mark read", &tg.ChannelsReadHistoryRequest{Channel: &tg.InputChannel{ChannelID: 2}, MaxID: 3}},
		{"react", &tg.MessagesSendReactionRequest{Peer: &tg.InputPeerUser{UserID: 1}, MsgID: 3}},
		{"pin", &tg.MessagesUpdatePinnedMessageRequest{Peer: &tg.InputPeerUser{UserID: 1}, ID: 3}},
		{"poll", &tg.MessagesSendMediaRequest{Peer: &tg.InputPeerUser{UserID: 1}, Media: &tg.InputMediaPoll{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limiter := limiterWithClock(t, newThrottleClock(), nil)
			flood := tgerr.New(420, "FLOOD_WAIT_2")
			calls := 0
			invoke := throttleMiddleware(limiter).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return flood }))
			if err := invoke(context.Background(), tc.request, nil); err != flood || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestThrottleSlowmodeDoesNotPause(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	slow := tgerr.New(420, "SLOWMODE_WAIT_5")
	var calls int
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		if calls == 1 {
			return slow
		}
		return nil
	}))
	if err := invoke(context.Background(), &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUser{UserID: 1}}, nil); err != slow {
		t.Fatalf("error = %v", err)
	}
	if err := invoke(context.Background(), &tg.MessagesGetHistoryRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestThrottleFloodPauseBlocksParallelRequest(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	flood := tgerr.New(420, "FLOOD_WAIT_11")
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var calls int
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(entered)
			<-release
			return flood
		}
		return nil
	}))
	first := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	<-entered
	second := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	// Keep the first request in flight until the second has reached the invoker.
	// Then a third request must wait for the account-wide flood gate.
	if err := invokeResult(t, second); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := invokeResult(t, first); err != flood {
		t.Fatalf("first error = %v", err)
	}
	third := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(11*time.Second))
	clock.Advance(11 * time.Second)
	if err := invokeResult(t, third); err != nil {
		t.Fatal(err)
	}
}

func backgroundHistoryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, historyPageContextKey{}, true)
}

func TestBackgroundHistoryRetriesEveryFloodWait(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	floods := []error{tgerr.New(420, "FLOOD_WAIT_5"), tgerr.New(420, "FLOOD_WAIT_120")}
	calls := 0
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		if calls < len(floods) {
			err := floods[calls]
			calls++
			return err
		}
		calls++
		return nil
	}))
	result := invokeAsync(invoke, backgroundHistoryContext(context.Background()), &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(5*time.Second))
	clock.Advance(5 * time.Second)
	clock.awaitTimer(t, clock.Now().Add(120*time.Second))
	clock.Advance(120 * time.Second)
	if err := invokeResult(t, result); err != nil || calls != 3 {
		t.Fatalf("background retries: err=%v calls=%d", err, calls)
	}
}

func TestBackgroundHistoryCancellationDuringFloodWait(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	calls := 0
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_120")
	}))
	ctx, cancel := context.WithCancel(backgroundHistoryContext(context.Background()))
	result := invokeAsync(invoke, ctx, &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(120*time.Second))
	cancel()
	if err := invokeResult(t, result); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled background request: err=%v calls=%d", err, calls)
	}
}

func TestBackgroundFloodFastRejectMapsToRPCCode(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	calls := 0
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_120")
	}))
	ctx, cancel := context.WithCancel(backgroundHistoryContext(context.Background()))
	background := invokeAsync(invoke, ctx, &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(120*time.Second))
	err := mapError(invoke(context.Background(), &tg.MessagesGetHistoryRequest{}, nil))
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait || rpcErr.Data.(map[string]any)["retry_after_seconds"] != 120 || calls != 1 {
		t.Fatalf("ordinary request: err=%v calls=%d", err, calls)
	}
	cancel()
	if err := invokeResult(t, background); !errors.Is(err, context.Canceled) {
		t.Fatalf("background cancellation: %v", err)
	}
}

func TestOrdinaryFloodPauseStillQueues(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	calls := 0
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		if calls == 1 {
			return tgerr.New(420, "FLOOD_WAIT_120")
		}
		return nil
	}))
	if err := invoke(context.Background(), &tg.MessagesGetHistoryRequest{}, nil); err == nil {
		t.Fatal("ordinary long flood wait was retried")
	}
	second := invokeAsync(invoke, context.Background(), &tg.MessagesGetHistoryRequest{})
	clock.awaitTimer(t, clock.Now().Add(120*time.Second))
	clock.Advance(120 * time.Second)
	if err := invokeResult(t, second); err != nil || calls != 2 {
		t.Fatalf("ordinary queued request: err=%v calls=%d", err, calls)
	}
}

func TestBackgroundPolicyOnlyAppliesToHistory(t *testing.T) {
	clock := newThrottleClock()
	l := limiterWithClock(t, clock, nil)
	calls := 0
	invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_120")
	}))
	err := invoke(backgroundHistoryContext(context.Background()), &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUser{UserID: 1}}, nil)
	if _, flood := tgerr.AsFloodWait(err); !flood || calls != 1 {
		t.Fatalf("non-history request: err=%v calls=%d", err, calls)
	}
}

func TestClientFloodWaitRemaining(t *testing.T) {
	clock := newThrottleClock()
	limiter := limiterWithClock(t, clock, nil)
	client := &Client{throttle: limiter}
	if got := client.FloodWaitRemaining(); got != 0 {
		t.Fatalf("initial pause = %s", got)
	}
	limiter.PauseBackground(12 * time.Second)
	clock.Advance(2 * time.Second)
	if got := client.FloodWaitRemaining(); got != 10*time.Second {
		t.Fatalf("remaining pause = %s", got)
	}
	if got := (&Client{}).FloodWaitRemaining(); got != 0 {
		t.Fatalf("nil limiter pause = %s", got)
	}
}
