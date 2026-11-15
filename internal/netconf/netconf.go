// Package netconf validates and stores profile network settings.
package netconf

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Transport string

const (
	TCP  Transport = "tcp"
	WSS  Transport = "wss"
	Auto Transport = "auto"
)

type ProxyKind string

const (
	SOCKS5  ProxyKind = "socks5"
	MTProxy ProxyKind = "mtproxy"
	HTTP    ProxyKind = "http"
)

type Proxy struct {
	Kind     ProxyKind
	Host     string
	Port     int
	User     string
	Password string
	Secret   []byte
	raw      string
}
type Settings struct {
	Transport Transport
	Proxy     *Proxy
}

func ParseTransport(s string) (Transport, error) {
	switch Transport(s) {
	case TCP, WSS, Auto:
		return Transport(s), nil
	}
	return "", errors.New("transport must be tcp, wss or auto")
}
func ParseProxy(s string) (*Proxy, error) {
	if s == "" || s == "none" {
		return nil, nil
	}
	invalid := func() (*Proxy, error) { return nil, errors.New("invalid proxy address") }
	u, err := url.Parse(s)
	if err != nil {
		return invalid()
	}
	p := &Proxy{raw: s}
	switch {
	case u.Scheme == "socks5" || u.Scheme == "http":
		if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return invalid()
		}
		p.Kind = ProxyKind(u.Scheme)
		p.Host = u.Hostname()
		if u.User != nil {
			p.User = u.User.Username()
			p.Password, _ = u.User.Password()
			if p.User == "" || len(p.User) > 255 || len(p.Password) > 255 {
				return invalid()
			}
		}
		p.Port, err = strconv.Atoi(u.Port())
	case (u.Scheme == "tg" && u.Host == "proxy" && u.Path == "") || (u.Scheme == "https" && u.Host == "t.me" && u.Path == "/proxy"):
		if u.User != nil || u.Fragment != "" {
			return invalid()
		}
		q, e := url.ParseQuery(u.RawQuery)
		if e != nil || len(q) != 3 || len(q["server"]) != 1 || len(q["port"]) != 1 || len(q["secret"]) != 1 {
			return invalid()
		}
		p.Kind = MTProxy
		p.Host = q.Get("server")
		p.Port, err = strconv.Atoi(q.Get("port"))
		secret := q.Get("secret")
		p.Secret, e = hex.DecodeString(secret)
		if e != nil {
			p.Secret, e = base64.RawURLEncoding.DecodeString(strings.TrimRight(secret, "="))
		}
		if e != nil || !validSecret(p.Secret) {
			return invalid()
		}
	default:
		return invalid()
	}
	if err != nil || p.Host == "" || strings.ContainsAny(p.Host, " \t\r\n/@") || p.Port < 1 || p.Port > 65535 {
		return invalid()
	}
	return p, nil
}
func validSecret(s []byte) bool {
	if len(s) == 16 {
		return true
	}
	if len(s) < 17 {
		return false
	}
	switch s[0] {
	case 0xef, 0xee, 0xdd:
		return true
	}
	return false
}
func (s Settings) Validate() error {
	if s.Transport != "" && s.Transport != TCP && s.Transport != WSS && s.Transport != Auto {
		return errors.New("transport must be tcp, wss or auto")
	}
	if s.Proxy != nil && s.Proxy.Kind == MTProxy && (s.Transport == WSS || s.Transport == Auto) {
		return errors.New("MTProxy cannot be combined with wss or auto")
	}
	if s.Proxy != nil && s.Proxy.Kind == HTTP && s.Transport != WSS && s.Transport != Auto {
		return errors.New("an HTTP proxy works only with wss or auto")
	}
	return nil
}
func (p *Proxy) Address() string {
	if p == nil {
		return ""
	}
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}
func (p *Proxy) Redacted() string {
	if p == nil {
		return ""
	}
	return string(p.Kind) + "://" + p.Address()
}
func (p *Proxy) String() string   { return p.Redacted() }
func (p *Proxy) GoString() string { return p.Redacted() }
func (p *Proxy) Encode() string {
	if p == nil {
		return "none"
	}
	return p.raw
}
func (s Settings) String() string {
	return fmt.Sprintf("transport=%s proxy=%s", s.Transport, s.Proxy.Redacted())
}
func (s Settings) GoString() string { return s.String() }
