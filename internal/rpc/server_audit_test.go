package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// audit07WaitLimit is a safety net: a passing test never waits for it.
const audit07WaitLimit = 5 * time.Second

// audit07Wait waits for a signal and fails with a clear message instead of
// hanging until the test binary times out.
func audit07Wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(audit07WaitLimit):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// audit07SyncBuffer is a log sink that is safe to read while the server writes.
type audit07SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *audit07SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *audit07SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func audit07ReadResponse(t *testing.T, conn net.Conn, reader *bufio.Reader) Response {
	t.Helper()
	var response Response
	if err := json.Unmarshal(readPipeFrame(t, conn, reader), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func audit07RPCError(t *testing.T, err error) *Error {
	t.Helper()
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error %v is not an *Error", err)
	}
	return rpcErr
}

func audit07Panics(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	f()
}

func TestAudit07_RequestGuardStopsHandlerAndIsLogged(t *testing.T) {
	var logs audit07SyncBuffer
	srv := NewServer(slog.New(slog.NewTextHandler(&logs, nil)))
	var called atomic.Int32
	srv.Handle("echo", func(context.Context, json.RawMessage) (any, error) {
		called.Add(1)
		return "ok", nil
	})
	var users sync.Map
	srv.SetRequestGuard(func(req Request) error {
		users.Store(req.ExpectUser, true)
		if req.ExpectUser == "wrong" {
			return NewError(CodeWriteDisabled, "guarded")
		}
		return nil
	})
	c := pipeClient(t, srv)
	ctx := ctxTimeout(t)

	c.SetExpectUser("wrong")
	err := c.Call(ctx, "echo", nil, nil)
	if e := audit07RPCError(t, err); e.Code != CodeWriteDisabled || e.Message != "guarded" {
		t.Fatalf("guard error: %+v", e)
	}
	if called.Load() != 0 {
		t.Fatal("the handler ran although the guard refused the request")
	}
	if got := logs.String(); !strings.Contains(got, "method=echo") || !strings.Contains(got, "code=-32008") {
		t.Fatalf("the refused request is not in the journal: %q", got)
	}

	c.SetExpectUser("right")
	if err := c.Call(ctx, "echo", nil, nil); err != nil {
		t.Fatalf("an allowed request failed: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", called.Load())
	}
	for _, user := range []string{"wrong", "right"} {
		if _, ok := users.Load(user); !ok {
			t.Errorf("the guard never saw expect_user %q", user)
		}
	}
}

func TestAudit07_RequestGuardCoversSubscribeAndUnsubscribe(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{}, nil })
	srv.SetRequestGuard(func(Request) error { return NewError(CodeWriteDisabled, "guarded") })
	conn, reader, _ := pipeSubscriptionServer(t, srv)
	for id, method := range []string{MethodSubscribe, MethodUnsubscribe} {
		sendPipeRequest(t, conn, id+1, method, nil)
		response := audit07ReadResponse(t, conn, reader)
		if response.Error == nil || response.Error.Code != CodeWriteDisabled {
			t.Errorf("%s: %+v", method, response)
		}
	}
	if n := hub.Subscribers(); n != 0 {
		t.Errorf("a refused subscribe left %d subscribers", n)
	}
}

