package tgclient

import (
	"context"

	"tg-cli/internal/rpc"
)

func (c *Client) CacheJoinRequestUsers(ctx context.Context, requests []rpc.JoinRequest) error {
	if len(requests) == 0 {
		return nil
	}
	m, err := c.manager(ctx)
	if err != nil {
		return err
	}
	self, err := c.Self(ctx)
	if err != nil {
		return err
	}
	if self == nil {
		return rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	for _, r := range requests {
		if r.AccessHash != 0 {
			if err := m.SetUserAccessHash(ctx, self.ID, r.UserID, r.AccessHash); err != nil {
				return mapError(err)
			}
		}
	}
	return nil
}
