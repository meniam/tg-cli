package tgclient

import (
	"context"
	"errors"
	"net"
	"strings"

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
		if tgErr.Type == "CHAT_FORWARDS_RESTRICTED" {
			return rpc.NewError(rpc.CodeTelegram, "forwarding from this chat is restricted (protected content)")
		}
		return rpc.NewError(rpc.CodeTelegram, tgErr.Type)
	}
	return safeClientError(err)
}

func safeClientError(err error) error {
	message := "telegram request failed: unexpected response"
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			message = "telegram request failed: timeout"
		} else {
			message = "telegram request failed: network error"
		}
	}
	return &rpc.Error{Code: rpc.CodeTelegram, Message: message, Cause: err}
}

// mapActionError keeps new write errors stable without changing older commands.
func mapActionError(err error) error {
	if tgErr, ok := tgerr.As(err); ok {
		switch tgErr.Type {
		case "REACTION_INVALID":
			return rpc.NewError(rpc.CodeInvalidParams, "reaction is not available for this message")
		case "MESSAGE_ID_INVALID", "MSG_ID_INVALID":
			return rpc.NewError(rpc.CodeInvalidParams, "message id is invalid")
		case "DATA_INVALID", "QUERY_ID_INVALID", "RESULT_ID_INVALID", "TTL_PERIOD_INVALID", "MEDIA_NEW_INVALID", "MEDIA_PREV_INVALID", "MESSAGE_NOT_MODIFIED", "MESSAGE_EDIT_TIME_EXPIRED", "MESSAGE_AUTHOR_REQUIRED", "STICKERSET_INVALID":
			return rpc.NewError(rpc.CodeInvalidParams, "Telegram rejected the requested action: "+tgErr.Type)
		case "CHAT_ADMIN_REQUIRED":
			return rpc.NewError(rpc.CodeTelegram, "chat admin rights are required")
		case "CHAT_SEND_POLL_FORBIDDEN":
			return rpc.NewError(rpc.CodeTelegram, "sending polls is forbidden in this chat")
		}
		if strings.HasPrefix(tgErr.Type, "POLL_") {
			return rpc.NewError(rpc.CodeInvalidParams, "poll settings were rejected by Telegram: "+tgErr.Type)
		}
		if strings.HasPrefix(tgErr.Type, "QUIZ_") {
			return rpc.NewError(rpc.CodeInvalidParams, "quiz settings were rejected by Telegram: "+tgErr.Type)
		}
	}
	return mapError(err)
}

func mapScheduleError(err error) error {
	if e, ok := tgerr.As(err); ok {
		switch e.Type {
		case "SCHEDULE_DATE_TOO_LATE", "SCHEDULE_DATE_INVALID":
			return rpc.NewError(rpc.CodeInvalidParams, "schedule date rejected by Telegram: "+e.Type)
		case "SCHEDULE_TOO_MUCH":
			return rpc.NewError(rpc.CodeTelegram, "too many scheduled messages")
		}
	}
	return mapActionError(err)
}
