package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"tg-cli/internal/rpc"
)

// ErrAlreadyRunning means another daemon holds the lock of the profile.
var ErrAlreadyRunning = errors.New("a daemon is already running for this profile")

const (
	defaultChatLimit = 50
	maxChatLimit     = 500
	stopGrace        = 5 * time.Second
	statusTimeout    = 10 * time.Second
)

// Options configure Run.
type Options struct {
	Profile  string
	Paths    Paths
	Telegram Telegram
	Log      *slog.Logger
	// OnListening, when set, is called once the socket accepts connections.
	OnListening func()
}

type daemon struct {
	opts   Options
	log    *slog.Logger
	srv    *rpc.Server
	login  *loginManager
	cancel context.CancelFunc
	once   sync.Once
}

// Run starts the daemon and blocks until ctx ends, a shutdown request
// arrives or the Telegram client stops. It returns nil on a clean stop.
func Run(ctx context.Context, opts Options) error {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(opts.Paths.Dir, 0o700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	if err := os.Chmod(opts.Paths.Dir, 0o700); err != nil {
		return fmt.Errorf("restrict profile directory: %w", err)
	}
	release, err := acquireLock(opts.Paths.Lock)
	if err != nil {
		return err
	}
	defer release()

	// The lock is ours, so a socket file left here is stale.
	if err := os.Remove(opts.Paths.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	l, err := net.Listen("unix", opts.Paths.Socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.Paths.Socket, err)
	}
	defer func() { _ = os.Remove(opts.Paths.Socket) }()
	if err := os.Chmod(opts.Paths.Socket, 0o600); err != nil {
		_ = l.Close()
		return fmt.Errorf("restrict socket: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := &daemon{opts: opts, log: log, srv: rpc.NewServer(log), cancel: cancel}
	d.login = newLoginManager(runCtx, opts.Telegram)
	d.register()

	tgDone := make(chan error, 1)
	go func() { tgDone <- opts.Telegram.Run(runCtx) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- d.srv.Serve(l) }()

	log.Info("daemon started", "profile", opts.Profile, "socket", opts.Paths.Socket)
	if opts.OnListening != nil {
		opts.OnListening()
	}

	var runErr error
	tgFinished := false
	select {
	case <-runCtx.Done():
	case err := <-tgDone:
		tgFinished = true
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = fmt.Errorf("telegram client stopped: %w", err)
		}
	case err := <-serveDone:
		if err != nil && !errors.Is(err, rpc.ErrServerClosed) {
			runErr = fmt.Errorf("serve: %w", err)
		}
	}

	log.Info("daemon stopping")
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), stopGrace)
	defer stopCancel()
	if err := d.srv.Shutdown(stopCtx); err != nil {
		log.Warn("forced server close", "err", err)
	}
	if !tgFinished {
		select {
		case <-tgDone:
		case <-stopCtx.Done():
			log.Warn("telegram client did not stop in time")
		}
	}
	return runErr
}

func (d *daemon) register() {
	d.srv.Handle(rpc.MethodPing, d.ping)
	d.srv.Handle(rpc.MethodStatus, d.status)
	d.srv.Handle(rpc.MethodShutdown, d.shutdown)
	d.srv.Handle(rpc.MethodAuthStart, d.authStart)
	d.srv.Handle(rpc.MethodAuthCode, d.authCode)
	d.srv.Handle(rpc.MethodAuthPassword, d.authPassword)
	d.srv.Handle(rpc.MethodAuthResend, d.authResend)
	d.srv.Handle(rpc.MethodAuthStartQR, d.authStartQR)
	d.srv.Handle(rpc.MethodAuthWait, d.authWait)
	d.srv.Handle(rpc.MethodAuthCancel, d.authCancel)
	d.srv.Handle(rpc.MethodChatList, d.chatList)
	d.srv.Handle(rpc.MethodChatResolve, d.chatResolve)
	d.srv.Handle(rpc.MethodChatInfo, d.chatInfo)
	d.srv.Handle(rpc.MethodChatInviteInfo, d.chatInviteInfo)
	d.srv.Handle(rpc.MethodMessageHistory, d.messageHistory)
	d.srv.Handle(rpc.MethodMessageGet, d.messageGet)
	d.srv.Handle(rpc.MethodMessagePinned, d.messagePinned)
}

func (d *daemon) ping(context.Context, json.RawMessage) (any, error) {
	return rpc.PingResult{Pong: true}, nil
}

func (d *daemon) status(ctx context.Context, _ json.RawMessage) (any, error) {
	st := rpc.StatusResult{
		Profile:    d.opts.Profile,
		PID:        os.Getpid(),
		LoginState: d.login.state(),
		// The status request itself is active while it runs, do not count it.
		ActiveRequests: max(d.srv.Active()-1, 0),
	}
	select {
	case <-d.opts.Telegram.Ready():
		st.Connected = true
	default:
		return st, nil
	}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	user, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	st.Authorized = user != nil
	st.User = user
	return st, nil
}

func (d *daemon) shutdown(context.Context, json.RawMessage) (any, error) {
	d.once.Do(d.cancel)
	return rpc.ShutdownResult{Stopping: true}, nil
}

func (d *daemon) authStart(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.AuthStartParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	return d.login.start(ctx, p.Phone)
}

func (d *daemon) authCode(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.AuthCodeParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	return d.login.code(ctx, p.Code)
}

func (d *daemon) authPassword(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.AuthPasswordParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	return d.login.password(ctx, p.Password)
}

func (d *daemon) authStartQR(ctx context.Context, _ json.RawMessage) (any, error) {
	return d.login.startQR(ctx)
}

func (d *daemon) authWait(ctx context.Context, _ json.RawMessage) (any, error) {
	return d.login.waitNext(ctx)
}

func (d *daemon) authResend(ctx context.Context, _ json.RawMessage) (any, error) {
	return d.login.resend(ctx)
}

func (d *daemon) authCancel(context.Context, json.RawMessage) (any, error) {
	return rpc.AuthCancelResult{Canceled: d.login.cancel()}, nil
}

// requireAccount waits for the connection and checks that an account is logged
// in. A client may call right after the daemon started, so it waits.
func (d *daemon) requireAccount(ctx context.Context) error {
	select {
	case <-d.opts.Telegram.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}
	user, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return err
	}
	if user == nil {
		return rpc.NewError(rpc.CodeNotAuthorized, "not logged in: run `auth login` first")
	}
	return nil
}

func (d *daemon) chatList(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	limit := p.Limit
	switch {
	case limit == 0:
		limit = defaultChatLimit
	case limit < 0 || limit > maxChatLimit:
		return nil, rpc.NewError(rpc.CodeInvalidParams, fmt.Sprintf("limit must be between 1 and %d", maxChatLimit))
	}
	if p.Type != "" && !chatTypes[p.Type] {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "type must be one of user, bot, group, supergroup, channel")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	chats, err := d.opts.Telegram.Chats(ctx, limit, p.Type)
	if err != nil {
		return nil, err
	}
	if chats == nil {
		chats = []rpc.Chat{}
	}
	return rpc.ChatListResult{Chats: chats}, nil
}

// decodeParams decodes params into v; absent params leave v untouched.
func decodeParams(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return rpc.NewError(rpc.CodeInvalidParams, "invalid params")
	}
	return nil
}
