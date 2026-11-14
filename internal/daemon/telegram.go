package daemon

import (
	"context"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// Telegram is what the daemon needs from a Telegram client. It is declared
// here, by the consumer, so tests can replace the real client.
//
// Methods return *rpc.Error for failures the caller should see with a
// specific code (not authorized, flood wait, Telegram errors).
type Telegram interface {
	// Run connects and keeps the connection until ctx ends. It returns
	// ctx.Err() on a clean stop.
	Run(ctx context.Context) error
	SetEventSink(func(rpc.Event))
	// Ready is closed once the client is connected.
	Ready() <-chan struct{}
	// Self returns the logged-in account, or nil when there is none.
	Self(ctx context.Context) (*rpc.User, error)
	// Login runs the phone login. code and password are asked when Telegram
	// requires them and block until the user provides a value. code receives
	// where Telegram delivered the code and returns either the code or a
	// request to resend it another way; Login calls it again after a resend.
	Login(ctx context.Context, phone string, code func(ctx context.Context, prompt rpc.CodePrompt) (code string, resend bool, err error), password func(context.Context) (string, error)) error
	// LoginQR runs the QR login. show is called with every new token the user
	// has to scan (the token refreshes while it waits); password is called when
	// the account has two-factor authentication.
	LoginQR(ctx context.Context, show func(ctx context.Context, token rpc.QRToken) error, password func(context.Context) (string, error)) error
	// Chats returns up to limit dialogs, newest first. A chat type (user, bot,
	// group, supergroup, channel) keeps only the dialogs of that type.
	Chats(ctx context.Context, limit int, chatType string) ([]rpc.Chat, error)

	// The methods below only read. They return *rpc.Error with CodeNotFound
	// when the chat or message does not exist or is not visible.

	// Resolve finds a chat by a reference.
	Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error)
	// ChatInfo returns a chat with its description and size.
	ChatInfo(ctx context.Context, ref chatref.Ref) (rpc.ChatInfo, error)
	// InviteInfo shows an invite link without joining the chat.
	InviteInfo(ctx context.Context, ref chatref.Ref) (rpc.InviteInfo, error)
	// History reads a chat from the newest message to the oldest.
	History(ctx context.Context, ref chatref.Ref, q rpc.HistoryQuery) (rpc.MessagesResult, error)
	// Message returns one message of a chat.
	Message(ctx context.Context, ref chatref.Ref, id int) (rpc.Message, error)
	// Pinned returns up to limit pinned messages of a chat.
	Pinned(ctx context.Context, ref chatref.Ref, limit int) (rpc.MessagesResult, error)
	// Writes are called only after the Daemon has checked the access policy.
	Send(ctx context.Context, ref chatref.Ref, p rpc.SendParams) (rpc.SendResult, error)
	Edit(ctx context.Context, ref chatref.Ref, p rpc.EditParams) (rpc.SendResult, error)
	Delete(ctx context.Context, ref chatref.Ref, p rpc.DeleteParams) (rpc.DeleteResult, error)
	Forward(ctx context.Context, from, to chatref.Ref, p rpc.ForwardParams) (rpc.ForwardResult, error)
}

// TelegramConfig is what a Telegram client needs to be created.
type TelegramConfig struct {
	AppID       int
	AppHash     string
	SessionPath string
	// StatePath is the profile database used for update checkpoints.
	StatePath string
	// DeviceModel is the device name Telegram shows for this session.
	DeviceModel string
	// Throttle limits this account's Telegram requests when set.
	Throttle *throttle.Limiter
}
