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
	"sync/atomic"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/ctxlock"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
)

// ErrAlreadyRunning means another daemon holds the lock of the profile.
var ErrAlreadyRunning = errors.New("a daemon is already running for this profile")

// ErrDatabaseInUse means another process holds the database of the profile. It
// happens when the same profile name is used with two data directories.
var ErrDatabaseInUse = errors.New("the profile database is in use")

const (
	defaultChatLimit = 50
	maxChatLimit     = 500
	stopGrace        = 5 * time.Second
	statusTimeout    = 10 * time.Second
)

// Options configure Run.
type Options struct {
	// ClientPreset is the profile preset included in session exports.
	ClientPreset string
	Profile      string
	Paths        Paths
	Telegram     Telegram
	// NewTelegram constructs a replacement for the single-use client.
	NewTelegram    func(context.Context, TelegramConfig) (Telegram, error)
	TelegramConfig TelegramConfig
	Store          *store.DB
	Access         *access.Loader
	Log            *slog.Logger
	// FileWarningsReported suppresses warnings already shown by the caller.
	FileWarningsReported bool
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
	admission            telegramAdmission
	supervisor           *clientSupervisor
	auto                 *autoTransport
	account              accountCache
	authLostMu           sync.RWMutex
	authLost             string
	idempotencyLocks     keyLocks
	transcriptionLocks   keyLocks
	phoneLimit           phoneLimiter
	phoneResolveMu       sync.Mutex
	opts                 Options
	log                  *slog.Logger
	srv                  *rpc.Server
	hub                  *rpc.Hub
	login                *loginManager
	store                *store.DB
	sync                 *historySyncManager
	cancel               context.CancelFunc
	once                 sync.Once
	eventMu              sync.Mutex
	deletionsMu          sync.Mutex
	contactsMu           ctxlock.Lock
	inlineMu             sync.Mutex
	transcriptionMu      sync.Mutex
	transcriptionWaiters map[transcriptionKey]*transcriptionWaiter
	inlineQueries        map[string]inlineQueryEntry
	addEvent             func(context.Context, store.Event) (int64, bool, error)
	findMessageChats     func(context.Context, []int) ([]int64, error)
	markMessagesDeleted  func(context.Context, int64, []int) (int, error)
	openMediaFile        func([]string, string, int64) (*os.File, int64, error)
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
	warnings, err := CheckFileNames(opts.Paths)
	if err != nil {
		return err
	}
	if !opts.FileWarningsReported {
		for _, warning := range warnings {
			log.Warn(warning)
		}
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
	// Everything about the database runs under two locks: the profile lock, which
	// the old and the new binary share, and the database lock, which a second
	// data directory with the same profile name cannot take.
	//
	// The record of the data directory is read before the old database is moved:
	// a move deletes a file, so a start of the wrong data directory must stop
	// before it.
	releaseDatabase, err := LockDatabase(opts.Paths)
	if err != nil {
		return err
	}
	defer releaseDatabase()
	if err := CheckDatabaseBinding(opts.Paths); err != nil {
		return err
	}
	moved, err := MoveLegacyDatabase(opts.Paths)
	if err != nil {
		return err
	}
	if err := BindDatabase(opts.Paths); err != nil {
		return err
	}
	if _, err := CheckFileNames(opts.Paths); err != nil {
		return err
	}
	releaseStart, err := guardSessionStart(opts.Paths)
	if err != nil {
		return err
	}
	releaseStart()
	ownedStore := opts.Store
	if ownedStore == nil {
		ownedStore, err = store.Open(opts.Paths.Database)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer ownedStore.Close()
	}
	if moved {
		log.Info("moved the database", "from", opts.Paths.LegacyDatabase, "to", opts.Paths.Database)
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
	d.admission.unblock(opts.Telegram)
	d.sync = newHistorySyncManager(runCtx, d, nil)
	// A daemon that has just started is not idle: the clock runs from now.
	d.lastActivity.Store(time.Now().UnixNano())
	d.login = newLoginManager(runCtx, opts.Telegram)
	if opts.NewTelegram != nil {
		d.login.acquire = d.acquireTelegram
	}
	authorized := make(chan struct{}, 1)
	d.login.onUser = d.account.set
	d.login.onAuthorized = func() error { return d.onAuthorized(runCtx, authorized) }
	if source, ok := opts.Telegram.(interface{ SetAuthLostSink(func(string)) }); ok {
		source.SetAuthLostSink(d.setAuthLost)
	}
	d.srv.SetRequestGuard(d.guardRequest)
	contactsDone := make(chan struct{})
	accountDone := make(chan struct{})

	d.register()
	d.srv.SetEventSource(d.hub, d.replayEvents)
	opts.Telegram.SetEventSink(d.handleEvent)
	if client, ok := opts.Telegram.(transcriptionTelegram); ok {
		client.SetTranscriptionSink(d.handleTranscription)
	}
	if _, err := d.store.PruneIdempotency(runCtx, time.Now()); err != nil {
		d.log.Warn("idempotency cleanup failed")
	}
	idempotencyDone := make(chan struct{})
	go func() { defer close(idempotencyDone); d.pruneIdempotency(runCtx) }()
	pruneDone := make(chan struct{})
	go func() { defer close(pruneDone); d.pruneEvents(runCtx, eventPruneEvery, eventRetention, eventKeep) }()

	var tgDone <-chan error
	switchDone := make(chan struct{})
	autoDone := make(chan struct{})
	if opts.NewTelegram != nil {
		autoReady := make(chan struct{})
		var readyOnce sync.Once
		auto := newAutoTransport(opts.TelegramConfig.Network, autoReady)
		d.auto = auto
		factory := func(ctx context.Context) (Telegram, error) {
			cfg := opts.TelegramConfig
			cfg.Network = auto.network()
			return opts.NewTelegram(ctx, cfg)
		}
		switchRequests := make(chan struct{}, 1)
		switchClient := func() {
			select {
			case switchRequests <- struct{}{}:
			default:
			}
		}
		onFailure := func() bool {
			if auto.switchOnFailure() {
				switchClient()
				return true
			}
			return auto.hasSwitched()
		}
		observeConnection := func(state rpc.ConnectionInfo) {
			if state.Connection == "disconnected" && d.getAuthLost() == "" {
				onFailure()
			}
		}
		d.logConnection(runCtx, opts.Telegram, observeConnection)
		prepare := func(client Telegram) {
			client.SetEventSink(d.handleEvent)
			if source, ok := client.(interface{ SetAuthLostSink(func(string)) }); ok {
				source.SetAuthLostSink(d.setAuthLost)
			}
			if source, ok := client.(transcriptionTelegram); ok {
				source.SetTranscriptionSink(d.handleTranscription)
			}
			d.logConnection(runCtx, client, observeConnection)
		}
		d.supervisor = newClientSupervisor(runCtx, &d.admission, opts.Telegram, factory, prepare, func() bool { return d.getAuthLost() != "" }, onFailure, func() { readyOnce.Do(func() { close(autoReady) }) }, 30*time.Second)
		go func() {
			defer close(switchDone)
			for {
				select {
				case <-runCtx.Done():
					return
				case <-switchRequests:
					if err := d.supervisor.restart(runCtx); err != nil && runCtx.Err() == nil {
						d.log.Warn("telegram transport switch failed")
					}
				}
			}
		}()
		if opts.TelegramConfig.Network.Transport == netconf.Auto {
			go func() {
				defer close(autoDone)
				timer := time.NewTimer(10 * time.Second)
				defer timer.Stop()
				auto.watch(runCtx, timer.C, switchClient)
			}()
		} else {
			close(autoDone)
		}
		tgDone = d.supervisor.exits
	} else {
		d.logConnection(runCtx, opts.Telegram, nil)
		close(switchDone)
		close(autoDone)
		done := make(chan error, 1)
		go func() { done <- opts.Telegram.Run(runCtx) }()
		tgDone = done
	}
	go func() {
		defer close(accountDone)
		client := opts.Telegram
		leaseCtx := runCtx
		release := func() {}
		if d.supervisor != nil {
			var err error
			leaseCtx, client, release, err = d.supervisor.acquireReady(runCtx)
			if err != nil {
				return
			}
		} else {
			select {
			case <-runCtx.Done():
				return
			case <-client.Ready():
			}
		}
		defer release()
		user, err := client.Self(leaseCtx)
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
		contactsCtx := runCtx
		if opts.NewTelegram != nil {
			leaseCtx, client, release, err := d.acquireTelegram(runCtx)
			if err != nil {
				return
			}
			defer release()
			contactsCtx = context.WithValue(leaseCtx, telegramContextKey{}, client)
		}
		if err := d.contactsMu.Acquire(contactsCtx); err != nil {
			return
		}
		defer d.contactsMu.Release()
		if _, err := d.syncContacts(throttle.WithBackground(contactsCtx)); err != nil && !errors.Is(err, context.Canceled) {
			d.log.Warn("contact sync failed")
		}
	}()
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

	runErr, tgFinished := d.waitForShutdown(runCtx, tgDone, serveDone)

	log.Info("daemon stopping")
	cancel()
	d.sync.close()
	// Join the idle monitor: a goroutine that outlived the shutdown would keep
	// the process alive after Run returned.
	<-idleDone
	<-switchDone
	<-autoDone
	<-pruneDone
	<-contactsDone
	<-idempotencyDone
	<-accountDone
	stopCtx, stopCancel := context.WithTimeout(context.Background(), stopGrace)
	defer stopCancel()
	if err := d.srv.Shutdown(stopCtx); err != nil {
		log.Warn("forced server close", "err", err)
	}
	if d.supervisor != nil {
		if err := d.supervisor.wait(stopCtx); err != nil {
			log.Warn("telegram client did not stop in time")
		}
	} else if !tgFinished {
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
		if needsTelegram(method) && d.opts.NewTelegram != nil {
			leasedCtx, client, release, err := d.acquireTelegram(ctx)
			if err != nil {
				return nil, err
			}
			defer release()
			ctx = context.WithValue(leasedCtx, telegramContextKey{}, client)
		}
		return h(ctx, params)
	}))
}

