package tgclient

import (
	"context"
	"errors"
	"github.com/gotd/td/telegram"
	"testing"
	"tg-cli/internal/netconf"
	"time"
)

func TestConnectionTransitions(t *testing.T) {
	s := newConnection(netconf.Settings{})
	s.update(telegram.ConnectionStateEvent{State: telegram.ConnectionStateReady, DC: 2})
	if s.info.ActiveTransport != "tcp" {
		t.Fatalf("active transport: %+v", s.info)
	}
	select {
	case <-s.catchup:
		t.Fatal("first connection triggered catch-up")
	default:
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	s.update(telegram.ConnectionStateEvent{State: telegram.ConnectionStateDisconnected, DC: 2})
	s.update(telegram.ConnectionStateEvent{State: telegram.ConnectionStateConnecting, DC: 2})
	s.update(telegram.ConnectionStateEvent{State: telegram.ConnectionStateReady, DC: 4})
	s.update(telegram.ConnectionStateEvent{State: telegram.ConnectionStateReady, DC: 4})
	if s.info.Reconnects != 1 || s.info.DC != 4 {
		t.Fatalf("wrong state: %+v", s.info)
	}
	select {
	case <-s.catchup:
	default:
		t.Fatal("missing catch-up")
	}
	select {
	case <-s.catchup:
		t.Fatal("duplicate catch-up")
	default:
	}
}
func TestConnectionWaitCancellation(t *testing.T) {
	c := &Client{connection: newConnection(netconf.Settings{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.WaitConnected(ctx, time.Minute) != context.Canceled {
		t.Fatal("did not honor cancellation")
	}
}
func TestAudit09_ConnectionWaitReady(t *testing.T) {
	ready := telegram.ConnectionStateEvent{State: telegram.ConnectionStateReady, DC: 2}
	t.Run("already ready", func(t *testing.T) {
		c := &Client{connection: newConnection(netconf.Settings{})}
		c.connection.update(ready)
		if err := c.WaitConnected(context.Background(), time.Minute); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("becomes ready", func(t *testing.T) {
		c := &Client{connection: newConnection(netconf.Settings{})}
		done := make(chan error, 1)
		go func() { done <- c.WaitConnected(context.Background(), time.Minute) }()
		// The waiter may not have reached the wait yet; a ready update is seen either way.
		c.connection.update(ready)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("WaitConnected did not wake up on ready")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		c := &Client{connection: newConnection(netconf.Settings{})}
		if err := c.WaitConnected(context.Background(), time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error %v, want deadline exceeded", err)
		}
	})
}
func TestNetworkResolver(t *testing.T) {
	r, err := resolverFor(netconf.Settings{})
	if err != nil || r != nil {
		t.Fatalf("default changed: %v %v", r, err)
	}
	for _, tr := range []netconf.Transport{netconf.TCP, netconf.WSS} {
		p, err := netconf.ParseProxy("socks5://u:p@localhost:1080")
		if err != nil {
			t.Fatal(err)
		}
		r, err := resolverFor(netconf.Settings{Transport: tr, Proxy: p})
		if err != nil || r == nil {
			t.Fatalf("missing resolver %s: %v", tr, err)
		}
	}
}
