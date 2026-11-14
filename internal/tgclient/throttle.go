package tgclient

import (
	"context"
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
	case *tg.MessagesSendMessageRequest, *tg.MessagesForwardMessagesRequest,
		*tg.MessagesEditMessageRequest, *tg.MessagesDeleteMessagesRequest, *tg.ChannelsDeleteMessagesRequest,
		*tg.MessagesReadHistoryRequest, *tg.ChannelsReadHistoryRequest:
		return true
	}
	return false
}

func throttleMiddleware(limiter *throttle.Limiter) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			class, peer := classifyRequest(input)
			invoke := func() error {
				permit, err := limiter.Acquire(ctx, class, peer, throttle.WaitFromContext(ctx))
				if err != nil {
					return err
				}
				defer permit.Release()
				err = next.Invoke(ctx, input, output)
				if delay, ok := tgerr.AsFloodWait(err); ok {
					limiter.Pause(delay)
				}
				return err
			}
			err := invoke()
			delay, flood := tgerr.AsFloodWait(err)
			if !flood || class != throttle.General || writeRequest(input) || limiter.FloodAutoWait() == 0 || delay > limiter.FloodAutoWait() {
				return err
			}
			return invoke()
		}
	})
}
