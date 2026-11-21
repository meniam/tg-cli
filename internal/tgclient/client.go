package tgclient

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"tg-cli/internal/ctxlock"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// Config is what the client needs to connect.
type Config struct {
	Network netconf.Settings
	// AppID and AppHash identify the application, see https://my.telegram.org.
	AppID   int
	AppHash string
	// SessionPath is the session file. It is a secret and is written with mode 0600.
	SessionPath string
	// DeviceModel is the device name Telegram shows for the session, in the
	// list of devices. DefaultDeviceModel when empty.
	DeviceModel string
	// Throttle limits RPC requests when set. A nil value preserves the old behavior.
	Throttle *throttle.Limiter
	// StateKV persists update cursors; nil keeps them in memory.
	StateKV StateKV
}

// DefaultDeviceModel is the device name when none is configured. It is the
// owner's choice (the name of their machine as they want it in the list of
// Telegram devices); --device overrides it.
const DefaultDeviceModel = "MacBook Pro M1 BRN"

// deviceConfig is what the client tells Telegram about the device. Only the
// model is ours; the other fields keep the library defaults.
func deviceConfig(model string) telegram.DeviceConfig {
	if model == "" {
		model = DefaultDeviceModel
	}
	return telegram.DeviceConfig{DeviceModel: model}
}

// Client is a Telegram client for one account.
type Client struct {
	connection        *connectionState
	appID             int
	appHash           string
	sessionStorage    *telegram.FileSessionStorage
	tg                *telegram.Client
	ready             chan struct{}
	readyOnce         sync.Once
	authReady         chan struct{}
	updates           *updates.Manager
	stateKV           StateKV
	throttle          *throttle.Limiter
	transfers         *transfers
	sinkMu            sync.RWMutex
	sink              func(rpc.Event)
	transcriptionSink func(rpc.Transcription)
	authLostMu        sync.RWMutex
	authLostSink      func(string)

	mgr         *peers.Manager // resolves chat references to Telegram peers
	mgrMu       ctxlock.Lock
	mgrReady    bool
	preloadMu   sync.Mutex
	preloadDone chan struct{}
	preloaded   bool // the first dialogs were read to fill the peer cache
}

// New creates a client. It does not connect until Run is called.
func New(cfg Config) *Client {
	c, _ := NewWithNetwork(cfg)
	return c
}

func NewWithNetwork(cfg Config) (*Client, error) {
	connection := newConnection(cfg.Network)
	resolver, err := resolverForState(cfg.Network, connection)
	if err != nil {
		return nil, err
	}
	c := &Client{connection: connection, appID: cfg.AppID, appHash: cfg.AppHash, ready: make(chan struct{}), authReady: make(chan struct{}, 1), stateKV: cfg.StateKV, throttle: cfg.Throttle}
	c.transfers = newTransfers()
	c.sessionStorage = &telegram.FileSessionStorage{Path: cfg.SessionPath}
	options := telegram.Options{
		Resolver:               resolver,
		FilePoolSize:           4,
		OnConnectionStateEvent: c.connection.update,
		SessionStorage:         c.sessionStorage,
		OnAuthLost: func(_ context.Context, err error) error {
			c.notifyAuthLost(err)
			return nil
		},
		Device:        deviceConfig(cfg.DeviceModel),
		UpdateHandler: managerHandler{client: c},
	}
	options.Middlewares = []telegram.Middleware{c.connectionMiddleware()}
	if cfg.Throttle != nil {
		options.Middlewares = append(options.Middlewares, throttleMiddleware(cfg.Throttle))
	}
	c.tg = telegram.NewClient(cfg.AppID, cfg.AppHash, options)
	c.mgr = peers.Options{Storage: &peers.InmemoryStorage{}}.Build(tg.NewClient(fullRawInvoker{next: c.tg.API().Invoker()}))
	c.updates = updates.New(c.updateConfig())
	return c, nil
}

// FloodWaitRemaining reports the current account-wide flood gate delay.
func (c *Client) FloodWaitRemaining() time.Duration {
	if c.throttle == nil {
		return 0
	}
	return c.throttle.PausedFor()
}

// ThrottleQueued reports how many requests wait in the account throttle.
func (c *Client) ThrottleQueued() int {
	if c.throttle == nil {
		return 0
	}
	return c.throttle.Queued()
}

