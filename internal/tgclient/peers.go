package tgclient

import (
	"context"
	"errors"
	"strings"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// preloadLimit is how many dialogs are read once to teach the peer cache the
// chats the session has not seen since the daemon started.
const preloadLimit = 200

// notFoundErrors are the Telegram errors that mean a chat, user or message does
// not exist or is not visible to the account.
var notFoundErrors = []string{
	"USERNAME_NOT_OCCUPIED", "USERNAME_INVALID", "CHANNEL_INVALID", "CHANNEL_PRIVATE",
	"CHAT_ID_INVALID", "PEER_ID_INVALID", "USER_ID_INVALID", "PHONE_NOT_OCCUPIED",
	"INVITE_HASH_INVALID", "INVITE_HASH_EXPIRED", "INVITE_HASH_EMPTY", "MSG_ID_INVALID",
}

// isNotFound reports whether err means "no such chat, user or message".
func isNotFound(err error) bool {
	var byPeer *peers.PeerNotFoundError
	var byPhone *peers.PhoneNotFoundError
	if errors.As(err, &byPeer) || errors.As(err, &byPhone) || tgerr.Is(err, notFoundErrors...) {
		return true
	}
	// The peer manager has no typed error for a user whose access hash the
	// session never saw: Telegram then answers with an empty list.
	return strings.Contains(err.Error(), "got empty result for")
}

func notFound() error {
	return rpc.ErrChatNotFound()
}

// manager returns the peer manager, initialized on first use.
func (c *Client) manager(ctx context.Context) (*peers.Manager, error) {
	c.mgrMu.Lock()
	defer c.mgrMu.Unlock()
	if !c.mgrReady {
		if err := c.mgr.Init(ctx); err != nil {
			return nil, mapError(err)
		}
		c.mgrReady = true
	}
	return c.mgr, nil
}

// resolve finds the chat a reference points at.
func (c *Client) resolve(ctx context.Context, ref chatref.Ref) (peers.Peer, error) {
	m, err := c.manager(ctx)
	if err != nil {
		return nil, err
	}
	var p peers.Peer
	switch ref.Kind {
	case chatref.Username:
		p, err = m.Resolve(ctx, "@"+ref.Username)
	case chatref.Phone:
		var u peers.User
		u, err = m.ResolvePhone(ctx, "+"+ref.Phone)
		p = u
	case chatref.ID:
		p, err = m.ResolveTDLibID(ctx, constant.TDLibPeerID(ref.ID))
		if err != nil && isNotFound(err) && c.preloadDialogs(ctx) {
			p, err = m.ResolveTDLibID(ctx, constant.TDLibPeerID(ref.ID))
		}
	default:
		return nil, rpc.NewError(rpc.CodeInvalidParams, "an invite link points at a chat you have not joined: use chat invite-info")
	}
	if err != nil {
		if isNotFound(err) {
			if ref.Kind == chatref.ID {
				return nil, rpc.ErrUnknownChatID()
			}
			return nil, notFound()
		}
		return nil, mapError(err)
	}
	return p, nil
}

// preloadDialogs reads the first dialogs once, which fills the peer cache. It
// reports whether it did something new.
func (c *Client) preloadDialogs(ctx context.Context) bool {
	for {
		c.preloadMu.Lock()
		if c.preloaded {
			c.preloadMu.Unlock()
			return false
		}
		if done := c.preloadDone; done != nil {
			c.preloadMu.Unlock()
			select {
			case <-done:
				c.preloadMu.Lock()
				loaded := c.preloaded
				c.preloadMu.Unlock()
				if loaded {
					return true // this lookup can retry with the new cache
				}
				continue // the first attempt failed
			case <-ctx.Done():
				return false
			}
		}
		done := make(chan struct{})
		c.preloadDone = done
		c.preloadMu.Unlock()

		_, err := c.chats(ctx, preloadLimit, "", true)
		c.preloadMu.Lock()
		if err == nil {
			c.preloaded = true
		}
		c.preloadDone = nil
		close(done)
		c.preloadMu.Unlock()
		return err == nil
	}
}

// toChatOf converts a resolved peer.
func toChatOf(p peers.Peer) rpc.Chat {
	chat := rpc.Chat{ID: int64(p.TDLibPeerID()), Title: p.VisibleName()}
	chat.Username, _ = p.Username()
	chat.Type = peerType(p)
	return chat
}

func peerType(p peers.Peer) string {
	switch v := p.(type) {
	case peers.User:
		if v.Raw().Bot {
			return "bot"
		}
		return "user"
	case peers.Chat:
		return "group"
	case peers.Channel:
		if v.IsSupergroup() {
			return "supergroup"
		}
		return "channel"
	}
	return "chat"
}

// Resolve finds a chat by a reference.
func (c *Client) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.Chat{}, err
	}
	return toChatOf(p), nil
}

