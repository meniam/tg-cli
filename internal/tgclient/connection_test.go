package tgclient

import (
	"context"
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
