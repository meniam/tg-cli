package tgclient

import (
	"context"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func (c *Client) ResolvePhone(ctx context.Context, phone string) (rpc.Chat, error) {
	m, err := c.manager(ctx)
	if err != nil {
		return rpc.Chat{}, err
	}
	return resolvePhoneAPI(ctx, c.tg.API(), m, phone)
}
func resolvePhoneAPI(ctx context.Context, api *tg.Client, m *peers.Manager, phone string) (rpc.Chat, error) {
	r, err := api.ContactsResolvePhone(ctx, phone)
	if err != nil {
		return rpc.Chat{}, mapError(err)
	}
	peer, ok := r.Peer.(*tg.PeerUser)
	if !ok {
		return rpc.Chat{}, rpc.ErrChatNotFound()
	}
	if err := m.Apply(ctx, r.Users, r.Chats); err != nil {
		return rpc.Chat{}, mapError(err)
	}
	for _, raw := range r.Users {
		if u, ok := raw.(*tg.User); ok && u.ID == peer.UserID {
			return toChatOf(m.User(u)), nil
		}
	}
	return rpc.Chat{}, rpc.ErrChatNotFound()
}
