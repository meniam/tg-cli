package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServer serves srv on a loopback TCP port and returns the address.
func startServer(t *testing.T, srv *Server) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l.Addr().String()
}

func dial(t *testing.T, addr string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func ctxTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func echoServer() *Server {
	srv := NewServer(nil)
	srv.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var v map[string]any
		if err := json.Unmarshal(params, &v); err != nil {
			return nil, NewError(CodeInvalidParams, "bad params")
		}
		return v, nil
	})
	return srv
}

func TestMessageShapes(t *testing.T) {
	req, _ := json.Marshal(Request{JSONRPC: Version, ID: json.RawMessage("7"), Method: "m", Params: json.RawMessage(`{"a":1}`)})
	if got, want := string(req), `{"jsonrpc":"2.0","id":7,"method":"m","params":{"a":1}}`; got != want {
		t.Errorf("request: got %s, want %s", got, want)
	}
	note, _ := json.Marshal(Request{JSONRPC: Version, Method: "n"})
	if got, want := string(note), `{"jsonrpc":"2.0","method":"n"}`; got != want {
		t.Errorf("notification: got %s, want %s", got, want)
	}
	if !(Request{}).IsNotification() {
		t.Error("request without id must be a notification")
	}
	errResp, _ := json.Marshal(errorResponse(nil, CodeParseError, "parse error"))
	if got, want := string(errResp), `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`; got != want {
		t.Errorf("error response: got %s, want %s", got, want)
	}
}

func TestCallRoundTrip(t *testing.T) {
	c := dial(t, startServer(t, echoServer()))
	var out map[string]any
	if err := c.Call(ctxTimeout(t), "echo", map[string]any{"x": "y"}, &out); err != nil {
		t.Fatal(err)
	}
	if out["x"] != "y" {
		t.Errorf("unexpected result %v", out)
	}
}

func TestCallContextStopsBlockedWrite(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	c := NewClient(clientConn)
	t.Cleanup(func() {
		_ = c.Close()
		_ = peerConn.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "echo", map[string]string{"x": "y"}, nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked write returned %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call remained blocked after its context expired")
	}
}

func TestParallelCallsKeepTheirIDs(t *testing.T) {
	const n = 8
	srv := NewServer(nil)
	var arrived sync.WaitGroup
	arrived.Add(n)
	release := make(chan struct{})
	srv.Handle("hold", func(_ context.Context, params json.RawMessage) (any, error) {
		arrived.Done()
		<-release // proves n requests run at the same time
		var v struct{ N int }
		_ = json.Unmarshal(params, &v)
		return v, nil
	})
	c := dial(t, startServer(t, srv))

	results := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct{ N int }
			if err := c.Call(ctxTimeout(t), "hold", map[string]int{"N": i}, &out); err != nil {
				t.Error(err)
			}
			results[i] = out.N
		}()
	}
	arrived.Wait()
	close(release)
	wg.Wait()
	for i, got := range results {
		if got != i {
			t.Errorf("call %d got result %d", i, got)
		}
	}
}

// rawConn gives line-level access to a server.
type rawConn struct {
	c net.Conn
	r *bufio.Reader
}

func dialRaw(t *testing.T, addr string) *rawConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &rawConn{c: c, r: bufio.NewReader(c)}
}

func (r *rawConn) send(t *testing.T, line string) {
	t.Helper()
	if _, err := r.c.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func (r *rawConn) read(t *testing.T) Response {
	t.Helper()
	_ = r.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := r.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return resp
}

func TestNotificationGetsNoResponse(t *testing.T) {
	srv := echoServer()
	got := make(chan struct{}, 1)
	srv.Handle("note", func(context.Context, json.RawMessage) (any, error) {
		got <- struct{}{}
		return nil, nil
	})
	rc := dialRaw(t, startServer(t, srv))
	rc.send(t, `{"jsonrpc":"2.0","method":"note"}`)
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("notification handler not called")
	}
	rc.send(t, `{"jsonrpc":"2.0","id":1,"method":"echo","params":{}}`)
	resp := rc.read(t)
	if string(resp.ID) != "1" {
		t.Errorf("first response is for id %s, want 1: the notification must not be answered", resp.ID)
	}
}

func TestProtocolErrors(t *testing.T) {
	rc := dialRaw(t, startServer(t, echoServer()))

	rc.send(t, `{bad json`)
	if r := rc.read(t); r.Error == nil || r.Error.Code != CodeParseError || string(r.ID) != "null" {
		t.Errorf("parse error: %+v", r)
	}
	rc.send(t, `{"jsonrpc":"1.0","id":2,"method":"echo"}`)
	if r := rc.read(t); r.Error == nil || r.Error.Code != CodeInvalidRequest {
		t.Errorf("invalid request: %+v", r)
	}
	rc.send(t, `{"jsonrpc":"2.0","id":3,"method":"nope"}`)
	if r := rc.read(t); r.Error == nil || r.Error.Code != CodeMethodNotFound || string(r.ID) != "3" {
		t.Errorf("method not found: %+v", r)
	}
	rc.send(t, `{"jsonrpc":"2.0","id":4,"method":"echo","params":[1]}`)
	if r := rc.read(t); r.Error == nil || r.Error.Code != CodeInvalidParams {
		t.Errorf("invalid params: %+v", r)
	}
}