func (d *daemon) register() {
	d.handle(rpc.MethodSessionExport, d.sessionExport)
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
	d.handle(rpc.MethodChatStats, d.chatStats)
	d.handle(rpc.MethodChatBoosts, d.chatBoosts)
	d.handle(rpc.MethodChatInviteInfo, d.chatInviteInfo)
	d.handle(rpc.MethodMessageHistory, d.messageHistory)
	d.handle(rpc.MethodMessageGet, d.messageGet)
	d.handle(rpc.MethodMessageReaders, d.messageReaders)
	d.handle(rpc.MethodMessageTranscribe, d.messageTranscribe)
	d.handle(rpc.MethodMessageTranslate, d.messageTranslate)
	d.srv.SetMethodTimeout(rpc.MethodMessageTranscribe, 100*time.Second)
	d.handle(rpc.MethodAccountProfile, d.accountProfile)
	d.handle(rpc.MethodAccountPrivacyGet, d.accountPrivacyGet)
	d.handle(rpc.MethodAccountPrivacySet, d.accountPrivacySet)
	d.handle(rpc.MethodAccountSet, d.accountSet)
	d.handle(rpc.MethodChatSet, d.chatSet)
	d.handle(rpc.MethodChatRequests, d.chatRequests)
	d.handle(rpc.MethodChatApprove, d.approveJoinRequest)
	d.handle(rpc.MethodChatDecline, d.declineJoinRequest)
	d.handle(rpc.MethodAccountEmojiStatuses, d.accountEmojiStatuses)
	d.handle(rpc.MethodMessageDownload, d.messageDownload)
	d.srv.SetMethodTimeout(rpc.MethodMessageDownload, 600*time.Second)
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
	d.handle(rpc.MethodMessageExportMonth, d.messageExportMonth)
	d.srv.SetMethodTimeout(rpc.MethodMessageExportMonth, 600*time.Second)
	d.handle(rpc.MethodMessageExportVerify, d.messageExportVerify)
	d.srv.SetMethodTimeout(rpc.MethodMessageExportVerify, 600*time.Second)
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
	d.registerDialogs()
	d.registerSaved()
	d.registerUsers()
	d.registerChatAdmin()
	d.registerUnread()
	d.registerMessageExtras()
}