func TestAudit07_RegistrationPanics(t *testing.T) {
	srv := NewServer(nil)
	srv.Handle("m", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	audit07Panics(t, "a duplicate Handle", func() {
		srv.Handle("m", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	})
	audit07Panics(t, "SetMethodTimeout of an unknown method", func() { srv.SetMethodTimeout("unknown", time.Second) })
	audit07Panics(t, "a zero method timeout", func() { srv.SetMethodTimeout("m", 0) })
}

func TestAudit07_MethodsAreSortedAndIncludeSubscriptionMethods(t *testing.T) {
	srv := NewServer(nil)
	for _, name := range []string{"b.two", "a.one", "c.three"} {
		srv.Handle(name, func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	}
	if got, want := srv.Methods(), []string{"a.one", "b.two", "c.three"}; !slices.Equal(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
	srv.SetEventSource(NewHub(), func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{}, nil })
	got := srv.Methods()
	if !slices.IsSorted(got) || !slices.Contains(got, MethodSubscribe) || !slices.Contains(got, MethodUnsubscribe) {
		t.Fatalf("methods with an event source = %v", got)
	}
}

func TestAudit07_UnserializableResultIsAnInternalError(t *testing.T) {
	srv := NewServer(nil)
	srv.Handle("bad", func(context.Context, json.RawMessage) (any, error) { return make(chan int), nil })
	err := pipeClient(t, srv).Call(ctxTimeout(t), "bad", nil, nil)
	if e := audit07RPCError(t, err); e.Code != CodeInternal || e.Message != "internal error" {
		t.Fatalf("error: %+v", e)
	}
}

// TestAudit07_ShutdownWithExpiredContextClosesEverything checks the forced
// path: a handler that never finishes must not keep Shutdown waiting once its
// context is over, and the handler must see its own context canceled.
func TestAudit07_ShutdownWithExpiredContextClosesEverything(t *testing.T) {
	srv := NewServer(nil)
	started, finished := make(chan struct{}), make(chan struct{})
	srv.Handle("hang", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	})
	c := pipeClient(t, srv)
	callErr := make(chan error, 1)
	go func() { callErr <- c.Call(context.Background(), "hang", nil, nil) }()
	audit07Wait(t, started, "the handler to start")

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Shutdown(expired); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown = %v, want context.Canceled", err)
	}
	audit07Wait(t, finished, "the handler context to be canceled")
	select {
	case err := <-callErr:
		if err == nil {
			t.Fatal("the call of a canceled handler succeeded")
		}
	case <-time.After(audit07WaitLimit):
		t.Fatal("the client call did not end after the forced shutdown")
	}
}

func TestAudit07_SubscribeNotificationIsIgnored(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{}, nil })
	conn, reader, _ := pipeSubscriptionServer(t, srv)
	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","method":"subscribe"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	// A round trip of an ordinary request gives a subscription made by the
	// notification the time to finish: the server reads frames in order.
	sendPipeRequest(t, conn, 6, "ping", nil)
	if response := audit07ReadResponse(t, conn, reader); string(response.ID) != "6" || response.Error != nil {
		t.Fatalf("ping: %+v", response)
	}
	// If the notification had created a subscription, this request is refused
	// as busy.
	sendPipeRequest(t, conn, 7, MethodSubscribe, nil)
	response := audit07ReadResponse(t, conn, reader)
	if string(response.ID) != "7" || response.Error != nil {
		t.Fatalf("subscribe after a notification: %+v", response)
	}
	if n := hub.Subscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
}

