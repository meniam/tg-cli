package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func (c *Client) TerminateSession(ctx context.Context, hash int64) error {
	return terminateSessionAPI(ctx, c.tg.API(), hash)
}

func terminateSessionAPI(ctx context.Context, api *tg.Client, hash int64) error {
	ok, err := api.AccountResetAuthorization(ctx, hash)
	if tgerr.Is(err, "FRESH_RESET_AUTHORISATION_FORBIDDEN") {
		return rpc.NewError(rpc.CodeTelegram, "this session is too new to terminate another session; try again after 24 hours")
	}
	if err != nil {
		return mapError(err)
	}
	if !ok {
		return rpc.NewError(rpc.CodeTelegram, "Telegram rejected session termination")
	}
	return nil
}
