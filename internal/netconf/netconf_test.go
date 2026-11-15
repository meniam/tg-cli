package netconf

import (
	"fmt"
	"strings"
	"testing"
)

func TestProxyParsingAndRedaction(t *testing.T) {
	for _, input := range []string{"socks5://alice:marker@proxy.example:1080", "tg://proxy?server=example.org&port=443&secret=dddddddddddddddddddddddddddddddddd", "https://t.me/proxy?server=example.org&port=443&secret=EREREREREREREREREREREQ"} {
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
	for _, input := range []string{"socks5://x:0", "socks5://x:65536", "socks4://x:80", "http://x:80", "socks5://:3", "tg://proxy?server=x&port=3&secret=marker"} {
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
}
