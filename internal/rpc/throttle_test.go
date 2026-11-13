package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/throttle"
)

type waitClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*waitTimer]struct{}
}

type waitTimer struct {
	clock    *waitClock
	deadline time.Time
	ch       chan time.Time
}

func newWaitClock() *waitClock {
	return &waitClock{now: time.Unix(0, 0), timers: make(map[*waitTimer]struct{})}
}

func (c *waitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *waitClock) NewTimer(deadline time.Time) throttle.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &waitTimer{clock: c, deadline: deadline, ch: make(chan time.Time, 1)}
	if !deadline.After(c.now) {
		t.ch <- c.now
	} else {
		c.timers[t] = struct{}{}
	}
	return t
}

func (t *waitTimer) C() <-chan time.Time { return t.ch }
func (t *waitTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	return active
}

func (c *waitClock) advance(d time.Duration) {
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

func (c *waitClock) awaitTimer(t *testing.T) {
	t.Helper()
	until := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		found := len(c.timers) > 0
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-until:
			t.Fatal("wait timer was not scheduled")
		default:
			runtime.Gosched()
		}
	}
}

func TestThrottleResponseWithoutWaitKeepsBytes(t *testing.T) {
	s := NewServer(nil)
	s.Handle("read", func(context.Context, json.RawMessage) (any, error) { return map[string]int{"id": 7}, nil })
	resp := s.call(context.Background(), Request{JSONRPC: Version, ID: json.RawMessage("1"), Method: "read"})
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"jsonrpc":"2.0","id":1,"result":{"id":7}}`; string(data) != want {
		t.Fatalf("response = %s, want %s", data, want)
	}
}

func TestThrottleResponseOnSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fail   bool
		pause  time.Duration
		wantMS int64
	}{
		{"success", false, 2 * time.Second, 2000},
		{"failure", true, 2 * time.Second, 2000},
		{"sub-millisecond", false, 100 * time.Microsecond, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newWaitClock()
			limiter, err := throttle.New(throttle.DefaultConfig(), clock)
			if err != nil {
				t.Fatal(err)
			}
			limiter.Pause(tc.pause)
			s := NewServer(nil)
			s.Handle("read", func(ctx context.Context, _ json.RawMessage) (any, error) {
				permit, err := limiter.Acquire(ctx, throttle.General, "", throttle.WaitFromContext(ctx))
				if err != nil {
					return nil, err
				}
				permit.Release()
				if tc.fail {
					return nil, NewError(CodeFloodWait, "wait")
				}
				return map[string]int{"id": 7}, nil
			})
			result := make(chan Response, 1)
			go func() {
				result <- s.call(context.Background(), Request{JSONRPC: Version, ID: json.RawMessage("1"), Method: "read"})
			}()
			clock.awaitTimer(t)
			clock.advance(tc.pause)
			select {
			case resp := <-result:
				if resp.ThrottledMS != tc.wantMS {
					t.Fatalf("wait = %d ms", resp.ThrottledMS)
				}
				if tc.fail && (resp.Error == nil || resp.Error.Code != CodeFloodWait) {
					t.Fatalf("error = %+v", resp.Error)
				}
				if !tc.fail && resp.Error != nil {
					t.Fatal(resp.Error)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("server call did not finish")
			}
		})
	}
}

func TestThrottleClientReceivesMetadata(t *testing.T) {
	for _, fail := range []bool{false, true} {
		clientConn, serverConn := net.Pipe()
		client := NewClient(clientConn)
		response := Response{JSONRPC: Version, ID: json.RawMessage("1"), Result: json.RawMessage(`{"id":7}`), ThrottledMS: 25}
		if fail {
			response.Result = nil
			response.Error = NewError(CodeFloodWait, "wait")
		}
		served := make(chan error, 1)
		go func() {
			defer serverConn.Close()
			if _, err := newFrameReader(serverConn).next(); err != nil {
				served <- err
				return
			}
			served <- (&frameWriter{w: serverConn}).write(response)
		}()
		var result struct {
			ID int `json:"id"`
		}
		milliseconds, err := client.CallWithThrottle(context.Background(), "read", nil, &result)
		if milliseconds != 25 {
			t.Errorf("wait = %d ms", milliseconds)
		}
		if fail {
			var rpcErr *Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != CodeFloodWait {
				t.Errorf("error = %v", err)
			}
		} else if err != nil || result.ID != 7 {
			t.Errorf("result = %+v, error = %v", result, err)
		}
		if serveErr := <-served; serveErr != nil {
			t.Error(serveErr)
		}
		client.Close()
	}
}
