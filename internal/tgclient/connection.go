package tgclient

import (
	"context"
	"crypto/rand"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.opentelemetry.io/otel/trace"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
)

type connectionState struct {
	mu             sync.Mutex
	info           rpc.ConnectionInfo
	changed        chan struct{}
	notify         func(rpc.ConnectionInfo)
	catchup        chan struct{}
	started        bool
	disconnected   bool
	hadReady       bool
	catchupPending bool
	barriers       map[*tg.UpdateUserStatus]barrierWaiter
}

func newConnection(s netconf.Settings) *connectionState {
	transport := string(s.Transport)
	if transport == "" {
		transport = "tcp"
	}
	if s.Proxy != nil && s.Proxy.Kind == netconf.MTProxy {
		transport = "mtproxy"
	}
	return &connectionState{info: rpc.ConnectionInfo{Transport: transport, Proxy: s.Proxy.Redacted(), Connection: "connecting"}, changed: make(chan struct{}), catchup: make(chan struct{}, 1)}
}
func (s *connectionState) update(e telegram.ConnectionStateEvent) {
	s.mu.Lock()
	old := s.info.Connection
	state := e.State.String()
	s.info.Connection = state
	if state == "ready" {
		s.info.DC = e.DC
		s.hadReady = true
		if s.disconnected && s.started && !s.catchupPending {
			s.catchupPending = true
			select {
			case s.catchup <- struct{}{}:
			default:
			}
		}
		s.disconnected = false
	}
	if state == "disconnected" && old == "ready" && s.hadReady {
		s.info.Reconnects++
		s.disconnected = true
	}
	close(s.changed)
	s.changed = make(chan struct{})
	info := s.info
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(info)
	}
}
func (c *Client) Connection() rpc.ConnectionInfo {
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	return c.connection.info
}
func (c *Client) SetConnectionSink(f func(rpc.ConnectionInfo)) {
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	c.connection.notify = f
}
func (c *Client) WaitConnected(ctx context.Context, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	for {
		c.connection.mu.Lock()
		if c.connection.info.Connection == "ready" {
			c.connection.mu.Unlock()
			return nil
		}
		changed := c.connection.changed
		c.connection.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// A marker passes through the manager after every earlier queued update.
// Its pointer is local-only and is consumed before the event dispatcher.
type barrierWaiter struct {
	done  chan bool
	trace trace.TraceID
}
type catchupHandler struct {
	client *Client
	next   telegram.UpdateHandler
}

func (h catchupHandler) Handle(ctx context.Context, update tg.UpdatesClass) error {
	s := h.client.connection
	var entries []tg.UpdateClass
	switch v := update.(type) {
	case *tg.UpdateShort:
		entries = []tg.UpdateClass{v.Update}
	case *tg.Updates:
		entries = v.Updates
	case *tg.UpdatesCombined:
		entries = v.Updates
	}
	for _, entry := range entries {
		if marker, ok := entry.(*tg.UpdateUserStatus); ok {
			s.mu.Lock()
			w, found := s.barriers[marker]
			s.mu.Unlock()
			if found {
				select {
				case w.done <- trace.SpanContextFromContext(ctx).TraceID() == w.trace:
				default:
				}
				return nil
			}
		}
	}
	return h.next.Handle(ctx, update)
}

// waitBarrier proves that a marker reached the manager's state queue. During
// initial replay gotd drops the caller's trace context and may drop buffered
// updates, so a probe is retried until it arrives with its trace intact.
func (c *Client) waitBarrier(ctx context.Context, probe bool) error {
	var id trace.TraceID
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: id, SpanID: trace.SpanID{1}})
	markerCtx := trace.ContextWithSpanContext(ctx, span)
	marker := &tg.UpdateUserStatus{UserID: -1, Status: &tg.UserStatusEmpty{}}
	done := make(chan bool, 1)
	c.connection.mu.Lock()
	if c.connection.barriers == nil {
		c.connection.barriers = make(map[*tg.UpdateUserStatus]barrierWaiter)
	}
	c.connection.barriers[marker] = barrierWaiter{done: done, trace: id}
	c.connection.mu.Unlock()
	send := func() error { return c.updates.Handle(markerCtx, &tg.UpdateShort{Update: marker}) }
	if err := send(); err != nil {
		return err
	}
	if !probe {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return nil
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case valid := <-done:
			if valid {
				return nil
			}
		case <-ticker.C:
			if err := send(); err != nil {
				return err
			}
		}
	}
}
func (c *Client) runCatchup(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.connection.catchup:
			// The first barrier also waits until startup replay has finished. This
			// prevents a synthetic update from being buffered or dropped there.
			err := c.waitBarrier(ctx, true)
			if err == nil {
				err = c.updates.Handle(ctx, &tg.UpdatesTooLong{})
			}
			if err == nil {
				err = c.waitBarrier(ctx, false)
			}
			c.connection.mu.Lock()
			c.connection.barriers = nil
			c.connection.catchupPending = false
			c.connection.mu.Unlock()
			if ctx.Err() != nil {
				return
			}
		}
	}
}
