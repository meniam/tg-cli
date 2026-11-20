package rpc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestMethodTimeoutOverridesOnlySelectedHandler(t *testing.T) {
	srv := NewServer(nil)
	deadline := func(ctx context.Context, _ json.RawMessage) (any, error) {
		until, ok := ctx.Deadline()
		if !ok {
			return nil, NewError(CodeInternal, "deadline missing")
		}
		return int(time.Until(until).Seconds()), nil
	}
	srv.Handle("ordinary", deadline)
	srv.Handle("media", deadline)
	srv.SetMethodTimeout("media", 600*time.Second)
	for _, tc := range []struct {
		method string
		want   int
	}{
		{"ordinary", 120},
		{"media", 600},
	} {
		response := srv.call(context.Background(), Request{JSONRPC: Version, ID: json.RawMessage("1"), Method: tc.method})
		if response.Error != nil {
			t.Fatalf("%s: %+v", tc.method, response.Error)
		}
		var remaining int
		if err := json.Unmarshal(response.Result, &remaining); err != nil || remaining < tc.want-2 || remaining > tc.want {
			t.Fatalf("%s timeout = %d, err=%v", tc.method, remaining, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.Handle("canceled", func(ctx context.Context, _ json.RawMessage) (any, error) { return nil, ctx.Err() })
	response := srv.call(ctx, Request{JSONRPC: Version, ID: json.RawMessage("2"), Method: "canceled"})
	if response.Error == nil || response.Error.Message != "request canceled" {
		t.Fatalf("caller cancellation: %+v", response.Error)
	}
}

func TestMethodTimeoutConnectionCloseCancelsHandler(t *testing.T) {
	srv := NewServer(nil)
	entered := make(chan struct{})
	canceled := make(chan struct{})
	srv.Handle("media", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	srv.SetMethodTimeout("media", 600*time.Second)
	clientSide, serverSide := net.Pipe()
	client := NewClient(clientSide)
	serverDone := make(chan struct{})
	go func() { srv.ServeConn(serverSide); close(serverDone) }()
	callDone := make(chan error, 1)
	go func() { callDone <- client.Call(context.Background(), "media", struct{}{}, nil) }()
	audit07Wait(t, entered, "the handler to start")
	_ = client.Close()
	audit07Wait(t, canceled, "the connection close to cancel the handler")
	audit07Wait(t, serverDone, "the server to stop")
	select {
	case <-callDone:
	case <-time.After(audit07WaitLimit):
		t.Fatal("the client call did not end after the close")
	}
}
