package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func pipeClient(t *testing.T, s *Server) *Client {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() { s.ServeConn(b); close(done) }()
	c := NewClient(a)
	t.Cleanup(func() { _ = c.Close(); <-done })
	return c
}

func TestConnLifecycleLimitsAndCancellation(t *testing.T) {
	s := NewServer(nil)
	started := make(chan struct{}, 16)
	canceled := make(chan struct{}, 16)
	release := make(chan struct{})
	s.Handle("hold", func(ctx context.Context, _ json.RawMessage) (any, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			canceled <- struct{}{}
			return nil, ctx.Err()
		case <-release:
			return true, nil
		}
	})
	s.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return true, nil })
	first := pipeClient(t, s)
	second := pipeClient(t, s)
	var wg sync.WaitGroup
	hold := func(c *Client) {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Call(context.Background(), "hold", nil, nil) }()
	}
	for range MaxConcurrentPerConn {
		hold(first)
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("handler did not start")
		}
	}
	var rpcErr *Error
	err := first.Call(ctxTimeout(t), "ping", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeBusy {
		t.Fatalf("ninth request: %v", err)
	}
	if err := second.Call(ctxTimeout(t), "ping", nil, nil); err != nil {
		t.Fatalf("other connection: %v", err)
	}
	for range MaxConcurrentPerConn {
		hold(second)
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("second connection did not start")
		}
	}
	third := pipeClient(t, s)
	err = third.Call(ctxTimeout(t), "ping", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeBusy {
		t.Fatalf("global limit: %v", err)
	}
	_ = first.Close()
	for range MaxConcurrentPerConn {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("handler not canceled")
		}
	}
	deadline := time.Now().Add(time.Second)
	for s.Active() != MaxConcurrentPerConn {
		if time.Now().After(deadline) {
			t.Fatal("closed connection retained active requests")
		}
		time.Sleep(time.Millisecond)
	}
	if err := third.Call(ctxTimeout(t), "ping", nil, nil); err != nil {
		t.Fatalf("third connection after close: %v", err)
	}
	select {
	case <-canceled:
		t.Fatal("other connection canceled")
	default:
	}
	close(release)
	wg.Wait()
}

func TestConnLifecycleHalfClose(t *testing.T) {
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := NewServer(nil)
	defer s.Close()
	started, canceled := make(chan struct{}), make(chan struct{})
	s.Handle("hold", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	go func() { _ = s.Serve(l) }()
	c, err := net.DialUnix("unix", nil, l.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"hold\"}\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler not started")
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("handler not canceled")
	}
}
