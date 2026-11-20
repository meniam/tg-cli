package tgclient

import (
	"context"
	"encoding/json"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"log"

	"github.com/gotd/td/tdp"
	"tg-cli/internal/tgraw"
)

const (
	rawCommonChats    = "messages.getCommonChats"
	rawCreateChannel  = "channels.createChannel"
	rawImportInvite   = "messages.importChatInvite"
	rawJoinChannel    = "channels.joinChannel"
	rawAddContact     = "contacts.addContact"
	rawDialogs        = "messages.getDialogs"
	rawFullUser       = "users.getFullUser"
	rawFullChat       = "messages.getFullChat"
	rawFullChannel    = "channels.getFullChannel"
	rawContacts       = "contacts.getContacts"
	rawContactsSearch = "contacts.search"
	rawChannelMembers = "channels.getParticipants"
	rawTranscribe     = "messages.transcribeAudio"
	rawTranscribed    = "updateTranscribedAudio"
)

func messageRaw(object tdp.Object) json.RawMessage {
	raw, err := tgraw.Marshal(object)
	if err != nil {
		log.Printf("Telegram object serialization failed: %T: %v", object, err)
		return nil
	}
	return raw
}

func methodRaw(method string, object tdp.Object) json.RawMessage {
	raw := messageRaw(object)
	if len(raw) == 0 {
		return nil
	}
	out, err := json.Marshal(map[string]json.RawMessage{method: raw})
	if err != nil {
		log.Printf("Telegram object serialization failed: %T: %v", object, err)
		return nil
	}
	return out
}

type fullRawCaptureKey struct{}
type fullRawCapture struct{ raw json.RawMessage }
type fullRawInvoker struct{ next tg.Invoker }

func (i fullRawInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if err := i.next.Invoke(ctx, input, output); err != nil {
		return err
	}
	capture, ok := ctx.Value(fullRawCaptureKey{}).(*fullRawCapture)
	if !ok {
		return nil
	}
	switch input.(type) {
	case *tg.UsersGetFullUserRequest:
		if response, ok := output.(*tg.UsersUserFull); ok {
			capture.raw = methodRaw(rawFullUser, response)
		}
	case *tg.MessagesGetFullChatRequest:
		if response, ok := output.(*tg.MessagesChatFull); ok {
			capture.raw = methodRaw(rawFullChat, response)
		}
	case *tg.ChannelsGetFullChannelRequest:
		if response, ok := output.(*tg.MessagesChatFull); ok {
			capture.raw = methodRaw(rawFullChannel, response)
		}
	}
	return nil
}
