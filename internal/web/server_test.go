package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestWebGatewayAccess(t *testing.T) {
	srv := rpc.NewServer(nil)
	srv.Handle(rpc.MethodWebHistory, func(_ context.Context, raw json.RawMessage) (any, error) {
		return map[string]bool{"local": true}, nil
	})
	srv.Handle(rpc.MethodWebSetChatSettings, func(_ context.Context, raw json.RawMessage) (any, error) {
		var p rpc.WebChatSettings
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, nil
	})
	dial := func(context.Context) (*rpc.Client, error) {
		server, client := net.Pipe()
		go srv.ServeConn(server)
		return rpc.NewClient(client), nil
	}
	const addr = "127.0.0.1:1313"
	const token = "viewer-secret"
	gateway := newGateway(addr, token, dial)

	type probe struct {
		name, path, host, origin, authorization, body string
		cookie, settings                              bool
		status                                        int
		contains                                      string
	}
	for _, tc := range []probe{
		{name: "foreign DNS", host: "rebound.test:1313", origin: "http://rebound.test:1313", path: "message.history", status: 403},
		{name: "foreign origin", host: addr, origin: "http://different.test", path: "message.history", status: 403},
		{name: "missing origin", host: addr, path: "message.history", status: 403},
		{name: "missing token", host: addr, origin: "http://" + addr, path: "message.history", status: 401},
		{name: "old token", host: addr, origin: "http://" + addr, path: "message.history", authorization: "stale", status: 401},
		{name: "local history", host: addr, origin: "http://" + addr, path: "message.history", authorization: token, status: 200, contains: `"local":true`},
		{name: "trailing JSON garbage", host: addr, origin: "http://" + addr, path: "message.history", authorization: token, body: "{} invalid", status: 400},
		{name: "credentials cannot write messages", host: addr, origin: "http://" + addr, path: "message.send", authorization: token, cookie: true, settings: true, status: 403},
		{name: "settings require cookie", host: addr, origin: "http://" + addr, path: "chat.settings.set", authorization: token, settings: true, status: 403},
		{name: "settings require CSRF header", host: addr, origin: "http://" + addr, path: "chat.settings.set", cookie: true, status: 403},
		{name: "browser can update chat settings", host: addr, origin: "http://" + addr, path: "chat.settings.set", cookie: true, settings: true, status: 200, body: `{"chat":"42","history_mode":"skip"}`, contains: `"history_mode":"skip"`},
		{name: "access edits are unavailable", host: addr, origin: "http://" + addr, path: "access.add", cookie: true, settings: true, status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body == "" {
				tc.body = "{}"
			}
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", "application/json")
			if tc.authorization != "" {
				req.Header.Set("Authorization", "Bearer "+tc.authorization)
			}
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: gateway.cookieName(), Value: token})
			}
			if tc.settings {
				req.Header.Set("X-Tg-Web", "settings")
			}
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, req)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("got %d %q; want %d %q", w.Code, w.Body, tc.status, tc.contains)
			}
			if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("security headers missing")
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "http://"+addr+"/api/session", nil)
	req.Header.Set("Origin", "http://"+addr)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	cookies := w.Result().Cookies()
	if w.Code != 204 || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("invalid session exchange: %d %v", w.Code, cookies)
	}
}

func TestWebListenExplicitAddress(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if second, err := listen(occupied.Addr().String(), true); err == nil {
		second.Close()
		t.Fatal("an explicit occupied port should fail")
	}
	if _, err := ValidateListen("192.0.2.1:1313", false); err == nil {
		t.Fatal("non-loopback from config should fail")
	}
	if remote, err := ValidateListen("192.0.2.1:1313", true); err != nil || !remote {
		t.Fatalf("explicit network IP rejected: %t %v", remote, err)
	}
}