func TestOversizedFrameIsRejected(t *testing.T) {
	rc := dialRaw(t, startServer(t, echoServer()))
	go func() {
		// Write errors are expected once the server closes the connection.
		_, _ = rc.c.Write([]byte(strings.Repeat("a", 2*MaxFrameSize) + "\n"))
	}()
	r := rc.read(t)
	if r.Error == nil || r.Error.Code != CodeInvalidRequest {
		t.Fatalf("want invalid request, got %+v", r)
	}
	_ = rc.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := rc.r.ReadByte(); err == nil {
		t.Error("connection must be closed after an oversized frame")
	}
}

func TestClosingConnectionCancelsHandler(t *testing.T) {
	srv := NewServer(nil)
	started := make(chan struct{})
	canceled := make(chan struct{})
	srv.Handle("block", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	c := dial(t, startServer(t, srv))
	go func() { _ = c.Call(context.Background(), "block", nil, nil) }()
	<-started
	_ = c.Close()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler context was not canceled after the connection closed")
	}
}

func TestBusyWhenLimitReached(t *testing.T) {
	srv := NewServer(nil)
	srv.sem = make(chan struct{}, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	srv.Handle("hold", func(context.Context, json.RawMessage) (any, error) {
		close(started)
		<-release
		return nil, nil
	})
	c := dial(t, startServer(t, srv))
	done := make(chan error, 1)
	go func() { done <- c.Call(context.Background(), "hold", nil, nil) }()
	<-started

	var rpcErr *Error
	err := c.Call(ctxTimeout(t), "hold", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeBusy {
		t.Errorf("want busy error, got %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Errorf("first call failed: %v", err)
	}
}

func TestHandlerErrors(t *testing.T) {
	srv := NewServer(nil)
	srv.Handle("typed", func(context.Context, json.RawMessage) (any, error) {
		return nil, &Error{Code: CodeFloodWait, Message: "FLOOD_WAIT", Data: map[string]any{"retry_after_seconds": 7}}
	})
	srv.Handle("plain", func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("secret detail")
	})
	srv.Handle("panic", func(context.Context, json.RawMessage) (any, error) {
		panic("boom")
	})
	c := dial(t, startServer(t, srv))

	var rpcErr *Error
	err := c.Call(ctxTimeout(t), "typed", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeFloodWait {
		t.Fatalf("typed error lost: %v", err)
	}
	if data, _ := rpcErr.Data.(map[string]any); data["retry_after_seconds"] != float64(7) {
		t.Errorf("error data lost: %v", rpcErr.Data)
	}
	for _, method := range []string{"plain", "panic"} {
		err := c.Call(ctxTimeout(t), method, nil, nil)
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternal || rpcErr.Message != "internal error" {
			t.Errorf("%s: want generic internal error, got %v", method, err)
		}
	}
}

func TestRequestTimeout(t *testing.T) {
	srv := NewServer(nil)
	srv.RequestTimeout = 50 * time.Millisecond
	srv.Handle("slow", func(ctx context.Context, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	c := dial(t, startServer(t, srv))
	var rpcErr *Error
	err := c.Call(ctxTimeout(t), "slow", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Message != "request timed out" {
		t.Errorf("want timeout error, got %v", err)
	}
}

func TestShutdownWaitsForInFlightAndClosesIdle(t *testing.T) {
	srv := NewServer(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	srv.Handle("hold", func(context.Context, json.RawMessage) (any, error) {
		close(started)
		<-release
		return map[string]bool{"done": true}, nil
	})
	srv.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	addr := startServer(t, srv)
	busy := dial(t, addr)
	idle := dial(t, addr)
	if err := idle.Call(ctxTimeout(t), "ping", nil, nil); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		var out map[string]bool
		err := busy.Call(context.Background(), "hold", nil, &out)
		if err == nil && !out["done"] {
			err = errors.New("empty result")
		}
		result <- err
	}()
	<-started

	shut := make(chan error, 1)
	go func() { shut <- srv.Shutdown(ctxTimeout(t)) }()

	select {
	case err := <-shut:
		t.Fatalf("Shutdown returned %v while a request was in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := idle.Call(ctxTimeout(t), "ping", nil, nil); err == nil {
		t.Error("idle connection must be closed by Shutdown")
	}
	close(release)
	if err := <-result; err != nil {
		t.Errorf("in-flight request must finish: %v", err)
	}
	if err := <-shut; err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if _, err := Dial(context.Background(), "tcp", addr); err == nil {
		t.Error("listener must be closed after Shutdown")
	}
}

func TestIsNotRunning(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	_, err = Dial(context.Background(), "tcp", addr)
	if err == nil || !IsNotRunning(err) {
		t.Errorf("connection refused must count as not running, got %v", err)
	}
}
