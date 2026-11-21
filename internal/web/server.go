package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tg-cli/internal/rpc"
)

//go:embed static
var assets embed.FS

type Options struct {
	Listen         string
	Profile        string
	ExplicitListen bool
	Dial           func(context.Context) (*rpc.Client, error)
	Ready          func(Result) error
}

type Result struct {
	URL     string `json:"url"`
	Listen  string `json:"listen"`
	Profile string `json:"profile"`
}

func Run(ctx context.Context, opts Options) error {
	listener, err := listen(opts.Listen, opts.ExplicitListen)
	if err != nil {
		return err
	}
	defer listener.Close()
	address := listener.Addr().String()
	var entropy [32]byte
	rand.Read(entropy[:])
	token := base64.RawURLEncoding.EncodeToString(entropy[:])
	handler := newGateway(address, token, opts.Dial)
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer server.Close()
	if err := opts.Ready(Result{
		URL:    "http://" + address + "/#token=" + token,
		Listen: listener.Addr().String(), Profile: opts.Profile,
	}); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ValidateListen permits literal IPs and localhost so host validation never
// depends on a DNS lookup or a name that resolves differently in the browser.
func ValidateListen(address string, explicit bool) (bool, error) {
	if address == "" {
		return false, nil
	}
	host, port, err := net.SplitHostPort(address)
	n, parseErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || parseErr != nil || n < 1 || n > 65535 || (host != "localhost" && (ip == nil || ip.IsUnspecified())) {
		return false, errors.New("listen must be a literal IP address or localhost with a port")
	}
	remote := host != "localhost" && !ip.IsLoopback()
	if remote && !explicit {
		return false, errors.New("a non-loopback address requires an explicit --listen flag")
	}
	return remote, nil
}

func listen(address string, explicit bool) (net.Listener, error) {
	if _, err := ValidateListen(address, explicit); err != nil {
		return nil, err
	}
	if address != "" {
		if strings.HasPrefix(address, "localhost:") {
			address = "127.0.0.1:" + strings.TrimPrefix(address, "localhost:")
		}
		return net.Listen("tcp", address)
	}
	for port := 1313; port <= 1412; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if !errors.Is(err, syscall.EADDRINUSE) {
			return listener, err
		}
	}
	return nil, errors.New("ports 1313–1412 are in use")
}

type gateway struct {
	address string
	token   string
	dial    func(context.Context) (*rpc.Client, error)
	routes  *http.ServeMux
}

func (a *gateway) cookieName() string {
	_, port, _ := net.SplitHostPort(a.address)
	return "tg_web_session_" + port
}

func newGateway(address, token string, dial func(context.Context) (*rpc.Client, error)) *gateway {
	a := &gateway{address: address, token: token, dial: dial, routes: http.NewServeMux()}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	a.routes.Handle("GET /{$}", http.FileServerFS(static))
	for _, name := range []string{"app.js", "api.js", "chatlist.js", "feed.js", "search.js", "details.js", "style.css"} {
		a.routes.Handle("GET /"+name, http.FileServerFS(static))
	}
	a.routes.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	a.routes.HandleFunc("POST /api/session", a.session)
	a.routes.HandleFunc("POST /api/{method}", a.call)
	a.routes.HandleFunc("GET /api/events", a.events)
	a.routes.HandleFunc("GET /api/thumb", a.thumb)
	return a
}

func (a *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")

	validHost := r.Host == a.address
	host, port, _ := net.SplitHostPort(a.address)
	if host == "127.0.0.1" || host == "localhost" {
		validHost = validHost || r.Host == "127.0.0.1:"+port || r.Host == "localhost:"+port
	}
	if !validHost || (r.Method == http.MethodPost && r.Header.Get("Origin") != "http://"+r.Host) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/session" && !a.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	a.routes.ServeHTTP(w, r)
}

func (a *gateway) bearer(r *http.Request) bool {
	value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(value), []byte(a.token)) == 1
}

func (a *gateway) authorized(r *http.Request) bool {
	if a.bearer(r) {
		return true
	}
	cookie, err := r.Cookie(a.cookieName())
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(a.token)) == 1
}

