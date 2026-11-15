package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) AccountPrivacySet(ctx context.Context, p rpc.PrivacySetParams, allowUsers, denyUsers, allowChats, denyChats []chatref.Ref) (rpc.PrivacyRules, error) {
	users := func(refs []chatref.Ref) ([]tg.InputUserClass, error) {
		out := []tg.InputUserClass{}
		for _, ref := range refs {
			u, err := c.resolveUser(ctx, ref)
			if err != nil {
				return nil, err
			}
			out = append(out, u.InputUser())
		}
		return out, nil
	}
	au, err := users(allowUsers)
	if err != nil {
		return rpc.PrivacyRules{}, err
	}
	du, err := users(denyUsers)
	if err != nil {
		return rpc.PrivacyRules{}, err
	}
	chats := func(refs []chatref.Ref) ([]int64, error) {
		out := []int64{}
		for _, ref := range refs {
			peer, err := c.resolve(ctx, ref)
			if err != nil {
				return nil, err
			}
			switch v := peer.InputPeer().(type) {
			case *tg.InputPeerChat:
				out = append(out, v.ChatID)
			case *tg.InputPeerChannel:
				out = append(out, v.ChannelID)
			default:
				return nil, rpc.NewError(rpc.CodeInvalidParams, "privacy chat must be a group or channel")
			}
		}
		return out, nil
	}
	ac, err := chats(allowChats)
	if err != nil {
		return rpc.PrivacyRules{}, err
	}
	dc, err := chats(denyChats)
	if err != nil {
		return rpc.PrivacyRules{}, err
	}
	return accountPrivacySetAPI(ctx, c.tg.API(), p, au, du, ac, dc)
}

func accountPrivacySetAPI(ctx context.Context, api *tg.Client, p rpc.PrivacySetParams, au, du []tg.InputUserClass, ac, dc []int64) (rpc.PrivacyRules, error) {
	rules := []tg.InputPrivacyRuleClass{}
	if len(au) > 0 {
		rules = append(rules, &tg.InputPrivacyValueAllowUsers{Users: au})
	}
	if len(du) > 0 {
		rules = append(rules, &tg.InputPrivacyValueDisallowUsers{Users: du})
	}
	seenChats := map[int64]bool{}
	for _, ids := range [][]int64{ac, dc} {
		for _, id := range ids {
			if seenChats[id] {
				return rpc.PrivacyRules{}, rpc.NewError(rpc.CodeInvalidParams, "ambiguous or duplicate privacy chat")
			}
			seenChats[id] = true
		}
	}
	if len(ac) > 0 {
		rules = append(rules, &tg.InputPrivacyValueAllowChatParticipants{Chats: ac})
	}
	if len(dc) > 0 {
		rules = append(rules, &tg.InputPrivacyValueDisallowChatParticipants{Chats: dc})
	}
	if p.AllowPremium {
		rules = append(rules, &tg.InputPrivacyValueAllowPremium{})
	}
	if p.AllowBots {
		rules = append(rules, &tg.InputPrivacyValueAllowBots{})
	}
	if p.DenyBots {
		rules = append(rules, &tg.InputPrivacyValueDisallowBots{})
	}
	if p.AllowCloseFriends {
		rules = append(rules, &tg.InputPrivacyValueAllowCloseFriends{})
	}
	switch p.Default {
	case "everybody":
		rules = append(rules, &tg.InputPrivacyValueAllowAll{})
	case "contacts":
		rules = append(rules, &tg.InputPrivacyValueAllowContacts{}, &tg.InputPrivacyValueDisallowAll{})
	case "nobody":
		rules = append(rules, &tg.InputPrivacyValueDisallowAll{})
	default:
		return rpc.PrivacyRules{}, rpc.NewError(rpc.CodeInvalidParams, "invalid default")
	}
	value, err := api.AccountSetPrivacy(ctx, &tg.AccountSetPrivacyRequest{Key: privacyKey(p.Key), Rules: rules})
	if err != nil {
		return rpc.PrivacyRules{}, mapError(err)
	}
	result, err := privacyFromTL(p.Key, value)
	if err != nil {
		return rpc.PrivacyRules{}, &rpc.Error{Code: rpc.CodeTelegram, Message: "privacy was changed but Telegram returned unsupported rules", Data: map[string]any{"changed": true}}
	}
	return result, nil
}
