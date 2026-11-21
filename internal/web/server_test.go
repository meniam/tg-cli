package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tg-cli/internal/rpc"
)

func TestWebGatewayAccess(t *testing.T) {
	srv := rpc.NewServer(nil)
	srv.Handle(rpc.MethodWebHistory, func(_ context.Context, raw json.RawMessage) (any, error) {
		return map[string]bool{"local": true}, nil
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

func TestWebGatewayRefusesEveryWrite(t *testing.T) {
	var calls atomic.Int64
	srv := rpc.NewServer(nil)
	// A request guard sees every RPC the gateway manages to send; the counter
	// must stay zero for the whole write surface.
	srv.SetRequestGuard(func(rpc.Request) error {
		calls.Add(1)
		return nil
	})
	dial := func(context.Context) (*rpc.Client, error) {
		server, client := net.Pipe()
		go srv.ServeConn(server)
		return rpc.NewClient(client), nil
	}
	const addr = "127.0.0.1:1313"
	const token = "viewer-secret"
	gateway := newGateway(addr, token, dial)

	// The eight methods the settings header used to unlock, plus the wider class
	// of writes a viewer must never reach.
	paths := []string{
		"chat.settings.set", "chat.mute", "chat.unmute", "chat.archive", "chat.unarchive",
		"chat.pin_dialog", "chat.unpin_dialog", "folder.edit",
		"message.send", "message.send_media", "message.edit", "message.delete",
		"message.forward", "message.react", "message.pin", "message.unpin",
		"message.poll", "chat.mark_read", "dialog.delete", "dialogs.hide",
		"access.add", "access.remove", "access.write", "account.profile",
		"session.export", "session.terminate", "folder.create", "folder.delete",
	}
	for _, path := range paths {
		for _, credentials := range []string{"cookie", "bearer"} {
			t.Run(path+"/"+credentials, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "http://"+addr+"/api/"+path, strings.NewReader(`{"chat":"42"}`))
				req.Header.Set("Origin", "http://"+addr)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Tg-Web", "settings")
				if credentials == "bearer" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				req.AddCookie(&http.Cookie{Name: gateway.cookieName(), Value: token})
				w := httptest.NewRecorder()
				gateway.ServeHTTP(w, req)
				if w.Code != http.StatusForbidden {
					t.Fatalf("got %d %q; want 403", w.Code, w.Body)
				}
			})
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("gateway reached the daemon %d times for writes", calls.Load())
	}
}

func TestWebGatewayReadsStillWork(t *testing.T) {
	var methods []string
	srv := rpc.NewServer(nil)
	srv.SetRequestGuard(func(req rpc.Request) error {
		methods = append(methods, req.Method)
		return nil
	})
	srv.Handle(rpc.MethodChatOverview, func(context.Context, json.RawMessage) (any, error) {
		return rpc.ChatOverviewResult{Chats: []rpc.ChatOverview{}}, nil
	})
	dial := func(context.Context) (*rpc.Client, error) {
		server, client := net.Pipe()
		go srv.ServeConn(server)
		return rpc.NewClient(client), nil
	}
	const addr = "127.0.0.1:1313"
	gateway := newGateway(addr, "viewer-secret", dial)
	req := httptest.NewRequest(http.MethodPost, "http://"+addr+"/api/chat.overview", strings.NewReader(`{"limit":500}`))
	req.Header.Set("Origin", "http://"+addr)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: gateway.cookieName(), Value: "viewer-secret"})
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK || len(methods) != 1 || methods[0] != rpc.MethodChatOverview {
		t.Fatalf("got %d %v", w.Code, methods)
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
