package tgclient

import (
	"context"
	"strconv"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) HandleJoinRequest(ctx context.Context, chat, user chatref.Ref, approved bool) error {
	p, u, err := c.adminPeers(ctx, chat, user)
	if err != nil {
		return err
	}
	return handleJoinRequestAPI(ctx, c.tg.API(), p, u, approved)
}
func handleJoinRequestAPI(ctx context.Context, api *tg.Client, p peers.Peer, u peers.User, approved bool) error {
	_, err := api.MessagesHideChatJoinRequest(ctx, &tg.MessagesHideChatJoinRequestRequest{Peer: p.InputPeer(), UserID: u.InputUser(), Approved: approved})
	if err != nil {
		return mapChatAdminError(err)
	}
	return nil
}

func (c *Client) SetChannelSetting(ctx context.Context, ref chatref.Ref, flag string, value string, signatures, profiles *string) (bool, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return false, err
	}
	channel, ok := p.(peers.Channel)
	if !ok {
		return false, rpc.NewError(rpc.CodeInvalidParams, "setting requires a channel or supergroup")
	}
	return setChannelSettingAPI(ctx, c.tg.API(), channel, flag, value, signatures, profiles)
}
func setChannelSettingAPI(ctx context.Context, api *tg.Client, ch peers.Channel, flag, value string, signatures, profiles *string) (bool, error) {
	var err error
	on := value == "on"
	switch flag {
	case "signatures", "signature-profiles":
		sig, prof := ch.Raw().Signatures, ch.Raw().SignatureProfiles
		if signatures == nil || profiles == nil {
			current, e := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{ch.InputChannel()})
			if e != nil {
				return false, mapChatAdminError(e)
			}
			found := false
			for _, raw := range current.GetChats() {
				if v, ok := raw.(*tg.Channel); ok && v.ID == ch.ID() {
					sig, prof, found = v.Signatures, v.SignatureProfiles, true
					break
				}
			}
			if !found {
				return false, rpc.NewError(rpc.CodeTelegram, "Telegram returned no channel")
			}
		}
		if signatures != nil {
			sig = *signatures == "on"
		}
		if profiles != nil {
			prof = *profiles == "on"
		}
		_, err = api.ChannelsToggleSignatures(ctx, &tg.ChannelsToggleSignaturesRequest{Channel: ch.InputChannel(), SignaturesEnabled: sig, ProfilesEnabled: prof})
	case "antispam":
		_, err = api.ChannelsToggleAntiSpam(ctx, &tg.ChannelsToggleAntiSpamRequest{Channel: ch.InputChannel(), Enabled: on})
	case "forum":
		_, err = api.ChannelsToggleForum(ctx, &tg.ChannelsToggleForumRequest{Channel: ch.InputChannel(), Enabled: on})
	case "prehistory":
		_, err = api.ChannelsTogglePreHistoryHidden(ctx, &tg.ChannelsTogglePreHistoryHiddenRequest{Channel: ch.InputChannel(), Enabled: value == "hidden"})
	case "join-requests":
		_, err = api.ChannelsToggleJoinRequest(ctx, &tg.ChannelsToggleJoinRequestRequest{Channel: ch.InputChannel(), Enabled: on})
	case "join-to-send":
		_, err = api.ChannelsToggleJoinToSend(ctx, &tg.ChannelsToggleJoinToSendRequest{Channel: ch.InputChannel(), Enabled: on})
	default:
		return false, rpc.NewError(rpc.CodeInvalidParams, "unknown setting")
	}
	if tgerr.Is(err, "CHAT_NOT_MODIFIED") {
		return false, nil
	}
	if err != nil {
		return false, mapChatAdminError(err)
	}
	return true, nil
}

func (c *Client) SetAccountField(ctx context.Context, field, value, until string, channel *chatref.Ref) (bool, error) {
	var peer tg.InputChannelClass = &tg.InputChannelEmpty{}
	if channel != nil {
		p, err := c.resolve(ctx, *channel)
		if err != nil {
			return false, err
		}
		ch, ok := p.(peers.Channel)
		if !ok || ch.Raw().Megagroup {
			return false, rpc.NewError(rpc.CodeInvalidParams, "personal channel must be a channel")
		}
		peer = ch.InputChannel()
	}
	return setAccountFieldAPI(ctx, c.tg.API(), field, value, until, peer)
}
func setAccountFieldAPI(ctx context.Context, api *tg.Client, field, value, until string, peer tg.InputChannelClass) (bool, error) {
	var ok bool
	var err error
	switch field {
	case "birthday":
		req := &tg.AccountUpdateBirthdayRequest{}
		if value != "none" {
			b, e := rpc.ParseBirthday(value, time.Now())
			if e != nil {
				return false, e
			}
			birthday := tg.Birthday{Day: b.Day, Month: b.Month}
			if b.Year != 0 {
				birthday.SetYear(b.Year)
			}
			req.SetBirthday(birthday)
		}
		ok, err = api.AccountUpdateBirthday(ctx, req)
	case "personal-channel":
		ok, err = api.AccountUpdatePersonalChannel(ctx, peer)
	case "emoji-status":
		var status tg.EmojiStatusClass = &tg.EmojiStatusEmpty{}
		if value != "none" {
			id, _ := strconv.ParseInt(value, 10, 64)
			s := &tg.EmojiStatus{DocumentID: id}
			if until != "" {
				v, _ := time.Parse(time.RFC3339, until)
				s.SetUntil(int(v.Unix()))
			}
			status = s
		}
		ok, err = api.AccountUpdateEmojiStatus(ctx, status)
	default:
		return false, rpc.NewError(rpc.CodeInvalidParams, "unknown account setting")
	}
	if tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED") {
		return false, &rpc.Error{Code: rpc.CodeTelegram, Message: "Telegram Premium is required", Data: map[string]any{"reason": "premium_required"}}
	}
	if err != nil {
		return false, mapError(err)
	}
	if !ok {
		return false, rpc.NewError(rpc.CodeTelegram, "Telegram rejected the setting")
	}
	return true, nil
}
