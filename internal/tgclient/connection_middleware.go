package tgclient

import (
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
	"time"
)

func (c *Client) connectionMiddleware() telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
			if rpc.NeedsConnectionWait(ctx) {
				if err := c.WaitConnected(ctx, 15*time.Second); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if errors.Is(err, context.DeadlineExceeded) {
						return rpc.NewError(rpc.CodeOffline, "telegram is offline")
					}
					return err
				}
			}
			return next.Invoke(ctx, in, out)
		}
	})
}
