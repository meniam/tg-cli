package tgclient

import (
	"context"
	"errors"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
)

// mapError turns a gotd error into an *rpc.Error with a specific code.
// Context errors and errors that already are *rpc.Error pass through.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return &rpc.Error{
			Code:    rpc.CodeFloodWait,
			Message: "FLOOD_WAIT: wait before the next request",
			Data:    map[string]any{"retry_after_seconds": int(d.Seconds())},
		}
	}
	if auth.IsUnauthorized(err) {
		return rpc.NewError(rpc.CodeNotAuthorized, "not logged in: run `auth login` first")
	}
	if isNotFound(err) {
		return notFound()
	}
	if tgErr, ok := tgerr.As(err); ok {
		return rpc.NewError(rpc.CodeTelegram, tgErr.Type)
	}
	return rpc.NewError(rpc.CodeTelegram, "telegram request failed: "+err.Error())
}
