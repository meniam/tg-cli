package tgclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/coder/websocket"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/transport"
	"golang.org/x/net/proxy"
	"tg-cli/internal/netconf"
)

func resolverFor(s netconf.Settings) (dcs.Resolver, error) {
	return resolverForDial(s, proxy.Direct)
}

func resolverForState(s netconf.Settings, state *connectionState) (dcs.Resolver, error) {
	return resolverForDialState(s, proxy.Direct, state.activeTransport)
}

// resolverForDial uses forward only to reach the selected proxy.
func resolverForDial(s netconf.Settings, forward proxy.Dialer) (dcs.Resolver, error) {
	return resolverForDialState(s, forward, nil)
}
func resolverForDialState(s netconf.Settings, forward proxy.Dialer, report func(string, bool)) (dcs.Resolver, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if s.Transport == netconf.Auto {
		wssSettings := s
		wssSettings.Transport = netconf.WSS
		ws, err := resolverForDialState(wssSettings, forward, nil)
		if err != nil {
			return nil, err
		}
		if s.Proxy != nil && s.Proxy.Kind == netconf.HTTP {
			return &autoResolver{ws: ws, sticky: true, report: report}, nil
		}
		tcpSettings := s
		tcpSettings.Transport = netconf.TCP
		tcp, err := resolverForDialState(tcpSettings, forward, nil)
		if err != nil {
			return nil, err
		}
		if tcp == nil {
			tcp = dcs.DefaultResolver()
		}
		return &autoResolver{tcp: tcp, ws: ws, report: report}, nil
	}
	var dial func(context.Context, string, string) (net.Conn, error)
	if s.Proxy != nil {
		switch s.Proxy.Kind {
		case netconf.MTProxy:
			r, err := dcs.MTProxy(s.Proxy.Address(), s.Proxy.Secret, dcs.MTProxyOptions{Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if d, ok := forward.(proxy.ContextDialer); ok {
					return d.DialContext(ctx, network, address)
				}
				return nil, errors.New("proxy dialer does not support cancellation")
			}})
			if err != nil {
				return nil, errors.New("invalid MTProxy secret")
			}
			return r, nil
		case netconf.SOCKS5:
			var auth *proxy.Auth
			if s.Proxy.User != "" {
				auth = &proxy.Auth{User: s.Proxy.User, Password: s.Proxy.Password}
			}
			d, err := proxy.SOCKS5("tcp", s.Proxy.Address(), auth, forward)
			if err != nil {
				return nil, errors.New("invalid SOCKS5 proxy")
			}
			cd, ok := d.(proxy.ContextDialer)
			if !ok {
				return nil, errors.New("SOCKS5 proxy does not support cancellation")
			}
			dial = cd.DialContext
		case netconf.HTTP:
			// The WSS transport below uses HTTP CONNECT for this proxy.
		default:
			return nil, errors.New("invalid proxy type")
		}
	}
	if s.Transport == netconf.WSS {
		if dial == nil {
			d, ok := forward.(proxy.ContextDialer)
			if !ok {
				return nil, errors.New("dialer does not support cancellation")
			}
			dial = d.DialContext
		}
		tr := &http.Transport{Proxy: nil, DialContext: dial, ForceAttemptHTTP2: false}
		if s.Proxy != nil && s.Proxy.Kind == netconf.HTTP {
			u := &url.URL{Scheme: "http", Host: s.Proxy.Address()}
			if s.Proxy.User != "" {
				u.User = url.UserPassword(s.Proxy.User, s.Proxy.Password)
			}
			tr.Proxy = http.ProxyURL(u)
		}
		return dcs.Websocket(dcs.WebsocketOptions{DialOptions: &websocket.DialOptions{Subprotocols: []string{"binary"}, HTTPClient: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("WebSocket redirects are not allowed") }}}}), nil
	}
	if dial != nil {
		return dcs.Plain(dcs.PlainOptions{Dial: dial}), nil
	}
	return nil, nil
}

type autoResolver struct {
	mu      sync.Mutex
	tcp, ws dcs.Resolver
	sticky  bool
	report  func(string, bool)
}

func (r *autoResolver) dial(ctx context.Context, dc int, list dcs.List, which func(dcs.Resolver, context.Context, int, dcs.List) (transport.Conn, error)) (transport.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sticky {
		conn, err := which(r.ws, ctx, dc, list)
		if err == nil && r.report != nil {
			r.report("wss", false)
		}
		return conn, err
	}
	conn, err := which(r.tcp, ctx, dc, list)
	if err == nil {
		if r.report != nil {
			r.report("tcp", false)
		}
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	conn, err = which(r.ws, ctx, dc, list)
	if err == nil {
		r.sticky = true
		if r.report != nil {
			r.report("wss", true)
		}
	}
	return conn, err
}
func (r *autoResolver) Primary(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	return r.dial(ctx, dc, list, dcs.Resolver.Primary)
}
func (r *autoResolver) MediaOnly(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	return r.dial(ctx, dc, list, dcs.Resolver.MediaOnly)
}
func (r *autoResolver) CDN(ctx context.Context, dc int, list dcs.List) (transport.Conn, error) {
	return r.dial(ctx, dc, list, dcs.Resolver.CDN)
}
