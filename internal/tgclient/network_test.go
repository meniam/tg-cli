package tgclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"
	"tg-cli/internal/netconf"
)

type proxyRecorder struct {
	mu          sync.Mutex
	dials       []string
	targets     []string
	credentials []string
}

func (r *proxyRecorder) Dial(network, addr string) (net.Conn, error) {
	return r.DialContext(context.Background(), network, addr)
}
func (r *proxyRecorder) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.dials = append(r.dials, addr)
	r.mu.Unlock()
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		b := make([]byte, 2)
		if _, err := io.ReadFull(server, b); err != nil {
			return
		}
		methods := make([]byte, int(b[1]))
		if _, err := io.ReadFull(server, methods); err != nil {
			return
		}
		if _, err := server.Write([]byte{5, 2}); err != nil {
			return
		}
		if _, err := io.ReadFull(server, b); err != nil {
			return
		}
		u := make([]byte, int(b[1]))
		if _, err := io.ReadFull(server, u); err != nil {
			return
		}
		if _, err := io.ReadFull(server, b[:1]); err != nil {
			return
		}
		p := make([]byte, int(b[0]))
		if _, err := io.ReadFull(server, p); err != nil {
			return
		}
		r.mu.Lock()
		r.credentials = append(r.credentials, string(u)+":"+string(p))
		r.mu.Unlock()
		if _, err := server.Write([]byte{1, 0}); err != nil {
			return
		}
		header := make([]byte, 4)
		if _, err := io.ReadFull(server, header); err != nil {
			return
		}
		var host string
		switch header[3] {
		case 1:
			ip := make([]byte, 4)
			if _, err := io.ReadFull(server, ip); err != nil {
				return
			}
			host = net.IP(ip).String()
		case 3:
			if _, err := io.ReadFull(server, b[:1]); err != nil {
				return
			}
			name := make([]byte, int(b[0]))
			if _, err := io.ReadFull(server, name); err != nil {
				return
			}
			host = string(name)
		default:
			return
		}
		if _, err := io.ReadFull(server, b); err != nil {
			return
		}
		port := int(b[0])<<8 | int(b[1])
		r.mu.Lock()
		r.targets = append(r.targets, net.JoinHostPort(host, strconv.Itoa(port)))
		r.mu.Unlock()
		_, _ = server.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
	}()
	return client, nil
}
func TestProxyResolverNeverDialsDestination(t *testing.T) {
	for _, transport := range []netconf.Transport{netconf.TCP, netconf.WSS} {
		t.Run(string(transport), func(t *testing.T) {
			p, err := netconf.ParseProxy("socks5://alice:password@proxy.test:1080")
			if err != nil {
				t.Fatal(err)
			}
			rec := &proxyRecorder{}
			resolver, err := resolverForDial(netconf.Settings{Transport: transport, Proxy: p}, rec)
			if err != nil {
				t.Fatal(err)
			}
			for dc := 1; dc <= 5; dc++ {
				host := fmt.Sprintf("dc%d.test", dc)
				list := dcs.List{Options: []tg.DCOption{{ID: dc, IPAddress: fmt.Sprintf("192.0.2.%d", dc), Port: 443}}, Domains: map[int]string{dc: "wss://" + host + "/apiws"}}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_, err := resolver.Primary(ctx, dc, list)
				cancel()
				if err == nil {
					t.Fatal("expected proxy failure")
				}
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if len(rec.dials) != 5 || len(rec.targets) != 5 {
				t.Fatalf("dials=%v targets=%v", rec.dials, rec.targets)
			}
			for i, addr := range rec.dials {
				if addr != "proxy.test:1080" {
					t.Fatalf("direct dial: %s", addr)
				}
				if rec.credentials[i] != "alice:password" {
					t.Fatal("credentials did not reach proxy")
				}
				switch transport {
				case netconf.WSS:
					if want := fmt.Sprintf("dc%d.test:", i+1); !strings.HasPrefix(rec.targets[i], want) {
						t.Fatalf("domain not sent to proxy: %s, want prefix %s", rec.targets[i], want)
					}
				case netconf.TCP:
					if want := fmt.Sprintf("192.0.2.%d:443", i+1); rec.targets[i] != want {
						t.Fatalf("target %s, want %s", rec.targets[i], want)
					}
				}
			}
		})
	}
}

type failingProxyDialer struct{ addresses []string }

func (d *failingProxyDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}
func (d *failingProxyDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	return nil, errors.New("proxy unavailable")
}
func TestMTProxyResolverDialsOnlyProxy(t *testing.T) {
	p, err := netconf.ParseProxy("tg://proxy?server=proxy.test&port=443&secret=dddddddddddddddddddddddddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	d := &failingProxyDialer{}
	resolver, err := resolverForDial(netconf.Settings{Transport: netconf.TCP, Proxy: p}, d)
	if err != nil {
		t.Fatal(err)
	}
	for dc := 1; dc <= 5; dc++ {
		for _, call := range []func(context.Context, int, dcs.List) (transport.Conn, error){resolver.Primary, resolver.MediaOnly, resolver.CDN} {
			_, err = call(context.Background(), dc, dcs.List{})
			if err == nil {
				t.Fatal("expected proxy failure")
			}
		}
	}
	if len(d.addresses) != 15 {
		t.Fatalf("dials: %v", d.addresses)
	}
	for _, address := range d.addresses {
		if address != "proxy.test:443" {
			t.Fatalf("direct dial: %s", address)
		}
	}
}

type httpProbeDialer struct {
	addresses chan string
	firstByte chan byte
}

func (d *httpProbeDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}
func (d *httpProbeDialer) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	d.addresses <- address
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		var first [1]byte
		if _, err := io.ReadFull(server, first[:]); err == nil {
			d.firstByte <- first[0]
		}
	}()
	return client, nil
}
func TestWSSIgnoresProxyEnvironmentAndUsesTLS(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://must-not-be-used.invalid:3128")
	t.Setenv("HTTP_PROXY", "http://must-not-be-used.invalid:3128")
	t.Setenv("ALL_PROXY", "socks5://must-not-be-used.invalid:1080")
	d := &httpProbeDialer{addresses: make(chan string, 1), firstByte: make(chan byte, 1)}
	resolver, err := resolverForDial(netconf.Settings{Transport: netconf.WSS}, d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = resolver.Primary(ctx, 2, dcs.List{Domains: map[int]string{2: "wss://telegram.example/apiws"}})
	if err == nil {
		t.Fatal("expected incomplete TLS handshake to fail")
	}
	select {
	case addr := <-d.addresses:
		if addr != "telegram.example:443" {
			t.Fatalf("proxy or incorrect address used: %s", addr)
		}
	default:
		t.Fatal("no connection attempt")
	}
	select {
	case b := <-d.firstByte:
		if b != 0x16 {
			t.Fatalf("expected TLS handshake, got %#x", b)
		}
	default:
		t.Fatal("no TLS handshake")
	}
}

func TestSOCKSMediaAndCDNStayBehindProxy(t *testing.T) {
	p, err := netconf.ParseProxy("socks5://alice:password@proxy.test:1080")
	if err != nil {
		t.Fatal(err)
	}
	rec := &proxyRecorder{}
	resolver, err := resolverForDial(netconf.Settings{Transport: netconf.TCP, Proxy: p}, rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		call   func(context.Context, int, dcs.List) (transport.Conn, error)
		option tg.DCOption
	}{{"media", resolver.MediaOnly, tg.DCOption{ID: 2, IPAddress: "192.0.2.2", Port: 443, MediaOnly: true}}, {"cdn", resolver.CDN, tg.DCOption{ID: 2, IPAddress: "192.0.2.3", Port: 443, CDN: true}}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.call(context.Background(), 2, dcs.List{Options: []tg.DCOption{tc.option}})
			if err == nil {
				t.Fatal("expected proxy failure")
			}
		})
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.dials) != 2 {
		t.Fatalf("dials: %v", rec.dials)
	}
	for _, addr := range rec.dials {
		if addr != "proxy.test:1080" {
			t.Fatalf("direct dial: %s", addr)
		}
	}
}