func (a *gateway) session(w http.ResponseWriter, r *http.Request) {
	if !a.bearer(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: a.token, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// readMethods is the whole surface of the viewer, and it is read-only. A write
// stays available through the CLI and the daemon, never through an HTTP request:
// the viewer has no way to change Telegram, the local settings or the access
// lists, whatever headers or cookies a request carries.
var readMethods = map[string]string{
	"chat.overview":   rpc.MethodChatOverview,
	"chat.info":       rpc.MethodWebChat,
	"access.view":     rpc.MethodWebAccess,
	"chat.settings":   rpc.MethodWebChatSettings,
	"folder.list":     rpc.MethodWebFolders,
	"message.history": rpc.MethodWebHistory,
	"message.context": rpc.MethodWebContext,
	"search":          rpc.MethodSearch,
}

func (a *gateway) call(w http.ResponseWriter, r *http.Request) {
	method, ok := readMethods[r.PathValue("method")]
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" {
		http.Error(w, "Expected application/json", http.StatusUnsupportedMediaType)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	var params map[string]json.RawMessage
	if err := decoder.Decode(&params); err != nil || params == nil {
		http.Error(w, "Expected a JSON object", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(w, "Expected one JSON object", http.StatusBadRequest)
		return
	}
	if method == rpc.MethodSearch {
		// The viewer searches visible messages, including when called outside its UI.
		params["scopes"] = json.RawMessage(`["messages"]`)
		delete(params, "include_deleted")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := a.dial(ctx)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	defer client.Close()
	var result json.RawMessage
	if err := client.Call(ctx, method, params, &result); err != nil {
		writeRPCError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(result)
}

func writeRPCError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	message := "The daemon could not complete the request"
	var re *rpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case rpc.CodeNotFound:
			status, message = http.StatusNotFound, re.Message
		case rpc.CodeInvalidParams:
			status, message = http.StatusBadRequest, re.Message
		case rpc.CodeNotAuthorized:
			status, message = http.StatusUnauthorized, re.Message
		case rpc.CodeWriteDisabled:
			status, message = http.StatusForbidden, re.Message
		case rpc.CodeCursorExpired:
			status, message = http.StatusConflict, re.Message
		case rpc.CodeFloodWait:
			status, message = http.StatusTooManyRequests, "Telegram requested a pause"
			if data, ok := re.Data.(map[string]any); ok {
				if seconds, ok := data["retry_after_seconds"].(float64); ok && seconds > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(seconds)))
				}
			}
		}
	}
	http.Error(w, message, status)
}

func (a *gateway) events(w http.ResponseWriter, r *http.Request) {
	after := r.Header.Get("Last-Event-ID")
	if after == "" {
		after = r.URL.Query().Get("after")
	}
	if _, err := rpc.CursorID(after); err != nil {
		http.Error(w, "Invalid event cursor", http.StatusBadRequest)
		return
	}
	dialCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	client, err := a.dial(dialCtx)
	cancel()
	if err != nil {
		writeRPCError(w, err)
		return
	}
	defer client.Close()
	controller := http.NewResponseController(w)
	started := false
	send := func(cursor string, data []byte) error {
		if cursor == "" {
			return nil
		}
		if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", cursor, data); err != nil {
			return err
		}
		return controller.Flush()
	}
	_, err = client.SubscribeWithStart(r.Context(), rpc.SubscribeParams{After: after, Local: true, Types: []string{rpc.EventMessageNew, rpc.EventMessageEdit, rpc.EventMessageDelete, rpc.EventReaction, rpc.EventRead, rpc.EventGap}}, func(result rpc.SubscribeResult) {
		started = true
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		if after == "" {
			_ = send(result.Cursor, []byte("null"))
		} else {
			_ = controller.Flush()
		}
	}, func(event rpc.Event) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if event.ID == "" {
			// Gap events may have no durable cursor.
			if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return err
			}
			return controller.Flush()
		}
		return send(event.ID, data)
	})
	if err != nil && !started {
		writeRPCError(w, err)
	}
}

func (a *gateway) thumb(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil || id < 1 {
		http.Error(w, "Invalid message id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := a.dial(ctx)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	defer client.Close()
	var out rpc.WebThumbnail
	p := rpc.WebThumbnailParams{Chat: r.URL.Query().Get("chat"), ID: id, Preview: r.URL.Query().Get("preview") == "1"}
	if err := client.Call(ctx, rpc.MethodMediaThumb, p, &out); err != nil {
		writeRPCError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(out.Bytes)
}
