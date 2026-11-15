package tgclient

import (
	"context"
	"errors"
	"strconv"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/throttle"
)

// classifyRequest selects the destination bucket without retaining request data.
func classifyRequest(input bin.Encoder) (throttle.Class, string) {
	switch request := input.(type) {
	case *tg.MessagesSendScheduledMessagesRequest:
		if request != nil {
			return classifyPeer(request.Peer)
		}
	case *tg.MessagesSendMessageRequest:
		if request != nil {
			return classifyPeer(request.Peer)
		}
	case *tg.MessagesSendMediaRequest:
		if request != nil {
			return classifyPeer(request.Peer)
		}
	case *tg.MessagesSendMultiMediaRequest:
		if request != nil {
			return classifyPeer(request.Peer)
		}
	case *tg.MessagesForwardMessagesRequest:
		if request != nil {
			return classifyPeer(request.ToPeer)
		}
	default:
		return throttle.General, ""
	}
	return throttle.GroupSend, "unknown"
}

func classifyPeer(peer tg.InputPeerClass) (throttle.Class, string) {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		if p != nil {
			return throttle.PrivateSend, "user:" + strconv.FormatInt(p.UserID, 10)
		}
	case *tg.InputPeerSelf:
		if p != nil {
			return throttle.PrivateSend, "self"
		}
	case *tg.InputPeerUserFromMessage:
		if p != nil {
			return throttle.PrivateSend, "user:" + strconv.FormatInt(p.UserID, 10)
		}
	case *tg.InputPeerChat:
		if p != nil {
			return throttle.GroupSend, "chat:" + strconv.FormatInt(p.ChatID, 10)
		}
	case *tg.InputPeerChannel:
		if p != nil {
			return throttle.GroupSend, "channel:" + strconv.FormatInt(p.ChannelID, 10)
		}
	case *tg.InputPeerChannelFromMessage:
		if p != nil {
			return throttle.GroupSend, "channel:" + strconv.FormatInt(p.ChannelID, 10)
		}
	}
	// Unknown and empty peers share the stricter group bucket.
	return throttle.GroupSend, "unknown"
}

func writeRequest(input bin.Encoder) bool {
	switch input.(type) {
	case *tg.ChannelsJoinChannelRequest, *tg.MessagesImportChatInviteRequest,
		*tg.ChannelsLeaveChannelRequest, *tg.MessagesDeleteChatUserRequest,
		*tg.ChannelsCreateChannelRequest, *tg.ChannelsEditTitleRequest,
		*tg.MessagesEditChatTitleRequest, *tg.MessagesEditChatAboutRequest,
		*tg.ChannelsEditBannedRequest, *tg.MessagesEditChatDefaultBannedRightsRequest,
		*tg.ChannelsToggleSlowModeRequest,
		*tg.ContactsAddContactRequest, *tg.ContactsDeleteContactsRequest,
		*tg.ContactsBlockRequest, *tg.ContactsUnblockRequest:
		return true
	case *tg.MessagesSaveDraftRequest, *tg.MessagesDeleteScheduledMessagesRequest, *tg.MessagesSendScheduledMessagesRequest, *tg.MessagesSetTypingRequest, *tg.MessagesSendMessageRequest, *tg.MessagesSendMediaRequest, *tg.MessagesSendMultiMediaRequest,
		*tg.MessagesForwardMessagesRequest,
		*tg.MessagesEditMessageRequest, *tg.MessagesDeleteMessagesRequest, *tg.ChannelsDeleteMessagesRequest,
		*tg.MessagesReadHistoryRequest, *tg.ChannelsReadHistoryRequest,
		*tg.MessagesSendReactionRequest,
		*tg.MessagesUpdatePinnedMessageRequest:
		return true
	}
	return false
}

func throttleMiddleware(limiter *throttle.Limiter) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			class, peer := classifyRequest(input)
			_, isHistory := input.(*tg.MessagesGetHistoryRequest)
			history := isHistory && historyPageFromContext(ctx)
			_, smallPart := input.(*tg.UploadSaveFilePartRequest)
			_, bigPart := input.(*tg.UploadSaveBigFilePartRequest)
			_, getFile := input.(*tg.UploadGetFileRequest)
			_, getCDN := input.(*tg.UploadGetCDNFileRequest)
			uploadPart := smallPart || bigPart || getFile || getCDN
			admissionCtx := ctx
			if history || uploadPart {
				admissionCtx = throttle.WithBackground(ctx)
			}
			invoke := func() error {
				permit, err := limiter.Acquire(admissionCtx, class, peer, throttle.WaitFromContext(ctx))
				if err != nil {
					var flood *throttle.FloodWaitError
					if errors.As(err, &flood) {
						return tgerr.New(420, "FLOOD_WAIT_"+strconv.FormatInt(flood.RetryAfterSeconds, 10))
					}
					return err
				}
				defer permit.Release()
				err = next.Invoke(ctx, input, output)
				if delay, ok := tgerr.AsFloodWait(err); ok {
					if history {
						limiter.PauseBackground(delay)
					} else {
						limiter.Pause(delay)
					}
				}
				return err
			}
			if history {
				for {
					err := invoke()
					if _, flood := tgerr.AsFloodWait(err); !flood {
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
				}
			}
			err := invoke()
			delay, flood := tgerr.AsFloodWait(err)
			if !flood || class != throttle.General || uploadPart || writeRequest(input) || limiter.FloodAutoWait() == 0 || delay > limiter.FloodAutoWait() {
				return err
			}
			return invoke()
		}
	})
}
