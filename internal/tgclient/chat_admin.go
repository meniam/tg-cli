package tgclient

import (
	"context"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// chatFromUpdates extracts the chat included by Telegram in a write result.
func chatFromUpdates(updates tg.UpdatesClass) (rpc.CommonChat, error) {
	withChats, ok := updates.(interface{ GetChats() []tg.ChatClass })
	if ok {
		for _, raw := range withChats.GetChats() {
			if chat, _, ok := commonChatOf(raw); ok {
				return chat, nil
			}
		}
	}
	return rpc.CommonChat{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned no chat")
}
func joinResult(result tg.MessagesChatInviteJoinResultClass) (rpc.ChatJoinResult, error) {
	switch r := result.(type) {
	case *tg.MessagesChatInviteJoinResultOk:
		chat, err := chatFromUpdates(r.Updates)
		if err != nil {
			return rpc.ChatJoinResult{}, err
		}
		return rpc.ChatJoinResult{ChatID: chat.ID, Type: chat.Type}, nil
	case *tg.MessagesChatInviteJoinResultWebView:
		return rpc.ChatJoinResult{}, rpc.NewError(rpc.CodeTelegram, "joining needs a payment or a bot web view: not supported")
	default:
		return rpc.ChatJoinResult{}, rpc.NewError(rpc.CodeTelegram, "unexpected Telegram join result")
	}
}
func (c *Client) Join(ctx context.Context, ref chatref.Ref) (rpc.ChatJoinResult, error) {
	if ref.Kind == chatref.Invite {
		return joinAdminChat(ctx, c.tg.API(), ref.Invite, nil)
	}
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatJoinResult{}, err
	}
	return joinAdminChat(ctx, c.tg.API(), "", p)
}

func joinAdminChat(ctx context.Context, api *tg.Client, invite string, p peers.Peer) (rpc.ChatJoinResult, error) {
	var result tg.MessagesChatInviteJoinResultClass
	var err error
	var resolved rpc.ChatJoinResult
	if invite != "" {
		result, err = api.MessagesImportChatInvite(ctx, invite)
	} else {
		channel, ok := p.(peers.Channel)
		if !ok {
			return rpc.ChatJoinResult{}, rpc.NewError(rpc.CodeInvalidParams, "joining requires a channel or supergroup")
		}
		resolved.ChatID = int64(p.TDLibPeerID())
		resolved.Type = "channel"
		if channel.Raw().Megagroup {
			resolved.Type = "supergroup"
		}
		result, err = api.ChannelsJoinChannel(ctx, channel.InputChannel())
	}
	if tgerr.Is(err, "USER_ALREADY_PARTICIPANT") {
		resolved.AlreadyMember = true
		return resolved, nil
	}
	if tgerr.Is(err, "INVITE_REQUEST_SENT") {
		resolved.Requested = true
		return resolved, nil
	}
	if err != nil {
		return rpc.ChatJoinResult{}, mapChatAdminError(err)
	}
	return joinResult(result)
}
func (c *Client) Leave(ctx context.Context, ref chatref.Ref) (rpc.ChatLeaveResult, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatLeaveResult{}, err
	}
	return leaveAdminChat(ctx, c.tg.API(), p)
}