func (d *daemon) ping(context.Context, json.RawMessage) (any, error) {
	return rpc.PingResult{Pong: true}, nil
}

func (d *daemon) status(ctx context.Context, _ json.RawMessage) (any, error) {
	st := rpc.StatusResult{
		Profile:    d.opts.Profile,
		PID:        os.Getpid(),
		LoginState: d.login.state(),
		AuthLost:   d.getAuthLost(),
		// The status request itself is active while it runs, do not count it.
		ActiveRequests: max(d.srv.Active()-1, 0),
	}
	if policy := d.policy(); policy.Enabled() || policy.WriteCount() > 0 || policy.AdminCount() > 0 || policy.CreateAllowed() || policy.ProfileAllowed() || policy.DialogsAllowed() || policy.SessionsAllowed() {
		st.Access = &rpc.AccessStatus{Enabled: policy.Enabled(), Allow: policy.AllowCount(), Block: policy.BlockCount(), Write: policy.WriteCount(), Admin: policy.AdminCount(), Create: policy.CreateAllowed(), Profile: policy.ProfileAllowed(), Dialogs: policy.DialogsAllowed(), Sessions: policy.SessionsAllowed(), Closed: policy.Closed()}
	}
	if st.AuthLost != "" {
		if d.opts.NewTelegram != nil && d.admission.isBlocked() {
			info := d.unavailableConnection()
			st.ConnectionInfo = &info
		}
		return st, nil
	}
	if d.opts.NewTelegram != nil {
		leasedCtx, client, release, err := d.acquireTelegram(ctx)
		if err != nil {
			info := d.unavailableConnection()
			st.ConnectionInfo = &info
			return st, nil
		}
		defer release()
		ctx = context.WithValue(leasedCtx, telegramContextKey{}, client)
	}
	if d.fillConnection(ctx, &st) {
		return st, nil
	}
	select {
	case <-d.telegram(ctx).Ready():
		st.Connected = true
	default:
		return st, nil
	}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	user, err := d.telegram(ctx).Self(ctx)
	if err != nil {
		return nil, err
	}
	st.Authorized = user != nil
	st.User = user
	return st, nil
}