// queuedWriter gives the in-memory TLS server the buffering a TCP socket has.
type queuedWriter struct {
	net.Conn
	writes chan []byte
}

func newQueuedWriter(conn net.Conn) *queuedWriter {
	w := &queuedWriter{Conn: conn, writes: make(chan []byte, 32)}
	go func() {
		for data := range w.writes {
			if _, err := w.Conn.Write(data); err != nil {
				return
			}
		}
	}()
	return w
}
func (w *queuedWriter) Write(data []byte) (int, error) {
	copyData := append([]byte(nil), data...)
	w.writes <- copyData
	return len(data), nil
}

type testTLSDialer struct{ config *tls.Config }

func (d testTLSDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}
func (d testTLSDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(2 * time.Second))
		writer := newQueuedWriter(server)
		defer close(writer.writes)
		_ = tls.Server(writer, d.config).Handshake()
	}()
	return client, nil
}
func TestWSSRejectsUntrustedCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"telegram.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dialer := testTLSDialer{config: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}}
	resolver, err := resolverForDial(netconf.Settings{Transport: netconf.WSS}, dialer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = resolver.Primary(ctx, 2, dcs.List{Domains: map[int]string{2: "wss://telegram.example/apiws"}})
	var target *tls.CertificateVerificationError
	if !errors.As(err, &target) {
		t.Fatalf("expected an unknown certificate authority, got: %v", err)
	}
}

func TestNetworkStatusNeverContainsProxyCredentials(t *testing.T) {
	for _, value := range []string{"socks5://user:secret-marker@proxy.test:1080", "tg://proxy?server=proxy.test&port=443&secret=dddddddddddddddddddddddddddddddddd"} {
		p, err := netconf.ParseProxy(value)
		if err != nil {
			t.Fatal(err)
		}
		c := newConnection(netconf.Settings{Transport: netconf.TCP, Proxy: p})
		data, err := json.Marshal(c.info)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"user", "secret-marker", "dddddddd"} {
			if strings.Contains(string(data), secret) {
				t.Fatal("proxy secret was exposed in connection status")
			}
		}
	}
}
