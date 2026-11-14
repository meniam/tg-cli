package tgclient

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
)

// qrPollInterval is how often QR login checks whether a token was scanned.
const qrPollInterval = 2 * time.Second

// LoginQR runs the QR login: show gets every new token and the user scans it
// in the Telegram app of a logged-in device. password is called when the
// account has two-factor authentication.
func (c *Client) LoginQR(ctx context.Context, show func(ctx context.Context, token rpc.QRToken) error, password func(context.Context) (string, error)) error {
	err := mapError(c.loginQR(ctx, show, password))
	if err == nil {
		c.signalAuthorized()
	}
	return err
}

func (c *Client) loginQR(ctx context.Context, show func(context.Context, rpc.QRToken) error, password func(context.Context) (string, error)) error {
	qr := qrlogin.NewQR(c.tg.API(), c.appID, c.appHash, qrlogin.Options{})
	var shown string
	for {
		res, err := qr.ExportResult(ctx)
		if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
			return c.qrPassword(ctx, password)
		}
		if err != nil {
			return err
		}
		switch {
		case res.Authorization != nil:
			return nil // the token was scanned and accepted
		case res.MigrateTo != nil:
			return c.qrMigrate(ctx, res.MigrateTo, password)
		}

		// A token stays the same until it expires: show it only when it changes.
		if url := res.Token.URL(); url != shown {
			shown = url
			token := rpc.QRToken{URL: url, ExpiresInSeconds: int(time.Until(res.Token.Expires()).Seconds())}
			if err := show(ctx, token); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(qrPollInterval):
		}
	}
}

// qrMigrate finishes a login of an account that lives in another data center.
func (c *Client) qrMigrate(ctx context.Context, to *tg.AuthLoginTokenMigrateTo, password func(context.Context) (string, error)) error {
	if err := c.tg.MigrateTo(ctx, to.DCID); err != nil {
		return err
	}
	res, err := c.tg.API().AuthImportLoginToken(ctx, to.Token)
	if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return c.qrPassword(ctx, password)
	}
	if err != nil {
		return err
	}
	if _, ok := res.(*tg.AuthLoginTokenSuccess); !ok {
		return errors.New("unexpected answer to the login token import")
	}
	return nil
}

func (c *Client) qrPassword(ctx context.Context, password func(context.Context) (string, error)) error {
	pw, err := password(ctx)
	if err != nil {
		return err
	}
	_, err = c.tg.Auth().Password(ctx, pw)
	return err
}