func (d *daemon) unavailableConnection() rpc.ConnectionInfo {
	settings := d.opts.TelegramConfig.Network
	transport := string(settings.Transport)
	if transport == "" {
		transport = "tcp"
	}
	info := rpc.ConnectionInfo{Transport: transport, Proxy: settings.Proxy.Redacted(), Connection: d.unavailableState()}
	if d.auto != nil && d.auto.hasSwitched() {
		info.TransportSwitches = 1
	}
	return info
}

func (d *daemon) unavailableState() string {
	if d.supervisor != nil && d.supervisor.offline() {
		return "offline"
	}
	return "restarting"
}

func (d *daemon) shutdown(context.Context, json.RawMessage) (any, error) {
	d.once.Do(d.cancel)
	return rpc.ShutdownResult{Stopping: true}, nil
}

func (d *daemon) onAuthorized(ctx context.Context, authorized chan<- struct{}) error {
	if d.getAuthLost() != "" && d.supervisor != nil {
		if err := d.supervisor.restart(ctx); err != nil {
			return err
		}
	}
	d.setAuthLost("")
	select {
	case authorized <- struct{}{}:
	default:
	}
	return nil
}

func (d *daemon) authStart(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.AuthStartParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if d.getAuthLost() != "" && d.supervisor != nil {
		if err := d.supervisor.restart(ctx); err != nil {
			return nil, rpc.NewError(rpc.CodeOffline, "telegram is offline")
		}
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
	if d.getAuthLost() != "" && d.supervisor != nil {
		if err := d.supervisor.restart(ctx); err != nil {
			return nil, rpc.NewError(rpc.CodeOffline, "telegram is offline")
		}
	}
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
		if waiter, ok := d.telegram(ctx).(interface {
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
	case <-d.telegram(ctx).Ready():
	case <-ctx.Done():
		return ctx.Err()
	}
	user, err := d.telegram(ctx).Self(ctx)
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
	chats, err := d.telegram(ctx).Chats(ctx, requested, p.Type)
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
			chats, err = d.telegram(ctx).Chats(ctx, requested, p.Type)
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

func (d *daemon) setAuthLost(kind string) {
	d.authLostMu.Lock()
	d.authLost = kind
	d.authLostMu.Unlock()
}
func (d *daemon) getAuthLost() string {
	d.authLostMu.RLock()
	defer d.authLostMu.RUnlock()
	return d.authLost
}

// needsTelegram excludes operations that are fully served by local storage.
func needsTelegram(method string) bool {
	switch method {
	case rpc.MethodSearch, rpc.MethodNoteList, rpc.MethodNoteDelete, rpc.MethodNoteDeleteByAuthor,
		rpc.MethodMessageExportLocal, rpc.MethodMessageExportVerify,
		rpc.MethodHistorySyncStatus, rpc.MethodHistorySyncCancel:
		return false
	}
	return accountMethod(method) || method == rpc.MethodSessionExport
}

// guardRequest rejects account operations after authorization loss.
func (d *daemon) guardRequest(req rpc.Request) error {
	if d.opts.NewTelegram != nil && needsTelegram(req.Method) && d.admission.isBlocked() {
		return rpc.NewError(rpc.CodeOffline, "telegram is offline")
	}
	if kind := d.getAuthLost(); kind != "" && accountMethod(req.Method) {
		return rpc.NewError(rpc.CodeNotAuthorized, "authorization lost: "+kind+"; log in again")
	}
	return d.account.check(req)
}

// A permanently stopped Telegram client must not take the RPC server down
// after authorization loss: status and shutdown remain available.
func (d *daemon) waitForShutdown(ctx context.Context, tgDone, serveDone <-chan error) (error, bool) {
	tgFinished := false
	for {
		select {
		case <-ctx.Done():
			return nil, tgFinished
		case err := <-tgDone:
			tgFinished = true
			if d.getAuthLost() != "" {
				tgDone = nil
				continue
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("telegram client stopped: %w", err), true
			}
			return nil, true
		case err := <-serveDone:
			if err != nil && !errors.Is(err, rpc.ErrServerClosed) {
				return fmt.Errorf("serve: %w", err), tgFinished
			}
			return nil, tgFinished
		}
	}
}
