package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
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
	Store    *store.DB
	Access   *access.Loader
	Log      *slog.Logger
	// IdleTimeout stops the daemon after this long without a request and with
	// no request in flight. Zero means no idle stop: the daemon lives until
	// `daemon stop` or a failure.
	IdleTimeout time.Duration
	// OnListening, when set, is called once the socket accepts connections.
	OnListening func()
}

// idleCheckEvery is how often the idle stop looks at the daemon. It is finer
// than the shortest useful timeout and coarse enough to cost nothing.
const idleCheckEvery = 100 * time.Millisecond

type daemon struct {
	account             accountCache
	idempotencyLocks    keyLocks
	opts                Options
	log                 *slog.Logger
	srv                 *rpc.Server
	hub                 *rpc.Hub
	login               *loginManager
	store               *store.DB
	sync                *historySyncManager
	cancel              context.CancelFunc
	once                sync.Once
	eventMu             sync.Mutex
	deletionsMu         sync.Mutex
	contactsMu          sync.Mutex
	addEvent            func(context.Context, store.Event) (int64, bool, error)
	findMessageChats    func(context.Context, []int) ([]int64, error)
	markMessagesDeleted func(context.Context, int64, []int) (int, error)
	openMediaFile       func([]string, string, int64) (*os.File, int64, error)
	// lastActivity is the Unix time of the last request that started or ended,
	// in nanoseconds. A request is often shorter than the idle check, so the
	// count of requests in flight alone would miss it.
	lastActivity atomic.Int64
	// inFlight is the number of requests being served right now.
	inFlight atomic.Int64
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
	ownedStore := opts.Store
	if ownedStore == nil {
		ownedStore, err = store.Open(opts.Paths.Database)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer ownedStore.Close()
	}

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
	if opts.Access == nil {
		opts.Access = access.Open(opts.Paths.Access)
	}
	d := &daemon{opts: opts, log: log, srv: rpc.NewServer(log), hub: rpc.NewHub(), cancel: cancel, store: ownedStore}
	d.sync = newHistorySyncManager(runCtx, d, nil)
	// A daemon that has just started is not idle: the clock runs from now.
	d.lastActivity.Store(time.Now().UnixNano())
	d.login = newLoginManager(runCtx, opts.Telegram)
	authorized := make(chan struct{}, 1)
	d.login.onUser = d.account.set
	d.login.onAuthorized = func() {
		select {
		case authorized <- struct{}{}:
		default:
		}
	}
	d.srv.SetRequestGuard(func(req rpc.Request) error {
		if strings.HasPrefix(req.Method, "user.") || strings.HasPrefix(req.Method, "contact.") {
			req.Method = "chat." + req.Method
		}
		return d.account.check(req)
	})
	contactsDone := make(chan struct{})
	accountDone := make(chan struct{})
	go func() {
		defer close(accountDone)
		select {
		case <-runCtx.Done():
			return
		case <-opts.Telegram.Ready():
		}
		user, err := opts.Telegram.Self(runCtx)
		if err == nil && user != nil {
			d.account.setIfEmpty(user)
		}
	}()
	go func() {
		defer close(contactsDone)
		select {
		case <-runCtx.Done():
			return
		case <-accountDone:
		}
		d.account.mu.RLock()
		hasAccount := d.account.user != nil
		d.account.mu.RUnlock()
		if !hasAccount {
			select {
			case <-runCtx.Done():
				return
			case <-authorized:
			}
		}
		d.contactsMu.Lock()
		defer d.contactsMu.Unlock()
		if _, err := d.syncContacts(throttle.WithBackground(runCtx)); err != nil && !errors.Is(err, context.Canceled) {
			d.log.Warn("contact sync failed")
		}
	}()
	d.register()
	d.srv.SetEventSource(d.hub, d.replayEvents)
	opts.Telegram.SetEventSink(d.handleEvent)
	if _, err := d.store.PruneIdempotency(runCtx, time.Now()); err != nil {
		d.log.Warn("idempotency cleanup failed")
	}
	idempotencyDone := make(chan struct{})
	go func() { defer close(idempotencyDone); d.pruneIdempotency(runCtx) }()
	pruneDone := make(chan struct{})
	go func() { defer close(pruneDone); d.pruneEvents(runCtx, eventPruneEvery, eventRetention, eventKeep) }()

	d.logConnection(runCtx)
	tgDone := make(chan error, 1)
	go func() { tgDone <- opts.Telegram.Run(runCtx) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- d.srv.Serve(l) }()

	// An idle stop ends the daemon the same way a shutdown request does: it
	// cancels the context Run waits on, so the stop sequence below runs once.
	idleDone := make(chan struct{})
	if opts.IdleTimeout > 0 {
		go func() {
			defer close(idleDone)
			watchIdle(runCtx, cancel, log, d, opts.IdleTimeout)
		}()
	} else {
		close(idleDone)
	}

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
	d.sync.close()
	// Join the idle monitor: a goroutine that outlived the shutdown would keep
	// the process alive after Run returned.
	<-idleDone
	<-pruneDone
	<-contactsDone
	<-idempotencyDone
	<-accountDone
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

// watchIdle cancels the daemon when it has been without a request for longer
// than idle and no request is in flight.
func watchIdle(ctx context.Context, cancel context.CancelFunc, log *slog.Logger, d *daemon, idle time.Duration) {
	ticker := time.NewTicker(idleCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// A request in flight is work that must finish, whatever its age.
			if d.inFlight.Load() > 0 || d.hub.Subscribers() > 0 || d.sync != nil && d.sync.hasActive() {
				continue
			}
			last := time.Unix(0, d.lastActivity.Load())
			if now.Sub(last) < idle {
				continue
			}
			log.Info("daemon idle, stopping", "idle", idle.String())
			cancel()
			return
		}
	}
}