// Run connects and keeps the connection until ctx ends. It is single-use.
func (c *Client) Run(ctx context.Context) error {
	catchupCtx, cancelCatchup := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); c.runCatchup(catchupCtx) }()
	defer func() { cancelCatchup(); <-done }()
	return c.tg.Run(ctx, func(ctx context.Context) error {
		c.readyOnce.Do(func() { close(c.ready) })
		for {
			user, err := c.Self(ctx)
			if err != nil {
				return err
			}
			if user != nil {
				if _, err := c.manager(ctx); err != nil {
					return err
				}
				return c.updates.Run(ctx, c.tg.API(), user.ID, updates.AuthOptions{Forget: false, OnStart: func(context.Context) { c.connection.mu.Lock(); c.connection.started = true; c.connection.mu.Unlock() }})
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.authReady:
			}
		}
	})
}

// SetEventSink installs a receiver before Run. The callback is invoked without
// holding the sink lock; it should finish promptly to keep updates moving.
func (c *Client) SetEventSink(sink func(rpc.Event)) {
	c.sinkMu.Lock()
	c.sink = sink
	c.sinkMu.Unlock()
}

func (c *Client) emit(event rpc.Event) {
	c.sinkMu.RLock()
	sink := c.sink
	c.sinkMu.RUnlock()
	if sink != nil {
		sink(event)
	}
}

func (c *Client) signalAuthorized() {
	select {
	case c.authReady <- struct{}{}:
	default:
	}
}

// Ready is closed once the client is connected.
func (c *Client) Ready() <-chan struct{} { return c.ready }