func leaveAdminChat(ctx context.Context, api *tg.Client, p peers.Peer) (rpc.ChatLeaveResult, error) {
	var err error
	switch v := p.(type) {
	case peers.Channel:
		_, err = api.ChannelsLeaveChannel(ctx, v.InputChannel())
	case peers.Chat:
		_, err = api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{ChatID: v.ID(), UserID: &tg.InputUserSelf{}})
	default:
		return rpc.ChatLeaveResult{}, rpc.NewError(rpc.CodeInvalidParams, "leaving requires a group or channel")
	}
	if tgerr.Is(err, "USER_NOT_PARTICIPANT") {
		return rpc.ChatLeaveResult{}, &rpc.Error{Code: rpc.CodeNotFound, Message: "you are not a member of this chat", Data: map[string]any{"telegram_error": "USER_NOT_PARTICIPANT"}}
	}
	if err != nil {
		return rpc.ChatLeaveResult{}, mapChatAdminError(err)
	}
	return rpc.ChatLeaveResult{ChatID: int64(p.TDLibPeerID()), Left: true}, nil
}
func (c *Client) CreateChat(ctx context.Context, p rpc.ChatCreateParams) (rpc.ChatCreateResult, error) {
	if p.DryRun {
		return rpc.ChatCreateResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := rpc.ValidateChatTitle(p.Title); err != nil {
		return rpc.ChatCreateResult{}, err
	}
	if err := rpc.ValidateChatAbout(p.About); err != nil {
		return rpc.ChatCreateResult{}, err
	}
	if p.Type != "supergroup" && p.Type != "channel" {
		return rpc.ChatCreateResult{}, rpc.NewError(rpc.CodeInvalidParams, "type must be supergroup or channel")
	}
	return createAdminChat(ctx, c.tg.API(), p)
}

func createAdminChat(ctx context.Context, api *tg.Client, p rpc.ChatCreateParams) (rpc.ChatCreateResult, error) {
	u, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{Title: p.Title, About: p.About, Megagroup: p.Type == "supergroup", Broadcast: p.Type == "channel"})
	if err != nil {
		return rpc.ChatCreateResult{}, mapChatAdminError(err)
	}
	chat, err := chatFromUpdates(u)
	if err != nil {
		return rpc.ChatCreateResult{}, err
	}
	return rpc.ChatCreateResult{ID: chat.ID, Type: chat.Type, Title: chat.Title}, nil
}
func (c *Client) EditChat(ctx context.Context, ref chatref.Ref, p rpc.ChatEditParams) (rpc.ChatEditResult, error) {
	if p.DryRun {
		return rpc.ChatEditResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if p.Title == nil && p.About == nil {
		return rpc.ChatEditResult{}, rpc.NewError(rpc.CodeInvalidParams, "title or about is required")
	}
	if p.Title != nil {
		if err := rpc.ValidateChatTitle(*p.Title); err != nil {
			return rpc.ChatEditResult{}, err
		}
	}
	if p.About != nil {
		if err := rpc.ValidateChatAbout(*p.About); err != nil {
			return rpc.ChatEditResult{}, err
		}
	}
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatEditResult{}, err
	}
	return editAdminChat(ctx, c.tg.API(), peer, p)
}