// tracking records a request as activity, so the idle stop sees a short request
// that starts and ends between two checks.
func (d *daemon) tracking(h rpc.Handler) rpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		d.inFlight.Add(1)
		d.lastActivity.Store(time.Now().UnixNano())
		defer func() {
			d.lastActivity.Store(time.Now().UnixNano())
			d.inFlight.Add(-1)
		}()
		return h(ctx, params)
	}
}

// handle registers a handler that counts as activity for the idle stop.
func (d *daemon) handle(method string, h rpc.Handler) {
	d.srv.Handle(method, d.tracking(func(ctx context.Context, params json.RawMessage) (any, error) {
		if method != rpc.MethodPing && method != rpc.MethodStatus && method != rpc.MethodShutdown {
			ctx = rpc.WithConnectionWait(ctx)
		}
		return h(ctx, params)
	}))
}

func (d *daemon) register() {
	d.handle(rpc.MethodPing, d.ping)
	d.handle(rpc.MethodStatus, d.status)
	d.handle(rpc.MethodShutdown, d.shutdown)
	d.handle(rpc.MethodAuthStart, d.authStart)
	d.handle(rpc.MethodAuthCode, d.authCode)
	d.handle(rpc.MethodAuthPassword, d.authPassword)
	d.handle(rpc.MethodAuthResend, d.authResend)
	d.handle(rpc.MethodAuthStartQR, d.authStartQR)
	d.handle(rpc.MethodAuthWait, d.authWait)
	d.handle(rpc.MethodAuthCancel, d.authCancel)
	d.handle(rpc.MethodChatList, d.chatList)
	d.handle(rpc.MethodChatMembers, d.chatMembers)
	d.handle(rpc.MethodChatTopics, d.chatTopics)
	d.handle(rpc.MethodChatCommon, d.chatCommon)
	d.handle(rpc.MethodChatResolve, d.chatResolve)
	d.handle(rpc.MethodChatInfo, d.chatInfo)
	d.handle(rpc.MethodChatInviteInfo, d.chatInviteInfo)
	d.handle(rpc.MethodMessageHistory, d.messageHistory)
	d.handle(rpc.MethodMessageGet, d.messageGet)
	d.handle(rpc.MethodMessagePinned, d.messagePinned)
	d.handle(rpc.MethodMessageScheduledList, d.scheduledList)
	d.handle(rpc.MethodMessageScheduledDelete, d.scheduledDelete)
	d.handle(rpc.MethodMessageScheduledSendNow, d.scheduledSendNow)
	d.handle(rpc.MethodMessageSend, d.messageSend)
	d.handle(rpc.MethodMessageSendMedia, d.messageSendMedia)
	d.srv.SetMethodTimeout(rpc.MethodMessageSendMedia, 600*time.Second)
	d.handle(rpc.MethodMessageEdit, d.messageEdit)
	d.handle(rpc.MethodMessageDelete, d.messageDelete)
	d.handle(rpc.MethodMessageForward, d.messageForward)
	d.handle(rpc.MethodMessageReact, d.messageReact)
	d.handle(rpc.MethodMessagePin, d.messagePin)
	d.handle(rpc.MethodMessageUnpin, d.messageUnpin)
	d.handle(rpc.MethodMessagePoll, d.messagePoll)
	d.handle(rpc.MethodMessageSearch, d.messageSearch)
	d.handle(rpc.MethodMessageSearchPublic, d.messageSearchPublic)
	d.handle(rpc.MethodMessageContext, d.messageContext)
	d.handle(rpc.MethodMessageExportLocal, d.messageExportLocal)
	d.handle(rpc.MethodMessageComments, d.messageComments)
	d.handle(rpc.MethodMessageLink, d.messageLink)
	d.handle(rpc.MethodChatDraftGet, d.draftGet)
	d.handle(rpc.MethodChatDraftList, d.draftList)
	d.handle(rpc.MethodChatDraftSet, d.draftSet)
	d.handle(rpc.MethodChatDraftClear, d.draftClear)
	d.handle(rpc.MethodChatTyping, d.chatTyping)
	d.handle(rpc.MethodChatMarkRead, d.chatMarkRead)
	d.handle(rpc.MethodNoteAdd, d.noteAdd)
	d.handle(rpc.MethodNoteList, d.noteList)
	d.handle(rpc.MethodNoteDelete, d.noteDelete)
	d.handle(rpc.MethodNoteDeleteByAuthor, d.noteDeleteByAuthor)
	d.handle(rpc.MethodSearch, d.search)
	d.handle(rpc.MethodHistorySyncStart, d.historySyncStart)
	d.handle(rpc.MethodHistorySyncStatus, d.historySyncStatus)
	d.handle(rpc.MethodHistorySyncCancel, d.historySyncCancel)
	d.registerOwnerState()
	d.registerUsers()
	d.registerChatAdmin()
	d.registerUnread()
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
	if policy := d.policy(); policy.Enabled() || policy.WriteCount() > 0 || policy.AdminCount() > 0 || policy.CreateAllowed() {
		st.Access = &rpc.AccessStatus{Enabled: policy.Enabled(), Allow: policy.AllowCount(), Block: policy.BlockCount(), Write: policy.WriteCount(), Admin: policy.AdminCount(), Create: policy.CreateAllowed(), Closed: policy.Closed()}
	}
	if d.fillConnection(&st) {
		return st, nil
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
	if rpc.NeedsConnectionWait(ctx) {
		if waiter, ok := d.opts.Telegram.(interface {
			WaitConnected(context.Context, time.Duration) error
		}); ok {
			if err := waiter.WaitConnected(ctx, connectionWait); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return rpc.NewError(rpc.CodeOffline, "telegram is offline")
			}
		}
	}
	select {
	case <-d.opts.Telegram.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}
	user, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return err
	}
	d.account.set(user)
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
	policy := d.policy()
	requested := limit
	chats, err := d.opts.Telegram.Chats(ctx, requested, p.Type)
	if err != nil {
		return nil, err
	}
	if policy.Enabled() {
		for {
			visible := make([]rpc.Chat, 0, len(chats))
			for _, chat := range chats {
				if policy.Allowed(chat.ID) {
					visible = append(visible, chat)
				}
			}
			if len(visible) >= limit || len(chats) < requested || requested == maxChatLimit {
				chats = visible[:min(len(visible), limit)]
				break
			}
			requested = min(requested*2, maxChatLimit)
			chats, err = d.opts.Telegram.Chats(ctx, requested, p.Type)
			if err != nil {
				return nil, err
			}
		}
	}
	if chats == nil {
		chats = []rpc.Chat{}
	}
	d.saveChats(ctx, rpc.MethodChatList, chats, true)
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