// Self returns the logged-in account, or nil when the session is not authorized.
func (c *Client) Self(ctx context.Context) (*rpc.User, error) {
	st, err := c.tg.Auth().Status(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	if !st.Authorized || st.User == nil {
		return nil, nil
	}
	return toUser(st.User), nil
}

// Chats returns up to limit dialogs of the account, newest first. A chat type
// (user, bot, group, supergroup, channel) keeps only the dialogs of that type.
// Every dialog read also teaches the peer cache, so that references to these
// chats by id resolve later.
func (c *Client) Chats(ctx context.Context, limit int, chatType string, after rpc.DialogOffset, allowed func(int64) bool) ([]rpc.Chat, *rpc.DialogOffset, error) {
	return c.dialogPage(ctx, limit, chatType, after, allowed, false)
}

func (c *Client) chats(ctx context.Context, limit int, chatType string, requireCache bool) ([]rpc.Chat, error) {
	chats, _, err := c.dialogPage(ctx, limit, chatType, rpc.DialogOffset{}, nil, requireCache)
	return chats, err
}

func (c *Client) dialogPage(ctx context.Context, limit int, chatType string, after rpc.DialogOffset, allowed func(int64) bool, requireCache bool) ([]rpc.Chat, *rpc.DialogOffset, error) {
	m, err := c.manager(ctx)
	if err != nil {
		return nil, nil, err
	}
	q := query.GetDialogs(c.tg.API())
	var it *dialogs.Iterator
	if allowed == nil {
		it = q.BatchSize(min(limit, 100)).Iter()
	} else {
		it = chatDialogIterator(q, 100)
	}
	return readDialogPage(ctx, it, limit, chatType, after, allowed, func(users []tg.UserClass, groups []tg.ChatClass) error {
		err := m.Apply(ctx, users, groups)
		if requireCache {
			return err
		}
		return nil
	})
}

// The iterator counts raw rows against the reported total. An inclusive
// boundary counts twice and can stop it before the last distinct dialog.
func chatDialogIterator(q dialogs.Query, batch int) *dialogs.Iterator {
	return dialogs.NewIterator(dialogs.QueryFunc(func(ctx context.Context, req dialogs.Request) (tg.MessagesDialogsClass, error) {
		result, err := q.Query(ctx, req)
		if page, ok := result.(*tg.MessagesDialogsSlice); ok && err == nil {
			page.Count = 0 // Let the iterator stop on an empty or non-advancing page.
		}
		return result, err
	}), batch)
}

func readDialogPage(ctx context.Context, it *dialogs.Iterator, limit int, chatType string, after rpc.DialogOffset, allowed func(int64) bool, cache func([]tg.UserClass, []tg.ChatClass) error) ([]rpc.Chat, *rpc.DialogOffset, error) {
	it.OffsetDate(int(after.Date)).OffsetID(int(after.MessageID)).OffsetPeer(dialogOffsetPeer(after))
	position := after
	remaining := after.Skip
	var next *rpc.DialogOffset
	chats := make([]rpc.Chat, 0, min(limit, 100))
	users := map[int64]tg.UserClass{}
	var groups []tg.ChatClass
	seen := map[int64]bool{}
	listed := map[int64]bool{} // the dialog iterator may repeat a boundary dialog between pages
	for it.Next(ctx) {
		elem := it.Value()
		for id, u := range elem.Entities.Users() {
			users[id] = u
		}
		for id, g := range elem.Entities.Chats() {
			if !seen[id] {
				seen[id] = true
				groups = append(groups, g)
			}
		}
		for id, ch := range elem.Entities.Channels() {
			if !seen[-id] {
				seen[-id] = true
				groups = append(groups, ch)
			}
		}
		chat, ok := toChat(elem)
		if !ok || listed[chat.ID] {
			continue
		}
		dialog := elem.Dialog.(*tg.Dialog)
		// Telegram may repeat the boundary and the pinned prefix on resume.
		if after.PeerID != 0 && (chat.ID == after.PeerID || dialog.Pinned) {
			continue
		}
		listed[chat.ID] = true
		if remaining > 0 {
			remaining--
			continue
		}
		if (chatType == "" || chat.Type == chatType) && (allowed == nil || allowed(chat.ID)) {
			if len(chats) == limit {
				next = &position
				break
			}
			chats = append(chats, chat)
			if allowed == nil && len(chats) == limit {
				break
			}
		}
		// Pinned and message-less dialogs have no chronological offset. Replay
		// this run from the preceding anchor when a page ends inside it.
		if dialog.Pinned || elem.Last == nil {
			position.Skip++
			continue
		}
		position = rpc.DialogOffset{Date: int64(elem.Last.GetDate()), MessageID: int64(elem.Last.GetID()), PeerID: chat.ID}
		switch peer := elem.Entities.OffsetPeer(dialog.Peer).(type) {
		case *tg.InputPeerUser:
			position.AccessHash = peer.AccessHash
		case *tg.InputPeerChannel:
			position.AccessHash = peer.AccessHash
		}
	}
	if err := it.Err(); err != nil {
		return nil, nil, mapError(err)
	}
	userList := make([]tg.UserClass, 0, len(users))
	for _, u := range users {
		userList = append(userList, u)
	}
	if err := cache(userList, groups); err != nil {
		return nil, nil, mapError(err)
	}
	return chats, next, nil
}

func dialogOffsetPeer(offset rpc.DialogOffset) tg.InputPeerClass {
	switch {
	case offset.PeerID > 0:
		return &tg.InputPeerUser{UserID: offset.PeerID, AccessHash: offset.AccessHash}
	case offset.PeerID < -channelIDBase:
		return &tg.InputPeerChannel{ChannelID: -channelIDBase - offset.PeerID, AccessHash: offset.AccessHash}
	case offset.PeerID < 0:
		return &tg.InputPeerChat{ChatID: -offset.PeerID}
	default:
		return &tg.InputPeerEmpty{}
	}
}

func toUser(u *tg.User) *rpc.User {
	out := &rpc.User{ID: u.ID}
	out.Username, _ = u.GetUsername()
	out.FirstName, _ = u.GetFirstName()
	out.LastName, _ = u.GetLastName()
	return out
}

func displayName(u *tg.User) string {
	first, _ := u.GetFirstName()
	last, _ := u.GetLastName()
	if name := strings.TrimSpace(first + " " + last); name != "" {
		return name
	}
	username, _ := u.GetUsername()
	return username
}

// SetAuthLostSink reports authorization loss to the daemon.
func (c *Client) SetAuthLostSink(f func(string)) {
	c.authLostMu.Lock()
	defer c.authLostMu.Unlock()
	c.authLostSink = f
}

func (c *Client) notifyAuthLost(err error) {
	c.authLostMu.RLock()
	f := c.authLostSink
	c.authLostMu.RUnlock()
	if f != nil {
		f(telegram.AuthLostKindOf(err).String())
	}
}