// ChatInfo returns a chat with its description and size.
func (c *Client) ChatInfo(ctx context.Context, ref chatref.Ref) (rpc.ChatInfo, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatInfo{}, err
	}
	base := toChatOf(p)
	info := rpc.ChatInfo{ID: base.ID, Type: base.Type, Title: base.Title, Username: base.Username, Verified: p.Verified()}

	switch v := p.(type) {
	case peers.User:
		if full, err := v.FullRaw(ctx); err != nil {
			return rpc.ChatInfo{}, mapInfoError(err)
		} else {
			info.About, _ = full.GetAbout()
		}
	case peers.Chat:
		info.Members = v.ParticipantsCount()
		if full, err := v.FullRaw(ctx); err != nil {
			return rpc.ChatInfo{}, mapInfoError(err)
		} else {
			info.About = full.About
		}
	case peers.Channel:
		info.Members = v.ParticipantsCount()
		info.Forum = v.Raw().Forum
		if full, err := v.FullRaw(ctx); err != nil {
			return rpc.ChatInfo{}, mapInfoError(err)
		} else {
			info.About = full.About
			if info.Members == 0 {
				info.Members, _ = full.GetParticipantsCount()
			}
		}
	}
	return info, nil
}

func mapInfoError(err error) error {
	if isNotFound(err) {
		return notFound()
	}
	return mapError(err)
}

// InviteInfo shows an invite link without joining the chat.
func (c *Client) InviteInfo(ctx context.Context, ref chatref.Ref) (rpc.InviteInfo, error) {
	if ref.Kind != chatref.Invite {
		return rpc.InviteInfo{}, rpc.NewError(rpc.CodeInvalidParams, "this is not an invite link: use chat info")
	}
	// messages.checkChatInvite only looks at the link, it does not join the chat.
	invite, err := c.tg.API().MessagesCheckChatInvite(ctx, ref.Invite)
	if err != nil {
		return rpc.InviteInfo{}, mapInfoError(err)
	}
	return toInviteInfo(invite), nil
}

func toInviteInfo(invite tg.ChatInviteClass) rpc.InviteInfo {
	switch v := invite.(type) {
	case *tg.ChatInviteAlready:
		info := rpc.InviteInfo{AlreadyMember: true, Type: "unknown"}
		switch chat := v.Chat.(type) {
		case *tg.Chat:
			info.Title, info.Type, info.Members = chat.Title, "group", chat.ParticipantsCount
		case *tg.Channel:
			info.Title, info.Members = chat.Title, chat.ParticipantsCount
			info.Type = "channel"
			if chat.Megagroup {
				info.Type = "supergroup"
			}
		}
		return info
	case *tg.ChatInvite:
		info := rpc.InviteInfo{Title: v.Title, Members: v.ParticipantsCount, RequestNeeded: v.RequestNeeded, Type: "group"}
		info.About, _ = v.GetAbout()
		switch {
		case v.Megagroup:
			info.Type = "supergroup"
		case v.Channel || v.Broadcast:
			info.Type = "channel"
		}
		return info
	case *tg.ChatInvitePeek:
		return rpc.InviteInfo{Type: "unknown"}
	}
	return rpc.InviteInfo{Type: "unknown"}
}
