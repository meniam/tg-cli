package daemon

import (
	"context"
	"errors"
	"sync"
	"time"

	"tg-cli/internal/rpc"
)

// loginTimeout bounds a whole login, including the time the user needs to
// type the code.
const loginTimeout = 5 * time.Minute

type loginEvent struct {
	state  string
	prompt rpc.CodePrompt
	qr     rpc.QRToken
	user   *rpc.User
	err    error
}

type loginFlow struct {
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan loginEvent // buffered, holds only the latest event
	codeCh     chan string
	passwordCh chan string
	resendCh   chan struct{}
	state      string // guarded by loginManager.mu
}

// emit stores ev, replacing an older unread event. It never blocks.
func (f *loginFlow) emit(ev loginEvent) {
	for {
		select {
		case f.events <- ev:
			return
		default:
			select {
			case <-f.events:
			default:
			}
		}
	}
}

// loginManager drives one phone login at a time. The Telegram client asks for
// the code and the password through callbacks; the manager turns them into
// the RPC steps auth.start, auth.code and auth.password.
type loginManager struct {
	tg      Telegram
	baseCtx context.Context

	mu   sync.Mutex
	flow *loginFlow
	// done is the last finished flow whose final event nobody has read yet.
	// A client that waits for a QR scan may ask after the flow ended.
	done *loginFlow
}

func newLoginManager(baseCtx context.Context, tg Telegram) *loginManager {
	return &loginManager{tg: tg, baseCtx: baseCtx}
}

// state returns the state the login waits in, or "" when none is running.
func (m *loginManager) state() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.flow == nil {
		return ""
	}
	return m.flow.state
}

