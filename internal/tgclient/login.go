package tgclient

import (
	"context"
	"errors"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

// Login runs the phone login. code is called when Telegram wants the login
// code and may block until the user answers; it can also ask for a resend
// through another channel. password is called when two-factor authentication
// is on.
//
// The flow is written out here instead of using auth.Flow, which has no way
// to resend the code.
func (c *Client) Login(ctx context.Context, phone string, code func(ctx context.Context, prompt rpc.CodePrompt) (string, bool, error), password func(context.Context) (string, error)) error {
	err := mapError(c.login(ctx, phone, code, password))
	if err == nil {
		c.signalAuthorized()
	}
	return err
}

func (c *Client) login(ctx context.Context, phone string, code func(context.Context, rpc.CodePrompt) (string, bool, error), password func(context.Context) (string, error)) error {
	cl := c.tg.Auth()
	sent, err := cl.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return err
	}
	for {
		var s *tg.AuthSentCode
		switch v := sent.(type) {
		case *tg.AuthSentCode:
			s = v
		case *tg.AuthSentCodeSuccess:
			if _, ok := v.Authorization.(*tg.AuthAuthorization); ok {
				return nil // Telegram signed us in without a code
			}
			return errors.New("signing up a new account is not supported")
		default:
			return errors.New("unexpected answer to the code request")
		}

		answer, resend, err := code(ctx, codePrompt(s))
		if err != nil {
			return err
		}
		if resend {
			if sent, err = cl.ResendCode(ctx, phone, s.PhoneCodeHash); err != nil {
				return err
			}
			continue
		}

		_, err = cl.SignIn(ctx, phone, answer, s.PhoneCodeHash)
		var signUp *auth.SignUpRequired
		switch {
		case errors.Is(err, auth.ErrPasswordAuthNeeded):
			pw, err := password(ctx)
			if err != nil {
				return err
			}
			_, err = cl.Password(ctx, pw)
			return err
		case errors.As(err, &signUp):
			return errors.New("signing up a new account is not supported")
		}
		return err
	}
}

// codePrompt tells where Telegram sent the code and what a resend would use.
func codePrompt(s *tg.AuthSentCode) rpc.CodePrompt {
	p := rpc.CodePrompt{Delivery: delivery(s)}
	if next, ok := s.GetNextType(); ok {
		switch next.(type) {
		case *tg.AuthCodeTypeSMS, *tg.AuthCodeTypeFragmentSMS:
			p.NextDelivery = rpc.DeliverySMS
		case *tg.AuthCodeTypeCall, *tg.AuthCodeTypeFlashCall, *tg.AuthCodeTypeMissedCall:
			p.NextDelivery = rpc.DeliveryCall
		default:
			p.NextDelivery = rpc.DeliveryOther
		}
	}
	if timeout, ok := s.GetTimeout(); ok {
		p.ResendAfterSeconds = timeout
	}
	return p
}

// delivery tells where Telegram sent the login code.
func delivery(sent *tg.AuthSentCode) string {
	if sent == nil {
		return rpc.DeliveryUnsent
	}
	switch sent.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		return rpc.DeliveryApp
	case *tg.AuthSentCodeTypeSMS, *tg.AuthSentCodeTypeFirebaseSMS, *tg.AuthSentCodeTypeSMSWord, *tg.AuthSentCodeTypeSMSPhrase:
		return rpc.DeliverySMS
	case *tg.AuthSentCodeTypeCall, *tg.AuthSentCodeTypeFlashCall, *tg.AuthSentCodeTypeMissedCall:
		return rpc.DeliveryCall
	case *tg.AuthSentCodeTypeEmailCode:
		return rpc.DeliveryEmail
	}
	return rpc.DeliveryOther
}
