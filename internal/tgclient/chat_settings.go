package tgclient

import (
	"encoding/json"
	"github.com/gotd/td/tg"
)

func settingsJSON(m map[string]any) json.RawMessage { b, _ := json.Marshal(m); return b }
func denied(r tg.ChatBannedRights) []string {
	v := []struct {
		name string
		set  bool
	}{
		{"send-plain", r.SendPlain}, {"send-media", r.SendMedia}, {"send-photos", r.SendPhotos}, {"send-videos", r.SendVideos}, {"send-roundvideos", r.SendRoundvideos}, {"send-audios", r.SendAudios}, {"send-voices", r.SendVoices}, {"send-docs", r.SendDocs}, {"send-stickers", r.SendStickers}, {"send-gifs", r.SendGifs}, {"send-games", r.SendGames}, {"send-inline", r.SendInline}, {"send-polls", r.SendPolls}, {"embed-links", r.EmbedLinks}, {"send-reactions", r.SendReactions}, {"change-info", r.ChangeInfo}, {"invite-users", r.InviteUsers}, {"pin-messages", r.PinMessages}, {"manage-topics", r.ManageTopics},
	}
	out := []string{}
	for _, x := range v {
		if x.set {
			out = append(out, x.name)
		}
	}
	return out
}
func settingsReactions(m map[string]any, r tg.ChatReactionsClass) {
	switch v := r.(type) {
	case *tg.ChatReactionsNone:
		m["reactions"] = map[string]any{"type": "none"}
	case *tg.ChatReactionsAll:
		m["reactions"] = map[string]any{"type": "all"}
	case *tg.ChatReactionsSome:
		items := []any{}
		for _, raw := range v.Reactions {
			switch x := raw.(type) {
			case *tg.ReactionEmoji:
				items = append(items, x.Emoticon)
			case *tg.ReactionCustomEmoji:
				items = append(items, map[string]any{"document_id": x.DocumentID})
			}
		}
		m["reactions"] = map[string]any{"type": "some", "emoji": items}
	}
}
func userSettings(full *tg.UserFull) json.RawMessage {
	m := map[string]any{}
	if x, ok := full.GetTTLPeriod(); ok {
		m["ttl_period"] = x
	}
	return settingsJSON(m)
}
func groupSettings(c *tg.Chat, full *tg.ChatFull) json.RawMessage {
	m := map[string]any{}
	if r, ok := c.GetDefaultBannedRights(); ok {
		m["default_denied"] = denied(r)
	}
	m["no_forwards"] = c.Noforwards
	if x, ok := full.GetTTLPeriod(); ok {
		m["ttl_period"] = x
	}
	if x, ok := full.GetAvailableReactions(); ok {
		settingsReactions(m, x)
	}
	return settingsJSON(m)
}
func channelSettings(c *tg.Channel, full *tg.ChannelFull) json.RawMessage {
	m := map[string]any{"no_forwards": c.Noforwards, "join_request": c.JoinRequest, "join_to_send": c.JoinToSend, "signatures": c.Signatures, "signature_profiles": c.SignatureProfiles, "antispam": full.Antispam, "hidden_history": full.HiddenPrehistory}
	if r, ok := c.GetDefaultBannedRights(); ok {
		m["default_denied"] = denied(r)
	}
	if x, ok := full.GetTTLPeriod(); ok {
		m["ttl_period"] = x
	}
	if x, ok := full.GetSlowmodeSeconds(); ok {
		m["slow_mode_seconds"] = x
	}
	if x, ok := full.GetLinkedChatID(); ok {
		m["linked_chat_id"] = -channelIDBase - x
	}
	if x, ok := full.GetAvailableReactions(); ok {
		settingsReactions(m, x)
	}
	return settingsJSON(m)
}
