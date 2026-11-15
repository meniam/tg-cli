package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"tg-cli/internal/throttle"
)

const (
	// MaxConcurrent is the number of requests the server runs in parallel.
	MaxConcurrent = 16
	// MaxConcurrentPerConn reserves capacity for other connections.
	MaxConcurrentPerConn = 8
	// DefaultRequestTimeout is the deadline of one request.
	DefaultRequestTimeout = 120 * time.Second
)

// ErrServerClosed is returned by Serve after Shutdown or Close.
var ErrServerClosed = errors.New("rpc: server closed")

// Handler serves one method. The returned value is encoded as the result.
// Return *Error to control the error code; any other error becomes an
// internal error with a generic message.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server dispatches JSON-RPC requests to handlers. It never logs request
// parameters: they can carry login codes and passwords.
type Server struct {
	requestGuard func(Request) error
	// RequestTimeout is the deadline of one request, DefaultRequestTimeout by default.
	RequestTimeout time.Duration

	log        *slog.Logger
	baseCtx    context.Context
	baseCancel context.CancelFunc
	sem        chan struct{}
	active     atomic.Int64
	nextSub    atomic.Uint64

	mu             sync.Mutex
	handlers       map[string]Handler
	methodTimeouts map[string]time.Duration
	conns          map[*serverConn]struct{}
	listeners      map[net.Listener]struct{}
	closing        bool
	hub            *Hub
	replay         ReplayFunc
}

type serverConn struct {
	c       net.Conn
	active  atomic.Int64
	mu      sync.Mutex
	sub     *Subscription
	subID   string
	pending bool
}

// NewServer creates a server without handlers.
func NewServer(log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		RequestTimeout: DefaultRequestTimeout,
		log:            log,
		baseCtx:        ctx,
		baseCancel:     cancel,
		sem:            make(chan struct{}, MaxConcurrent),
		handlers:       map[string]Handler{},
		methodTimeouts: map[string]time.Duration{},
		conns:          map[*serverConn]struct{}{},
		listeners:      map[net.Listener]struct{}{},
	}
}

// SetRequestGuard installs a check applied before all request handlers.
func (s *Server) SetRequestGuard(guard func(Request) error) { s.requestGuard = guard }

// Handle registers a handler. It panics on a duplicate method.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.handlers[method]; dup {
		panic("rpc: duplicate handler for " + method)
	}
	s.handlers[method] = h
}

// SetMethodTimeout overrides RequestTimeout for a registered method.
func (s *Server) SetMethodTimeout(method string, timeout time.Duration) {
	if timeout <= 0 {
		panic("rpc: method timeout must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handlers[method] == nil {
		panic("rpc: timeout for unregistered method " + method)
	}
	s.methodTimeouts[method] = timeout
}

// Methods returns a sorted snapshot of registered method names.
func (s *Server) Methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	methods := make([]string, 0, len(s.handlers))
	for method := range s.handlers {
		methods = append(methods, method)
	}
	if s.hub != nil {
		methods = append(methods, MethodSubscribe, MethodUnsubscribe)
	}
	sort.Strings(methods)
	return methods
}

// SetEventSource enables the built-in subscribe and unsubscribe methods.
// Call it before serving connections. The replay callback runs after the
// subscriber is registered and may read durable history from another package.
func (s *Server) SetEventSource(hub *Hub, replay ReplayFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hub, s.replay = hub, replay
}

// PublishEvent fans out one event without waiting for subscribers.
func (s *Server) PublishEvent(event Event) {
	s.mu.Lock()
	hub := s.hub
	s.mu.Unlock()
	if hub != nil {
		hub.Publish(event)
	}
}

// Active returns the number of requests being handled right now.
func (s *Server) Active() int { return int(s.active.Load()) }

// Serve accepts connections until Shutdown or Close, then returns ErrServerClosed.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = l.Close()
		return ErrServerClosed
	}
	s.listeners[l] = struct{}{}
	s.mu.Unlock()

	for {
		c, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing {
				return ErrServerClosed
			}
			return err
		}
		go s.ServeConn(c)
	}
}

// ServeConn serves one connection until it closes. Closing the connection
// cancels the context of its in-flight requests.
func (s *Server) ServeConn(c net.Conn) {
	sc := &serverConn{c: c}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = c.Close()
		return
	}
	s.conns[sc] = struct{}{}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(s.baseCtx)
	var wg sync.WaitGroup
	w := &frameWriter{w: c}
	s.readLoop(ctx, sc, newFrameReader(c), w, &wg)
	cancel()
	sc.stopSubscription()
	wg.Wait()
	_ = c.Close()

	s.mu.Lock()
	delete(s.conns, sc)
	s.mu.Unlock()
}

