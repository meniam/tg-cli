package tgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"
	"golang.org/x/net/proxy"
	"tg-cli/internal/netconf"
)

type fakeResolver struct {
	calls []int
	err   error
}

func (f *fakeResolver) Primary(_ context.Context, dc int, _ dcs.List) (transport.Conn, error) {
	f.calls = append(f.calls, dc)
	return nil, f.err
}
func (f *fakeResolver) MediaOnly(ctx context.Context, dc int, l dcs.List) (transport.Conn, error) {
	return f.Primary(ctx, dc, l)
}
func (f *fakeResolver) CDN(ctx context.Context, dc int, l dcs.List) (transport.Conn, error) {
	return f.Primary(ctx, dc, l)
}
func TestAutoTransportFallbackAndStickiness(t *testing.T) {
	tcp, ws := &fakeResolver{err: errors.New("failed")}, &fakeResolver{}
	state := newConnection(netconf.Settings{Transport: netconf.Auto})
	r := &autoResolver{tcp: tcp, ws: ws, report: state.activeTransport}
	if _, err := r.Primary(context.Background(), 3, dcs.List{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Primary(context.Background(), 4, dcs.List{}); err != nil {
		t.Fatal(err)
	}
	if len(tcp.calls) != 1 || len(ws.calls) != 2 || ws.calls[0] != 3 || ws.calls[1] != 4 {
		t.Fatalf("tcp=%v ws=%v", tcp.calls, ws.calls)
	}
	if state.info.ActiveTransport != "wss" || state.info.TransportSwitches != 1 {
		t.Fatalf("status: %+v", state.info)
	}
}
func TestAutoTransportDoesNotFallbackAfterCancellation(t *testing.T) {
	tcp, ws := &fakeResolver{err: errors.New("failed")}, &fakeResolver{}
	r := &autoResolver{tcp: tcp, ws: ws}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Primary(ctx, 2, dcs.List{}); !errors.Is(err, context.Canceled) || len(ws.calls) != 0 {
		t.Fatalf("error %v, wss calls %v", err, ws.calls)
	}
}
func TestAutoTransportProxyValidation(t *testing.T) {
	http, _ := netconf.ParseProxy("http://localhost:8080")
	mt, _ := netconf.ParseProxy("tg://proxy?server=localhost&port=443&secret=0123456789abcdef0123456789abcdef")
	if (netconf.Settings{Transport: netconf.Auto, Proxy: http}).Validate() != nil {
		t.Fatal("auto with HTTP rejected")
	}
	if (netconf.Settings{Transport: netconf.Auto, Proxy: mt}).Validate() == nil {
		t.Fatal("auto with MTProxy accepted")
	}
	r, err := resolverForDial(netconf.Settings{Transport: netconf.Auto, Proxy: http}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := r.(*autoResolver); !ok || !a.sticky || a.tcp != nil {
		t.Fatal("HTTP auto did not select WSS")
	}
}

func TestAutoTransportNeverBypassesProxy(t *testing.T) {
	for _, address := range []string{"socks5://proxy.test:1080", "http://proxy.test:8080"} {
		p, err := netconf.ParseProxy(address)
		if err != nil {
			t.Fatal(err)
		}
		dial := &failingProxyDialer{}
		r, err := resolverForDial(netconf.Settings{Transport: netconf.Auto, Proxy: p}, dial)
		if err != nil {
			t.Fatal(err)
		}
		list := dcs.List{Options: []tg.DCOption{{ID: 2, IPAddress: "192.0.2.1", Port: 443}}, Domains: map[int]string{2: "wss://dc2.example/apiws"}}
		if _, err := r.Primary(context.Background(), 2, list); err == nil {
			t.Fatal("expected proxy error")
		}
		if len(dial.addresses) == 0 {
			t.Fatal("proxy not used")
		}
		for _, got := range dial.addresses {
			if got != p.Address() {
				t.Fatalf("bypassed proxy: %s", got)
			}
		}
	}
}