func editAdminChat(ctx context.Context, api *tg.Client, peer peers.Peer, p rpc.ChatEditParams) (rpc.ChatEditResult, error) {
	var err error
	r := rpc.ChatEditResult{ChatID: int64(peer.TDLibPeerID())}
	switch peer.(type) {
	case peers.Chat, peers.Channel:
	default:
		return r, rpc.NewError(rpc.CodeInvalidParams, "editing requires a group or channel")
	}
	if p.Title != nil {
		switch v := peer.(type) {
		case peers.Channel:
			_, err = api.ChannelsEditTitle(ctx, &tg.ChannelsEditTitleRequest{Channel: v.InputChannel(), Title: *p.Title})
		case peers.Chat:
			_, err = api.MessagesEditChatTitle(ctx, &tg.MessagesEditChatTitleRequest{ChatID: v.ID(), Title: *p.Title})
		}
		if err != nil && !tgerr.Is(err, "CHAT_NOT_MODIFIED") {
			return r, mapChatAdminError(err)
		}
		r.TitleChanged = err == nil
	}
	if p.About != nil {
		var changed bool
		changed, err = api.MessagesEditChatAbout(ctx, &tg.MessagesEditChatAboutRequest{Peer: peer.InputPeer(), About: *p.About})
		if err != nil && !tgerr.Is(err, "CHAT_NOT_MODIFIED", "CHAT_ABOUT_NOT_MODIFIED") {
			return r, mapChatAdminError(err)
		}
		r.AboutChanged = err == nil && changed
	}
	return r, nil
}
func bannedRights(names []string, until int) tg.ChatBannedRights {
	r := tg.ChatBannedRights{UntilDate: until}
	for _, n := range names {
		switch n {
		case "send-plain":
			r.SendPlain = true
		case "send-media":
			r.SendMedia = true
		case "send-photos":
			r.SendPhotos = true
		case "send-videos":
			r.SendVideos = true
		case "send-roundvideos":
			r.SendRoundvideos = true
		case "send-audios":
			r.SendAudios = true
		case "send-voices":
			r.SendVoices = true
		case "send-docs":
			r.SendDocs = true
		case "send-stickers":
			r.SendStickers = true
		case "send-gifs":
			r.SendGifs = true
		case "send-games":
			r.SendGames = true
		case "send-inline":
			r.SendInline = true
		case "send-polls":
			r.SendPolls = true
		case "embed-links":
			r.EmbedLinks = true
		case "send-reactions":
			r.SendReactions = true
		case "change-info":
			r.ChangeInfo = true
		case "invite-users":
			r.InviteUsers = true
		case "pin-messages":
			r.PinMessages = true
		case "manage-topics":
			r.ManageTopics = true
		}
	}
	return r
}
func untilUnix(value string) int {
	if value == "" {
		return 0
	}
	t, _ := time.Parse(time.RFC3339, value)
	return int(t.Unix())
}

func (c *Client) adminPeers(ctx context.Context, chat, user chatref.Ref) (peers.Peer, peers.User, error) {
	p, err := c.resolve(ctx, chat)
	if err != nil {
		return nil, peers.User{}, err
	}
	if user.Kind != chatref.Username && (user.Kind != chatref.ID || user.ID <= 0) {
		return nil, peers.User{}, rpc.NewError(rpc.CodeInvalidParams, "user must be a username or positive id")
	}
	u, err := c.resolve(ctx, user)
	if err != nil {
		return nil, peers.User{}, err
	}
	member, ok := u.(peers.User)
	if !ok {
		return nil, peers.User{}, rpc.NewError(rpc.CodeInvalidParams, "target must be a user")
	}
	return p, member, nil
}
func editBanned(ctx context.Context, api *tg.Client, channel peers.Channel, member peers.User, rights tg.ChatBannedRights) error {
	_, err := api.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{Channel: channel.InputChannel(), Participant: member.InputPeer(), BannedRights: rights})
	return err
}

func kickChannel(ctx context.Context, api *tg.Client, channel peers.Channel, member peers.User) error {
	if err := editBanned(ctx, api, channel, member, tg.ChatBannedRights{ViewMessages: true}); err != nil {
		return mapChatAdminError(err)
	}
	if err := editBanned(ctx, api, channel, member, tg.ChatBannedRights{}); err != nil {
		return &rpc.Error{Code: rpc.CodeTelegram, Message: "kick partially completed; user remains banned", Data: map[string]any{"still_banned": true}}
	}
	return nil
}
func (c *Client) Kick(ctx context.Context, chat, user chatref.Ref) (rpc.ChatAdminResult, error) {
	p, u, err := c.adminPeers(ctx, chat, user)
	if err != nil {
		return rpc.ChatAdminResult{}, err
	}
	return kickAdminMember(ctx, c.tg.API(), p, u)
}

