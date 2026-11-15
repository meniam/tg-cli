package tgclient

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/gotd/td/telegram/dcs"
	"golang.org/x/net/proxy"
	"tg-cli/internal/netconf"
)

func resolverFor(s netconf.Settings) (dcs.Resolver, error) {
	return resolverForDial(s, proxy.Direct)
}

// resolverForDial uses forward only to reach the selected proxy.
func resolverForDial(s netconf.Settings, forward proxy.Dialer) (dcs.Resolver, error) {
	if err := s.Validate(); err != nil {
		return nil, err
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
		return dcs.Websocket(dcs.WebsocketOptions{DialOptions: &websocket.DialOptions{Subprotocols: []string{"binary"}, HTTPClient: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("WebSocket redirects are not allowed") }}}}), nil
	}
	if dial != nil {
		return dcs.Plain(dcs.PlainOptions{Dial: dial}), nil
	}
	return nil, nil
}
