package tgclient

import (
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func mapChatAdminError(err error) error {
	if e, ok := tgerr.As(err); ok {
		message := ""
		code := rpc.CodeTelegram
		switch e.Type {
		case "CHAT_ADMIN_REQUIRED", "RIGHT_FORBIDDEN", "USER_ADMIN_INVALID", "CHAT_WRITE_FORBIDDEN":
			message = "chat admin rights are required"
		case "USER_CREATOR":
			message = "the chat creator cannot leave or be restricted"
		case "USER_NOT_PARTICIPANT":
			code = rpc.CodeNotFound
			message = "user is not a member of this chat"
		case "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_ID_INVALID", "PEER_ID_INVALID", "USER_ID_INVALID", "PARTICIPANT_ID_INVALID", "INPUT_USER_DEACTIVATED":
			return notFound()
		case "INVITE_HASH_EMPTY", "INVITE_HASH_INVALID", "INVITE_HASH_EXPIRED":
			code = rpc.CodeNotFound
			message = "invite link is invalid or expired"
		case "CHAT_TITLE_EMPTY", "CHAT_ABOUT_TOO_LONG", "SECONDS_INVALID", "BANNED_RIGHTS_INVALID", "UNTIL_DATE_INVALID":
			code = rpc.CodeInvalidParams
			message = "invalid chat parameter: " + e.Type
		case "CHANNELS_TOO_MUCH", "USER_CHANNELS_TOO_MUCH", "CHANNELS_ADMIN_PUBLIC_TOO_MUCH", "USERS_TOO_MUCH":
			message = "Telegram limit reached: " + e.Type
		case "USER_RESTRICTED", "FROZEN_METHOD_INVALID":
			message = "the account is restricted by Telegram"
		case "STARS_PAYMENT_REQUIRED":
			message = "joining needs a payment or a bot web view: not supported"
		}
		if message != "" {
			return &rpc.Error{Code: code, Message: message, Data: map[string]any{"telegram_error": e.Type}}
		}
	}
	return mapError(err)
}