// begin checks that a login can start and registers a new flow.
func (m *loginManager) begin(ctx context.Context) (*loginFlow, error) {
	if err := m.waitReady(ctx); err != nil {
		return nil, err
	}
	user, err := m.tg.Self(ctx)
	if err != nil {
		return nil, err
	}
	if user != nil {
		return nil, rpc.NewError(rpc.CodeLoginState, "already logged in")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.flow != nil {
		return nil, rpc.NewError(rpc.CodeLoginState, "a login is already in progress; cancel it first")
	}
	fctx, cancel := context.WithTimeout(m.baseCtx, loginTimeout)
	f := &loginFlow{
		ctx:        fctx,
		cancel:     cancel,
		events:     make(chan loginEvent, 1),
		codeCh:     make(chan string),
		passwordCh: make(chan string),
		resendCh:   make(chan struct{}),
	}
	m.flow = f
	m.done = nil
	return f, nil
}

func (m *loginManager) start(ctx context.Context, phone string) (rpc.AuthResult, error) {
	if phone == "" {
		return rpc.AuthResult{}, rpc.NewError(rpc.CodeInvalidParams, "phone is required")
	}
	f, err := m.begin(ctx)
	if err != nil {
		return rpc.AuthResult{}, err
	}
	go m.run(f, phone)
	return m.wait(ctx, f)
}

// startQR begins a login by QR code: the result carries the first token.
func (m *loginManager) startQR(ctx context.Context) (rpc.AuthResult, error) {
	f, err := m.begin(ctx)
	if err != nil {
		return rpc.AuthResult{}, err
	}
	go m.runQR(f)
	return m.wait(ctx, f)
}

// waitNext waits for the next change of a login that is already running: a
// refreshed QR token, a password request or the end. It also returns the final
// event of a login that ended before the client asked.
func (m *loginManager) waitNext(ctx context.Context) (rpc.AuthResult, error) {
	m.mu.Lock()
	f, finished := m.flow, false
	if f == nil {
		f, finished = m.done, true
	}
	m.mu.Unlock()
	if f == nil {
		return rpc.AuthResult{}, rpc.NewError(rpc.CodeLoginState, "no login is in progress")
	}
	res, err := m.wait(ctx, f)
	if finished || err != nil {
		m.mu.Lock()
		if m.done == f {
			m.done = nil
		}
		m.mu.Unlock()
	}
	return res, err
}

func (m *loginManager) code(ctx context.Context, code string) (rpc.AuthResult, error) {
	if code == "" {
		return rpc.AuthResult{}, rpc.NewError(rpc.CodeInvalidParams, "code is required")
	}
	return m.provide(ctx, rpc.AuthCodeRequired, func(f *loginFlow) chan string { return f.codeCh }, code)
}

// resend asks Telegram to deliver the code another way. It is valid while the
// login waits for the code.
func (m *loginManager) resend(ctx context.Context) (rpc.AuthResult, error) {
	m.mu.Lock()
	f := m.flow
	if f == nil || f.state != rpc.AuthCodeRequired {
		m.mu.Unlock()
		return rpc.AuthResult{}, rpc.NewError(rpc.CodeLoginState, "login is not waiting for the code; the current state is "+m.stateLocked())
	}
	f.state = ""
	select {
	case <-f.events:
	default:
	}
	m.mu.Unlock()

	select {
	case f.resendCh <- struct{}{}:
	case <-ctx.Done():
		return rpc.AuthResult{}, ctx.Err()
	case <-f.ctx.Done():
		return rpc.AuthResult{}, loginEnded(f)
	}
	return m.wait(ctx, f)
}

func (m *loginManager) password(ctx context.Context, password string) (rpc.AuthResult, error) {
	if password == "" {
		return rpc.AuthResult{}, rpc.NewError(rpc.CodeInvalidParams, "password is required")
	}
	return m.provide(ctx, rpc.AuthPasswordRequired, func(f *loginFlow) chan string { return f.passwordCh }, password)
}

func (m *loginManager) provide(ctx context.Context, want string, channel func(*loginFlow) chan string, value string) (rpc.AuthResult, error) {
	m.mu.Lock()
	f := m.flow
	if f == nil || f.state != want {
		m.mu.Unlock()
		return rpc.AuthResult{}, rpc.NewError(rpc.CodeLoginState, "login is not waiting for this step; the current state is "+m.stateLocked())
	}
	f.state = "" // the value is being delivered; a second submit is rejected
	// Drop a stale event so wait sees only what follows this step.
	select {
	case <-f.events:
	default:
	}
	m.mu.Unlock()

	select {
	case channel(f) <- value:
	case <-ctx.Done():
		return rpc.AuthResult{}, ctx.Err()
	case <-f.ctx.Done():
		return rpc.AuthResult{}, loginEnded(f)
	}
	return m.wait(ctx, f)
}

func (m *loginManager) stateLocked() string {
	if m.flow == nil {
		return "none"
	}
	if m.flow.state == "" {
		return "processing"
	}
	return m.flow.state
}

func (m *loginManager) cancel() bool {
	m.mu.Lock()
	f := m.flow
	m.flow = nil
	m.mu.Unlock()
	if f == nil {
		return false
	}
	f.cancel()
	return true
}

func (m *loginManager) waitReady(ctx context.Context) error {
	select {
	case <-m.tg.Ready():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *loginManager) wait(ctx context.Context, f *loginFlow) (rpc.AuthResult, error) {
	select {
	case ev := <-f.events:
		if ev.err != nil {
			return rpc.AuthResult{}, ev.err
		}
		return rpc.AuthResult{
			State:              ev.state,
			Delivery:           ev.prompt.Delivery,
			NextDelivery:       ev.prompt.NextDelivery,
			ResendAfterSeconds: ev.prompt.ResendAfterSeconds,
			QRURL:              ev.qr.URL,
			ExpiresInSeconds:   ev.qr.ExpiresInSeconds,
			User:               ev.user,
		}, nil
	case <-ctx.Done():
		return rpc.AuthResult{}, ctx.Err()
	}
}

func loginEnded(f *loginFlow) error {
	if errors.Is(f.ctx.Err(), context.DeadlineExceeded) {
		return rpc.NewError(rpc.CodeLoginState, "login timed out")
	}
	return rpc.NewError(rpc.CodeLoginState, "login was canceled")
}

// passwordFn asks for the two-factor password through the RPC step.
func (m *loginManager) passwordFn(f *loginFlow) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		m.setState(f, rpc.AuthPasswordRequired, loginEvent{state: rpc.AuthPasswordRequired})
		select {
		case v := <-f.passwordCh:
			return v, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// setState records the state the login waits in and reports it as an event.
func (m *loginManager) setState(f *loginFlow, state string, ev loginEvent) {
	m.mu.Lock()
	f.state = state
	m.mu.Unlock()
	f.emit(ev)
}

// run executes the phone login and reports every state change as an event.
func (m *loginManager) run(f *loginFlow, phone string) {
	err := m.tg.Login(f.ctx, phone,
		func(ctx context.Context, prompt rpc.CodePrompt) (string, bool, error) {
			m.setState(f, rpc.AuthCodeRequired, loginEvent{state: rpc.AuthCodeRequired, prompt: prompt})
			select {
			case v := <-f.codeCh:
				return v, false, nil
			case <-f.resendCh:
				return "", true, nil
			case <-ctx.Done():
				return "", false, ctx.Err()
			}
		},
		m.passwordFn(f))
	m.finish(f, err)
}

// runQR executes the QR login and reports every new token as an event.
func (m *loginManager) runQR(f *loginFlow) {
	err := m.tg.LoginQR(f.ctx,
		func(_ context.Context, token rpc.QRToken) error {
			m.setState(f, rpc.AuthQRScan, loginEvent{state: rpc.AuthQRScan, qr: token})
			return nil
		},
		m.passwordFn(f))
	m.finish(f, err)
}

// finish ends the flow and reports the outcome as its last event.
func (m *loginManager) finish(f *loginFlow, err error) {
	ev := loginEvent{state: rpc.AuthAuthorized}
	switch {
	case err != nil && f.ctx.Err() != nil:
		ev = loginEvent{err: loginEnded(f)}
	case err != nil:
		ev = loginEvent{err: err}
	default:
		ev.user, ev.err = m.tg.Self(f.ctx)
	}

	m.mu.Lock()
	if m.flow == f {
		m.flow = nil
		m.done = f
	}
	m.mu.Unlock()
	f.emit(ev)
	f.cancel()
}
