package tgclient

import "context"

// SessionBytes reads the same storage object used by gotd, under its mutex.
func (c *Client) SessionBytes(ctx context.Context) ([]byte, error) {
	return c.sessionStorage.LoadSession(ctx)
}