func kickAdminMember(ctx context.Context, api *tg.Client, p peers.Peer, u peers.User) (rpc.ChatAdminResult, error) {
	var err error
	r := rpc.ChatAdminResult{ChatID: int64(p.TDLibPeerID()), UserID: u.ID()}
	switch v := p.(type) {
	case peers.Chat:
		_, err = api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{ChatID: v.ID(), UserID: u.InputUser()})
		if err != nil {
			return r, mapChatAdminError(err)
		}
	case peers.Channel:
		if err = kickChannel(ctx, api, v, u); err != nil {
			return r, err
		}
	default:
		return r, rpc.NewError(rpc.CodeInvalidParams, "kick requires a group or channel")
	}
	r.Kicked = true
	return r, nil
}
func (c *Client) Ban(ctx context.Context, chat, user chatref.Ref, until string) (rpc.ChatAdminResult, error) {
	if err := rpc.ValidateUntil(until, time.Now()); err != nil {
		return rpc.ChatAdminResult{}, err
	}
	return c.memberRights(ctx, chat, user, tg.ChatBannedRights{ViewMessages: true, UntilDate: untilUnix(until)}, until, true)
}
func (c *Client) Unban(ctx context.Context, chat, user chatref.Ref) (rpc.ChatAdminResult, error) {
	return c.memberRights(ctx, chat, user, tg.ChatBannedRights{}, "", false)
}
func (c *Client) memberRights(ctx context.Context, chat, user chatref.Ref, rights tg.ChatBannedRights, until string, banned bool) (rpc.ChatAdminResult, error) {
	p, u, err := c.adminPeers(ctx, chat, user)
	if err != nil {
		return rpc.ChatAdminResult{}, err
	}
	return setMemberBanned(ctx, c.tg.API(), p, u, rights, until, banned)
}

func setMemberBanned(ctx context.Context, api *tg.Client, p peers.Peer, u peers.User, rights tg.ChatBannedRights, until string, banned bool) (rpc.ChatAdminResult, error) {
	ch, ok := p.(peers.Channel)
	if !ok {
		return rpc.ChatAdminResult{}, rpc.NewError(rpc.CodeInvalidParams, "operation requires a supergroup or channel")
	}
	if err := editBanned(ctx, api, ch, u, rights); err != nil {
		return rpc.ChatAdminResult{}, mapChatAdminError(err)
	}
	return rpc.ChatAdminResult{ChatID: int64(p.TDLibPeerID()), UserID: u.ID(), Banned: &banned, Until: until}, nil
}
func (c *Client) Restrict(ctx context.Context, chat, user chatref.Ref, deny []string, clear bool, until string) (rpc.ChatAdminResult, error) {
	if err := rpc.ValidateRights(deny, clear); err != nil {
		return rpc.ChatAdminResult{}, err
	}
	if err := rpc.ValidateUntil(until, time.Now()); err != nil {
		return rpc.ChatAdminResult{}, err
	}
	p, u, err := c.adminPeers(ctx, chat, user)
	if err != nil {
		return rpc.ChatAdminResult{}, err
	}
	return restrictAdminMember(ctx, c.tg.API(), p, u, deny, until)
}

func restrictAdminMember(ctx context.Context, api *tg.Client, p peers.Peer, u peers.User, deny []string, until string) (rpc.ChatAdminResult, error) {
	ch, ok := p.(peers.Channel)
	if !ok || !ch.Raw().Megagroup {
		return rpc.ChatAdminResult{}, rpc.NewError(rpc.CodeInvalidParams, "restrict requires a supergroup")
	}
	if err := editBanned(ctx, api, ch, u, bannedRights(deny, untilUnix(until))); err != nil {
		return rpc.ChatAdminResult{}, mapChatAdminError(err)
	}
	return rpc.ChatAdminResult{ChatID: int64(p.TDLibPeerID()), UserID: u.ID(), Denied: deny, Until: until}, nil
}
func (c *Client) SetDefaultRights(ctx context.Context, ref chatref.Ref, deny []string, clear bool) (rpc.ChatAdminResult, error) {
	if err := rpc.ValidateRights(deny, clear); err != nil {
		return rpc.ChatAdminResult{}, err
	}
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatAdminResult{}, err
	}
	return setDefaultChatRights(ctx, c.tg.API(), p, deny)
}

