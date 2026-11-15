package daemon

import (
	"errors"
	"tg-cli/internal/rpc"
)

// Login input must never be repeated, even when an upstream error echoes it.
func safeLoginError(err error) error {
	var e *rpc.Error
	if errors.As(err, &e) {
		switch e.Message {
		case "PHONE_CODE_INVALID", "PHONE_CODE_EXPIRED", "PHONE_NUMBER_INVALID", "PASSWORD_HASH_INVALID", "SESSION_PASSWORD_NEEDED":
			return rpc.NewError(e.Code, e.Message)
		}
		if e.Code == rpc.CodeFloodWait {
			return &rpc.Error{Code: e.Code, Message: "FLOOD_WAIT: wait before the next request", Data: e.Data}
		}
		if e.Code == rpc.CodeNotAuthorized {
			return rpc.NewError(e.Code, "not authorized")
		}
	}
	return rpc.NewError(rpc.CodeTelegram, "login failed")
}