func (s *Server) readLoop(ctx context.Context, sc *serverConn, fr *frameReader, w *frameWriter, wg *sync.WaitGroup) {
	for {
		line, err := fr.next()
		if err != nil {
			if errors.Is(err, ErrFrameTooLarge) {
				_ = w.write(errorResponse(nil, CodeInvalidRequest, "frame exceeds 1 MiB"))
			}
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = w.write(errorResponse(nil, CodeParseError, "parse error"))
			continue
		}
		if req.JSONRPC != Version || req.Method == "" {
			if !req.IsNotification() {
				_ = w.write(errorResponse(req.ID, CodeInvalidRequest, "invalid request"))
			}
			continue
		}
		if req.IsNotification() && (req.Method == MethodSubscribe || req.Method == MethodUnsubscribe) {
			continue
		}
		if sc.active.Load() >= MaxConcurrentPerConn {
			if !req.IsNotification() {
				_ = w.write(errorResponse(req.ID, CodeBusy, "too many parallel requests on this connection"))
			}
			continue
		}
		select {
		case s.sem <- struct{}{}:
		default:
			if !req.IsNotification() {
				_ = w.write(errorResponse(req.ID, CodeBusy, "too many parallel requests"))
			}
			continue
		}
		sc.active.Add(1)
		s.active.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.sem }()
			defer s.active.Add(-1)
			defer sc.active.Add(-1)
			var resp Response
			var sub *Subscription
			var plan ReplayPlan
			started := time.Now()
			if s.requestGuard != nil && (req.Method == MethodSubscribe || req.Method == MethodUnsubscribe) {
				if err := s.requestGuard(req); err != nil {
					e := s.toError(req.Method, err)
					_ = w.write(Response{JSONRPC: Version, ID: req.ID, Error: e})
					return
				}
			}
			switch req.Method {
			case MethodSubscribe:
				resp, sub, plan = s.subscribe(ctx, sc, req)
			case MethodUnsubscribe:
				resp = s.unsubscribe(sc, req)
			default:
				resp = s.call(ctx, req)
			}
			if req.Method == MethodSubscribe || req.Method == MethodUnsubscribe {
				code := any("ok")
				if resp.Error != nil {
					code = resp.Error.Code
				}
				s.log.Info("request", "method", req.Method, "duration_ms", time.Since(started).Milliseconds(), "code", code)
			}
			if !req.IsNotification() {
				if err := w.write(resp); err != nil {
					sc.stopSubscription()
					_ = sc.c.Close()
					return
				}
			}
			if sub != nil {
				wg.Add(1)
				go func() { defer wg.Done(); s.sendEvents(ctx, sc, w, sub, plan) }()
			}
		}()
	}
}

func (s *Server) call(ctx context.Context, req Request) Response {
	started := time.Now()
	method := req.Method
	s.mu.Lock()
	h := s.handlers[req.Method]
	timeout := s.methodTimeouts[req.Method]
	s.mu.Unlock()
	if h == nil {
		// An unknown method is untrusted input and may contain a secret.
		s.log.Info("request", "method", "unknown", "duration_ms", time.Since(started).Milliseconds(), "code", CodeMethodNotFound)
		return errorResponse(req.ID, CodeMethodNotFound, "method not found: "+req.Method)
	}
	if timeout == 0 {
		timeout = s.RequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	waited := new(throttle.Wait)
	ctx = throttle.WithWait(ctx, waited)
	response := Response{JSONRPC: Version, ID: req.ID}
	defer func() {
		code := any("ok")
		if response.Error != nil {
			code = response.Error.Code
		}
		s.log.Info("request", "method", method, "duration_ms", time.Since(started).Milliseconds(), "code", code)
	}()

	if s.requestGuard != nil {
		if err := s.requestGuard(req); err != nil {
			response.Error = s.toError(req.Method, err)
			return response
		}
	}
	result, err := s.safeCall(ctx, h, req)
	if err != nil {
		response.Error = s.toError(req.Method, err)
		response.ThrottledMS = waitedMilliseconds(waited)
		return response
	}
	data, err := json.Marshal(result)
	if err != nil {
		response.Error = s.toError(req.Method, err)
		response.ThrottledMS = waitedMilliseconds(waited)
		return response
	}
	response.Result = data
	response.ThrottledMS = waitedMilliseconds(waited)
	return response
}

// Round positive sub-millisecond waits up so an actual throttle wait remains visible.
func waitedMilliseconds(waited *throttle.Wait) int64 {
	d := waited.Duration()
	if d <= 0 {
		return 0
	}
	return int64((d-1)/time.Millisecond + 1)
}

func (s *Server) safeCall(ctx context.Context, h Handler, req Request) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("handler panic", "method", req.Method, "stack", string(debug.Stack()))
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h(ctx, bytes.Clone(req.Params))
}

func (s *Server) toError(method string, err error) *Error {
	var rpcErr *Error
	switch {
	case errors.As(err, &rpcErr):
		return rpcErr
	case errors.Is(err, context.DeadlineExceeded):
		return NewError(CodeInternal, "request timed out")
	case errors.Is(err, context.Canceled):
		return NewError(CodeInternal, "request canceled")
	}
	// The client gets no details; the journal keeps the cause for debugging.
	// The request record above never carries error text.
	s.log.Error("handler failed", "method", method, "err", err)
	return NewError(CodeInternal, "internal error")
}

func errorResponse(id json.RawMessage, code int, message string) Response {
	return Response{JSONRPC: Version, ID: id, Error: NewError(code, message)}
}

// Shutdown stops accepting connections, closes idle ones and waits until
// in-flight requests finish. When ctx ends first, it closes everything.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	for l := range s.listeners {
		_ = l.Close()
	}
	s.listeners = map[net.Listener]struct{}{}
	s.mu.Unlock()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		for sc := range s.conns {
			if sc.active.Load() == 0 {
				_ = sc.c.Close()
			}
		}
		remaining := len(s.conns)
		s.mu.Unlock()
		if remaining == 0 {
			s.baseCancel()
			return nil
		}
		select {
		case <-ctx.Done():
			_ = s.Close()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Close stops the server at once: listeners and connections are closed and
// in-flight requests are canceled.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closing = true
	for l := range s.listeners {
		_ = l.Close()
	}
	s.listeners = map[net.Listener]struct{}{}
	for sc := range s.conns {
		_ = sc.c.Close()
	}
	s.mu.Unlock()
	s.baseCancel()
	return nil
}
