package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func privacyKey(key string) tg.InputPrivacyKeyClass {
	switch key {
	case "last-seen":
		return &tg.InputPrivacyKeyStatusTimestamp{}
	case "phone-number":
		return &tg.InputPrivacyKeyPhoneNumber{}
	case "added-by-phone":
		return &tg.InputPrivacyKeyAddedByPhone{}
	case "profile-photo":
		return &tg.InputPrivacyKeyProfilePhoto{}
	case "forwards":
		return &tg.InputPrivacyKeyForwards{}
	case "calls":
		return &tg.InputPrivacyKeyPhoneCall{}
	case "p2p-calls":
		return &tg.InputPrivacyKeyPhoneP2P{}
	case "invites":
		return &tg.InputPrivacyKeyChatInvite{}
	case "voice-messages":
		return &tg.InputPrivacyKeyVoiceMessages{}
	case "bio":
		return &tg.InputPrivacyKeyAbout{}
	case "birthday":
		return &tg.InputPrivacyKeyBirthday{}
	case "gifts-auto-save":
		return &tg.InputPrivacyKeyStarGiftsAutoSave{}
	case "no-paid-messages":
		return &tg.InputPrivacyKeyNoPaidMessages{}
	case "saved-music":
		return &tg.InputPrivacyKeySavedMusic{}
	}
	return nil
}
func privacyFromTL(key string, value *tg.AccountPrivacyRules) (rpc.PrivacyRules, error) {
	out := rpc.PrivacyRules{Key: key, AllowUsers: []int64{}, DenyUsers: []int64{}, AllowChats: []int64{}, DenyChats: []int64{}}
	chats := map[int64]int64{}
	addChat := func(id, mapped int64) {
		if existing, ok := chats[id]; ok && existing != mapped {
			chats[id] = 0
		} else if !ok {
			chats[id] = mapped
		}
	}
	for _, raw := range value.Chats {
		switch v := raw.(type) {
		case *tg.Chat:
			addChat(v.ID, -v.ID)
		case *tg.Channel:
			addChat(v.ID, -channelIDBase-v.ID)
		}
	}
	addChats := func(ids []int64, dst *[]int64) {
		for _, id := range ids {
			if x, ok := chats[id]; ok && x != 0 {
				*dst = append(*dst, x)
			} else {
				out.Hidden++
			}
		}
	}
	for _, raw := range value.Rules {
		if out.Default != "" {
			if _, ok := raw.(*tg.PrivacyValueDisallowAll); !ok || out.Default != "contacts" {
				return out, rpc.NewError(rpc.CodeTelegram, "unsupported privacy rule order")
			}
		}
		switch v := raw.(type) {
		case *tg.PrivacyValueAllowAll:
			if out.Default == "" {
				out.Default = "everybody"
			}
		case *tg.PrivacyValueAllowContacts:
			if out.Default == "" {
				out.Default = "contacts"
			}
		case *tg.PrivacyValueDisallowAll:
			if out.Default == "" {
				out.Default = "nobody"
			}
		case *tg.PrivacyValueDisallowContacts:
			return out, rpc.NewError(rpc.CodeTelegram, "unsupported privacy rule")
		case *tg.PrivacyValueAllowUsers:
			out.AllowUsers = append(out.AllowUsers, v.Users...)
		case *tg.PrivacyValueDisallowUsers:
			out.DenyUsers = append(out.DenyUsers, v.Users...)
		case *tg.PrivacyValueAllowChatParticipants:
			addChats(v.Chats, &out.AllowChats)
		case *tg.PrivacyValueDisallowChatParticipants:
			addChats(v.Chats, &out.DenyChats)
		case *tg.PrivacyValueAllowPremium:
			out.AllowPremium = true
		case *tg.PrivacyValueAllowBots:
			out.AllowBots = true
		case *tg.PrivacyValueDisallowBots:
			out.DenyBots = true
		case *tg.PrivacyValueAllowCloseFriends:
			out.AllowCloseFriends = true
		default:
			return out, rpc.NewError(rpc.CodeTelegram, "unsupported privacy rule")
		}
	}
	if out.Default == "" {
		return out, rpc.NewError(rpc.CodeTelegram, "Telegram returned no default privacy rule")
	}
	return out, nil
}
func (c *Client) AccountPrivacyGet(ctx context.Context, key string) (rpc.PrivacyRules, error) {
	return accountPrivacyGetAPI(ctx, c.tg.API(), key)
}
func accountPrivacyGetAPI(ctx context.Context, api *tg.Client, key string) (rpc.PrivacyRules, error) {
	if privacyKey(key) == nil {
		return rpc.PrivacyRules{}, rpc.NewError(rpc.CodeInvalidParams, "unknown privacy key")
	}
	data, err := api.AccountGetPrivacy(ctx, privacyKey(key))
	if err != nil {
		return rpc.PrivacyRules{}, mapError(err)
	}
	return privacyFromTL(key, data)
}
