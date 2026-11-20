package netconf

import (
	"fmt"
	"strings"
	"testing"
)

func TestProxyParsingAndRedaction(t *testing.T) {
	for _, input := range []string{"socks5://alice:marker@proxy.example:1080", "http://alice:marker@proxy.example:8080", "tg://proxy?server=example.org&port=443&secret=dddddddddddddddddddddddddddddddddd", "https://t.me/proxy?server=example.org&port=443&secret=EREREREREREREREREREREQ"} {
		p, err := ParseProxy(input)
		if err != nil {
			t.Fatal(err)
		}
		s := Settings{Transport: TCP, Proxy: p}
		for _, v := range []string{fmt.Sprintf("%v", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s), p.Redacted()} {
			if strings.Contains(v, "marker") || strings.Contains(v, "alice") || strings.Contains(v, "dddddddd") || strings.Contains(v, "ERERER") {
				t.Fatalf("secret leaked: %s", v)
			}
		}
	}
	for _, input := range []string{"socks5://x:0", "socks5://x:65536", "socks4://x:80", "socks5://:3", "tg://proxy?server=x&port=3&secret=marker"} {
		if _, err := ParseProxy(input); err == nil {
			t.Fatalf("accepted %s", input)
		} else if strings.Contains(err.Error(), "marker") {
			t.Fatal("leaked secret")
		}
	}
	p, err := ParseProxy("tg://proxy?server=x&port=443&secret=dddddddddddddddddddddddddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	if err = (Settings{Transport: WSS, Proxy: p}).Validate(); err == nil {
		t.Fatal("accepted wss with MTProxy")
	}
	httpProxy, err := ParseProxy("http://alice:marker@proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	if err := (Settings{Transport: TCP, Proxy: httpProxy}).Validate(); err == nil {
		t.Fatal("accepted HTTP proxy with TCP")
	}
	if err := (Settings{Transport: WSS, Proxy: httpProxy}).Validate(); err != nil {
		t.Fatal(err)
	}
	if got := httpProxy.Redacted(); got != "http://proxy.example:8080" {
		t.Fatalf("redacted proxy = %q", got)
	}
}

type audit06NetconfHolder struct {
	Name  string
	Proxy Proxy
}

func TestAudit06_ProxyValueHidesSecrets(t *testing.T) {
	for _, input := range []string{
		"socks5://alice:marker@proxy.example:1080",
		"http://alice:marker@proxy.example:8080",
		"tg://proxy?server=example.org&port=443&secret=dddddddddddddddddddddddddddddddddd",
	} {
		p, err := ParseProxy(input)
		if err != nil {
			t.Fatal(err)
		}
		holder := audit06NetconfHolder{Name: "x", Proxy: *p}
		for _, v := range []string{
			fmt.Sprintf("%v", *p), fmt.Sprintf("%+v", *p), fmt.Sprintf("%#v", *p), fmt.Sprintf("%s", *p),
			fmt.Sprintf("%v", holder), fmt.Sprintf("%+v", holder), fmt.Sprintf("%#v", holder),
			fmt.Sprintf("%v", []Proxy{*p}), fmt.Sprintf("%v", map[string]Proxy{"k": *p}),
		} {
			for _, secret := range []string{"marker", "alice", "dddddddd", "221"} {
				if strings.Contains(v, secret) {
					t.Fatalf("%s leaked %q: %s", input, secret, v)
				}
			}
		}
	}
}

