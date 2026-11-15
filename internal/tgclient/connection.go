package tgclient

import (
	"context"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
)

type connectionState struct {
	mu           sync.Mutex
	info         rpc.ConnectionInfo
	changed      chan struct{}
	notify       func(rpc.ConnectionInfo)
	catchup      chan struct{}
	started      bool
	disconnected bool
	hadReady     bool
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
		if s.disconnected && s.started {
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
func (c *Client) runCatchup(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.connection.catchup:
			_ = c.updates.Handle(ctx, &tg.UpdatesTooLong{})
		}
	}
}