func TestAudit07_SubscribeRefusals(t *testing.T) {
	badRaw := func(raw string) func(*testing.T, net.Conn) {
		return func(t *testing.T, conn net.Conn) {
			line := `{"jsonrpc":"2.0","id":1,"method":"subscribe","params":` + raw + "}\n"
			if _, err := conn.Write([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
	}
	viaParams := func(p SubscribeParams) func(*testing.T, net.Conn) {
		return func(t *testing.T, conn net.Conn) { sendPipeRequest(t, conn, 1, MethodSubscribe, p) }
	}
	okReplay := func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{}, nil }
	for _, tc := range []struct {
		name    string
		replay  ReplayFunc // nil means a server without an event source
		send    func(*testing.T, net.Conn)
		code    int
		message string
	}{
		{"no event source", nil, viaParams(SubscribeParams{}), CodeMethodNotFound, "method not found: subscribe"},
		{"params of the wrong type", okReplay, badRaw(`"text"`), CodeInvalidParams, "invalid subscribe params"},
		{"negative topic", okReplay, viaParams(SubscribeParams{Topic: -1}), CodeInvalidParams, "topic must not be negative"},
		{"broken cursor", okReplay, viaParams(SubscribeParams{After: "bad"}), CodeInvalidParams, "invalid event cursor"},
		{"replay returns a typed error", func(context.Context, SubscribeParams) (ReplayPlan, error) {
			return ReplayPlan{}, NewError(CodeCursorExpired, "cursor is too old")
		}, viaParams(SubscribeParams{After: "1.0.0.0"}), CodeCursorExpired, "cursor is too old"},
		{"replay returns a plain error", func(context.Context, SubscribeParams) (ReplayPlan, error) {
			return ReplayPlan{}, errors.New("database is locked")
		}, viaParams(SubscribeParams{}), CodeInternal, "event replay failed"},
		{"replay events out of order", func(context.Context, SubscribeParams) (ReplayPlan, error) {
			return ReplayPlan{Cursor: "5.0.0.0", Events: []Event{{ID: "3.0.0.0"}, {ID: "2.0.0.0"}}}, nil
		}, viaParams(SubscribeParams{}), CodeInternal, "event replay returned an invalid cursor or order"},
		{"replay event beyond the cursor", func(context.Context, SubscribeParams) (ReplayPlan, error) {
			return ReplayPlan{Cursor: "2.0.0.0", Events: []Event{{ID: "3.0.0.0"}}}, nil
		}, viaParams(SubscribeParams{}), CodeInternal, "event replay returned an invalid cursor or order"},
		{"replay event before the resume cursor", func(context.Context, SubscribeParams) (ReplayPlan, error) {
			return ReplayPlan{Cursor: "5.0.0.0", Events: []Event{{ID: "1.0.0.0"}}}, nil
		}, viaParams(SubscribeParams{After: "2.0.0.0"}), CodeInternal, "event replay returned an invalid cursor or order"},
		{"replay cursor is broken", func(context.Context, SubscribeParams) (ReplayPlan, error) {
			return ReplayPlan{Cursor: "nonsense"}, nil
		}, viaParams(SubscribeParams{}), CodeInternal, "event replay returned an invalid cursor or order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			srv := NewServer(nil)
			if tc.replay != nil {
				srv.SetEventSource(hub, tc.replay)
			}
			conn, reader, _ := pipeSubscriptionServer(t, srv)
			tc.send(t, conn)
			response := audit07ReadResponse(t, conn, reader)
			if response.Error == nil || response.Error.Code != tc.code || response.Error.Message != tc.message {
				t.Fatalf("response: %+v, want code %d %q", response.Error, tc.code, tc.message)
			}
			if n := hub.Subscribers(); n != 0 {
				t.Fatalf("a refused subscribe left %d subscribers", n)
			}
		})
	}
}

func TestAudit07_UnsubscribeEdges(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{}, nil })
	conn, reader, _ := pipeSubscriptionServer(t, srv)

	// Nothing to cancel yet.
	sendPipeRequest(t, conn, 1, MethodUnsubscribe, nil)
	response := audit07ReadResponse(t, conn, reader)
	if response.Error != nil || !strings.Contains(string(response.Result), `"unsubscribed":false`) {
		t.Fatalf("unsubscribe without a subscription: %+v", response)
	}

	sendPipeRequest(t, conn, 2, MethodSubscribe, nil)
	var subscribed SubscribeResult
	response = audit07ReadResponse(t, conn, reader)
	if err := json.Unmarshal(response.Result, &subscribed); err != nil || subscribed.SubscriptionID == "" {
		t.Fatalf("subscribe: %+v %v", response, err)
	}

	// Somebody else's id is refused and keeps the subscription.
	sendPipeRequest(t, conn, 3, MethodUnsubscribe, UnsubscribeParams{SubscriptionID: subscribed.SubscriptionID + "0"})
	response = audit07ReadResponse(t, conn, reader)
	if response.Error == nil || response.Error.Code != CodeInvalidParams {
		t.Fatalf("a foreign subscription id: %+v", response)
	}
	if n := hub.Subscribers(); n != 1 {
		t.Fatalf("subscribers after a refused unsubscribe = %d, want 1", n)
	}

	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":4,"method":"unsubscribe","params":"text"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	response = audit07ReadResponse(t, conn, reader)
	if response.Error == nil || response.Error.Code != CodeInvalidParams {
		t.Fatalf("broken params: %+v", response)
	}
}

// TestAudit07_SubscriptionFilterFromReplayPlan checks the live filter end to
// end: events of other chats and gap events without a chat are treated as the
// plan says.
func TestAudit07_SubscriptionFilterFromReplayPlan(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) {
		return ReplayPlan{Filter: EventFilter{ChatIDs: []int64{7}}}, nil
	})
	conn, reader, _ := pipeSubscriptionServer(t, srv)
	sendPipeRequest(t, conn, 1, MethodSubscribe, nil)
	if response := audit07ReadResponse(t, conn, reader); response.Error != nil {
		t.Fatalf("subscribe: %+v", response)
	}
	chat := func(id int64) *int64 { return &id }
	hub.Publish(Event{ID: "1.0.0.0", Type: EventMessageNew, ChatID: chat(8)})
	hub.Publish(Event{ID: "2.0.0.0", Type: EventMessageNew})
	hub.Publish(Event{ID: "3.0.0.0", Type: EventGap})
	hub.Publish(Event{ID: "4.0.0.0", Type: EventMessageNew, ChatID: chat(7)})
	for _, want := range []string{"3.0.0.0", "4.0.0.0"} {
		if event := readEvent(t, conn, reader); event.ID != want {
			t.Fatalf("event %s, want %s: the filter let a foreign chat through or dropped a gap", event.ID, want)
		}
	}
}