func TestAudit06_ParseTransport(t *testing.T) {
	for _, in := range []Transport{TCP, WSS, Auto} {
		got, err := ParseTransport(string(in))
		if err != nil || got != in {
			t.Fatalf("ParseTransport(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "TCP", "udp", " tcp"} {
		if got, err := ParseTransport(in); err == nil || got != "" {
			t.Fatalf("ParseTransport(%q) = %q, %v; want error", in, got, err)
		}
	}
}

func TestAudit06_ParseProxyEmptyAndNone(t *testing.T) {
	for _, in := range []string{"", "none"} {
		p, err := ParseProxy(in)
		if p != nil || err != nil {
			t.Fatalf("ParseProxy(%q) = %v, %v; want nil, nil", in, p, err)
		}
	}
	long := strings.Repeat("u", 256)
	for _, in := range []string{
		"socks5://" + long + ":pw@proxy.example:1080",
		"socks5://user:" + long + "@proxy.example:1080",
		"socks5://proxy.example:1080/path",
		"socks5://proxy.example:1080?x=1",
		"socks5://proxy.example",
	} {
		if _, err := ParseProxy(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
	if _, err := ParseProxy("socks5://" + strings.Repeat("u", 255) + ":pw@proxy.example:1080"); err != nil {
		t.Fatalf("rejected a 255-byte user: %v", err)
	}
}

func TestAudit06_EncodeRoundTrip(t *testing.T) {
	for _, in := range []string{
		"socks5://alice:marker@proxy.example:1080",
		"http://proxy.example:8080",
		"tg://proxy?server=example.org&port=443&secret=dddddddddddddddddddddddddddddddddd",
		"https://t.me/proxy?server=example.org&port=443&secret=EREREREREREREREREREREQ",
	} {
		p, err := ParseProxy(in)
		if err != nil {
			t.Fatal(err)
		}
		if p.Encode() != in {
			t.Fatalf("Encode() = %q, want %q", p.Encode(), in)
		}
		back, err := ParseProxy(p.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if back.Kind != p.Kind || back.Host != p.Host || back.Port != p.Port || back.User != p.User || back.Password != p.Password || string(back.Secret) != string(p.Secret) {
			t.Fatalf("round trip changed %q: %+v vs %+v", in, back, p)
		}
	}
	var none *Proxy
	if none.Encode() != "none" {
		t.Fatalf("nil Encode() = %q", none.Encode())
	}
	if back, err := ParseProxy(none.Encode()); back != nil || err != nil {
		t.Fatalf("ParseProxy(none) = %v, %v", back, err)
	}
}

func TestAudit06_ValidateMatrix(t *testing.T) {
	mt, err := ParseProxy("tg://proxy?server=x&port=443&secret=dddddddddddddddddddddddddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	hp, err := ParseProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ParseProxy("socks5://proxy.example:1080")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		s    Settings
		want string
	}{
		{"unknown transport", Settings{Transport: "udp"}, "transport must be tcp, wss or auto"},
		{"mtproxy auto", Settings{Transport: Auto, Proxy: mt}, "MTProxy cannot be combined"},
		{"mtproxy wss", Settings{Transport: WSS, Proxy: mt}, "MTProxy cannot be combined"},
		{"http tcp", Settings{Transport: TCP, Proxy: hp}, "an HTTP proxy works only"},
		{"http default transport", Settings{Proxy: hp}, "an HTTP proxy works only"},
		{"http auto", Settings{Transport: Auto, Proxy: hp}, ""},
		{"http wss", Settings{Transport: WSS, Proxy: hp}, ""},
		{"mtproxy tcp", Settings{Transport: TCP, Proxy: mt}, ""},
		{"mtproxy default transport", Settings{Proxy: mt}, ""},
		{"socks5 auto", Settings{Transport: Auto, Proxy: sp}, ""},
		{"socks5 tcp", Settings{Transport: TCP, Proxy: sp}, ""},
		{"no proxy", Settings{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAudit06_NilProxy(t *testing.T) {
	var p *Proxy
	if p.Address() != "" || p.Redacted() != "" {
		t.Fatalf("nil proxy: address %q, redacted %q", p.Address(), p.Redacted())
	}
	if got := (Settings{Transport: TCP}).String(); got != "transport=tcp proxy=" {
		t.Fatalf("Settings.String() = %q", got)
	}
}