func setDefaultChatRights(ctx context.Context, api *tg.Client, p peers.Peer, deny []string) (rpc.ChatAdminResult, error) {
	switch v := p.(type) {
	case peers.Chat:
	case peers.Channel:
		if !v.Raw().Megagroup {
			return rpc.ChatAdminResult{}, rpc.NewError(rpc.CodeInvalidParams, "permissions require a group")
		}
	default:
		return rpc.ChatAdminResult{}, rpc.NewError(rpc.CodeInvalidParams, "permissions require a group")
	}
	_, err := api.MessagesEditChatDefaultBannedRights(ctx, &tg.MessagesEditChatDefaultBannedRightsRequest{Peer: p.InputPeer(), BannedRights: bannedRights(deny, 0)})
	if err != nil {
		return rpc.ChatAdminResult{}, mapChatAdminError(err)
	}
	return rpc.ChatAdminResult{ChatID: int64(p.TDLibPeerID()), Denied: deny}, nil
}
func (c *Client) SetSlowMode(ctx context.Context, ref chatref.Ref, seconds int) (rpc.ChatAdminResult, error) {
	if err := rpc.ValidateSlowModeSeconds(seconds); err != nil {
		return rpc.ChatAdminResult{}, err
	}
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatAdminResult{}, err
	}
	return setChatSlowMode(ctx, c.tg.API(), p, seconds)
}

func setChatSlowMode(ctx context.Context, api *tg.Client, p peers.Peer, seconds int) (rpc.ChatAdminResult, error) {
	ch, ok := p.(peers.Channel)
	if !ok || !ch.Raw().Megagroup {
		return rpc.ChatAdminResult{}, rpc.NewError(rpc.CodeInvalidParams, "slow mode needs a supergroup")
	}
	_, err := api.ChannelsToggleSlowMode(ctx, &tg.ChannelsToggleSlowModeRequest{Channel: ch.InputChannel(), Seconds: seconds})
	if err != nil {
		return rpc.ChatAdminResult{}, mapChatAdminError(err)
	}
	return rpc.ChatAdminResult{ChatID: int64(p.TDLibPeerID()), Seconds: &seconds}, nil
}

// OwnAdminRights fetches fresh chat metadata and returns whether the account is the creator and its rights.
func (c *Client) OwnAdminRights(ctx context.Context, ref chatref.Ref) (rpc.ChatOwnAdminRights, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatOwnAdminRights{}, err
	}
	return ownAdminRights(ctx, c.tg.API(), p)
}

func ownAdminRights(ctx context.Context, api *tg.Client, p peers.Peer) (rpc.ChatOwnAdminRights, error) {
	var chats []tg.ChatClass
	switch v := p.(type) {
	case peers.Channel:
		response, e := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{v.InputChannel()})
		if e != nil {
			return rpc.ChatOwnAdminRights{}, mapChatAdminError(e)
		}
		chats = response.GetChats()
	case peers.Chat:
		response, e := api.MessagesGetFullChat(ctx, v.ID())
		if e != nil {
			return rpc.ChatOwnAdminRights{}, mapChatAdminError(e)
		}
		chats = response.Chats
	default:
		return rpc.ChatOwnAdminRights{}, rpc.NewError(rpc.CodeInvalidParams, "admin rights require a group or channel")
	}
	for _, raw := range chats {
		switch v := raw.(type) {
		case *tg.Chat:
			if int64(-v.ID) == int64(p.TDLibPeerID()) {
				return rpc.ChatOwnAdminRights{Creator: v.Creator, ChangeInfo: v.AdminRights.ChangeInfo, BanUsers: v.AdminRights.BanUsers}, nil
			}
		case *tg.Channel:
			if -channelIDBase-v.ID == int64(p.TDLibPeerID()) {
				return rpc.ChatOwnAdminRights{Creator: v.Creator, ChangeInfo: v.AdminRights.ChangeInfo, BanUsers: v.AdminRights.BanUsers}, nil
			}
		}
	}
	return rpc.ChatOwnAdminRights{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned no matching chat")
}
