package tgclient

import (
	"context"
	"strings"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// Config is what the client needs to connect.
type Config struct {
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
	appID     int
	appHash   string
	tg        *telegram.Client
	ready     chan struct{}
	readyOnce sync.Once
	authReady chan struct{}
	updates   *updates.Manager
	stateKV   StateKV
	sinkMu    sync.RWMutex
	sink      func(rpc.Event)

	mgr         *peers.Manager // resolves chat references to Telegram peers
	mgrMu       sync.Mutex
	mgrReady    bool
	preloadMu   sync.Mutex
	preloadDone chan struct{}
	preloaded   bool // the first dialogs were read to fill the peer cache
}

// New creates a client. It does not connect until Run is called.
func New(cfg Config) *Client {
	c := &Client{appID: cfg.AppID, appHash: cfg.AppHash, ready: make(chan struct{}), authReady: make(chan struct{}, 1), stateKV: cfg.StateKV}
	options := telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{Path: cfg.SessionPath},
		Device:         deviceConfig(cfg.DeviceModel),
		UpdateHandler:  managerHandler{client: c},
	}
	if cfg.Throttle != nil {
		options.Middlewares = []telegram.Middleware{throttleMiddleware(cfg.Throttle)}
	}
	c.tg = telegram.NewClient(cfg.AppID, cfg.AppHash, options)
	c.mgr = peers.Options{Storage: &peers.InmemoryStorage{}}.Build(c.tg.API())
	c.updates = updates.New(c.updateConfig())
	return c
}

// Run connects and keeps the connection until ctx ends. It is single-use.
func (c *Client) Run(ctx context.Context) error {
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
				return c.updates.Run(ctx, c.tg.API(), user.ID, updates.AuthOptions{Forget: false})
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
func (c *Client) Chats(ctx context.Context, limit int, chatType string) ([]rpc.Chat, error) {
	return c.chats(ctx, limit, chatType, false)
}

func (c *Client) chats(ctx context.Context, limit int, chatType string, requireCache bool) ([]rpc.Chat, error) {
	m, err := c.manager(ctx)
	if err != nil {
		return nil, err
	}
	it := query.GetDialogs(c.tg.API()).BatchSize(min(limit, 100)).Iter()
	chats := make([]rpc.Chat, 0, min(limit, 100))
	users := map[int64]tg.UserClass{}
	var groups []tg.ChatClass
	seen := map[int64]bool{}
	listed := map[int64]bool{} // the dialog iterator may repeat a boundary dialog between pages
	for len(chats) < limit && it.Next(ctx) {
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
		if chat, ok := toChat(elem); ok && (chatType == "" || chat.Type == chatType) && !listed[chat.ID] {
			listed[chat.ID] = true
			chats = append(chats, chat)
		}
	}
	if err := it.Err(); err != nil {
		return nil, mapError(err)
	}
	userList := make([]tg.UserClass, 0, len(users))
	for _, u := range users {
		userList = append(userList, u)
	}
	// A failed cache update must not fail the listing.
	if err := m.Apply(ctx, userList, groups); err != nil && requireCache {
		return nil, mapError(err)
	}
	return chats, nil
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