func TestAudit07_HubFilters(t *testing.T) {
	chat := func(id int64) *int64 { return &id }
	data := func(topic int) json.RawMessage {
		return json.RawMessage(`{"topic_id":` + string(rune('0'+topic)) + `}`)
	}
	hub := NewHub()
	byChat := hub.Subscribe()
	byChat.SetFilter(EventFilter{ChatIDs: []int64{7}})
	byTopic := hub.Subscribe()
	byTopic.SetFilter(EventFilter{Topic: 3})
	byType := hub.Subscribe()
	byType.SetFilter(EventFilter{Types: []string{EventMessageEdit}})
	t.Cleanup(func() { byChat.Close(); byTopic.Close(); byType.Close() })

	for _, event := range []Event{
		{ID: "1.0.0.0", Type: EventMessageNew, ChatID: chat(7), Data: data(3)},
		{ID: "2.0.0.0", Type: EventMessageNew, ChatID: chat(8), Data: data(4)},
		{ID: "3.0.0.0", Type: EventMessageEdit, ChatID: chat(7), Data: data(3)},
		{ID: "4.0.0.0", Type: EventTyping},
		{ID: "5.0.0.0", Type: EventGap},
	} {
		hub.Publish(event)
	}
	ids := func(sub *Subscription) []string {
		var out []string
		for {
			select {
			case event := <-sub.Events():
				out = append(out, event.ID)
			default:
				return out
			}
		}
	}
	// A gap without a chat passes every filter.
	for name, tc := range map[string]struct {
		sub  *Subscription
		want []string
	}{
		"chat":  {byChat, []string{"1.0.0.0", "3.0.0.0", "5.0.0.0"}},
		"topic": {byTopic, []string{"1.0.0.0", "3.0.0.0", "5.0.0.0"}},
		"type":  {byType, []string{"3.0.0.0", "5.0.0.0"}},
	} {
		if got := ids(tc.sub); !slices.Equal(got, tc.want) {
			t.Errorf("%s filter delivered %v, want %v", name, got, tc.want)
		}
	}
}

func TestAudit07_MatchesTopic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event Event
		topic int
		want  bool
	}{
		{"no topic keeps everything", Event{Type: EventTyping}, 0, true},
		{"new message in the topic", Event{Type: EventMessageNew, Data: json.RawMessage(`{"topic_id":3}`)}, 3, true},
		{"edited message in the topic", Event{Type: EventMessageEdit, Data: json.RawMessage(`{"topic_id":3}`)}, 3, true},
		{"another topic", Event{Type: EventMessageNew, Data: json.RawMessage(`{"topic_id":4}`)}, 3, false},
		{"no topic in the data", Event{Type: EventMessageNew, Data: json.RawMessage(`{}`)}, 3, false},
		{"not a message event", Event{Type: EventTyping, Data: json.RawMessage(`{"topic_id":3}`)}, 3, false},
		{"broken data", Event{Type: EventMessageNew, Data: json.RawMessage(`{`)}, 3, false},
	} {
		if got := MatchesTopic(tc.event, tc.topic); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAudit07_ClientCancelWhileWaitingForAnswer(t *testing.T) {
	srv := NewServer(nil)
	started, release := make(chan struct{}), make(chan struct{})
	srv.Handle("slow", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "late", nil
	})
	srv.Handle("quick", func(context.Context, json.RawMessage) (any, error) { return "fast", nil })
	c := pipeClient(t, srv)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	callErr := make(chan error, 1)
	go func() { callErr <- c.Call(ctx, "slow", nil, nil) }()
	audit07Wait(t, started, "the handler to start")
	cancel()
	select {
	case err := <-callErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Call = %v, want context.Canceled", err)
		}
	case <-time.After(audit07WaitLimit):
		t.Fatal("Call ignored the canceled context")
	}
	c.mu.Lock()
	left := len(c.pending)
	c.mu.Unlock()
	if left != 0 {
		t.Errorf("%d pending calls left after the cancel", left)
	}
	var got string
	if err := c.Call(ctxTimeout(t), "quick", nil, &got); err != nil || got != "fast" {
		t.Fatalf("the client is unusable after a cancel: %q %v", got, err)
	}
}

func TestAudit07_ClientNotifyGetsNoAnswer(t *testing.T) {
	srv := NewServer(nil)
	seen := make(chan string, 1)
	srv.Handle("note", func(_ context.Context, params json.RawMessage) (any, error) {
		seen <- string(params)
		return "ignored", nil
	})
	srv.Handle("echo", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	c := pipeClient(t, srv)
	if err := c.Notify("note", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seen:
		if got != `{"n":1}` {
			t.Fatalf("handler params = %s", got)
		}
	case <-time.After(audit07WaitLimit):
		t.Fatal("the notification never reached the handler")
	}
	var got string
	if err := c.Call(ctxTimeout(t), "echo", nil, &got); err != nil || got != "pong" {
		t.Fatalf("Call after Notify: %q %v", got, err)
	}
}
