package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func subscriptionPair(t *testing.T, replay ReplayFunc) (*Client, *Hub, *Server) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	hub := NewHub()
	server := NewServer(nil)
	server.SetEventSource(hub, replay)
	done := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(done) }()
	client := NewClient(clientSide)
	t.Cleanup(func() { client.Close(); <-done; server.Close() })
	return client, hub, server
}

func TestClientSubscribeOrderAndCancellation(t *testing.T) {
	client, hub, _ := subscriptionPair(t, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{Cursor: "0.0.0.0"}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Event, 2)
	done := make(chan error, 1)
	go func() {
		_, err := client.SubscribeWithStart(ctx, SubscribeParams{}, func(SubscribeResult) {
			hub.Publish(Event{ID: "1.0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"})
			hub.Publish(Event{ID: "2.0.0.0", Type: EventMessageEdit, Time: "2026-11-14T00:00:00Z"})
		}, func(event Event) error { got <- event; return nil })
		done <- err
	}()
	for _, want := range []string{"1.0.0.0", "2.0.0.0"} {
		select {
		case event := <-got:
			if event.ID != want {
				t.Fatalf("event %s, want %s", event.ID, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event missing")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not stop")
	}
}

func TestClientSubscribeLaggedAndHandlerError(t *testing.T) {
	for _, lagged := range []bool{false, true} {
		t.Run(map[bool]string{false: "handler", true: "lagged"}[lagged], func(t *testing.T) {
			client, hub, _ := subscriptionPair(t, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{Cursor: "0.0.0.0"}, nil })
			expected := errors.New("stop")
			_, err := client.SubscribeWithStart(context.Background(), SubscribeParams{}, func(SubscribeResult) {
				typ := EventMessageNew
				if lagged {
					typ = EventLagged
				}
				hub.Publish(Event{ID: "1.0.0.0", Type: typ, Time: "2026-11-14T00:00:00Z"})
			}, func(Event) error { return expected })
			if lagged {
				var got *LaggedError
				if !errors.As(err, &got) || got.Cursor != "1.0.0.0" {
					t.Fatalf("lagged: %v", err)
				}
			} else if !errors.Is(err, expected) {
				t.Fatalf("handler: %v", err)
			}
		})
	}
}

func TestClientSubscribeClosedConnection(t *testing.T) {
	client, _, server := subscriptionPair(t, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{Cursor: "0.0.0.0"}, nil })
	_, err := client.SubscribeWithStart(context.Background(), SubscribeParams{}, func(SubscribeResult) { server.Close() }, func(Event) error { return nil })
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}

func TestSubscribedClientDoesNotAffectOtherCalls(t *testing.T) {
	stream, _, server := subscriptionPair(t, func(context.Context, SubscribeParams) (ReplayPlan, error) {
		return ReplayPlan{Cursor: "0.0.0.0"}, nil
	})
	server.Handle(MethodPing, func(context.Context, json.RawMessage) (any, error) { return PingResult{Pong: true}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	streamDone := make(chan error, 1)
	go func() {
		_, err := stream.SubscribeWithStart(ctx, SubscribeParams{}, func(SubscribeResult) { close(started) }, func(Event) error { return nil })
		streamDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not start")
	}
	serverSide, clientSide := net.Pipe()
	serveDone := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(serveDone) }()
	other := NewClient(clientSide)
	defer func() { other.Close(); <-serveDone }()
	callCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	var result PingResult
	if err := other.Call(callCtx, MethodPing, nil, &result); err != nil || !result.Pong {
		t.Fatalf("other client call: %+v %v", result, err)
	}
	cancel()
	select {
	case err := <-streamDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop")
	}
}
