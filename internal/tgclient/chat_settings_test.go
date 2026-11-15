package tgclient

import (
	"encoding/json"
	"github.com/gotd/td/tg"
	"testing"
)

func TestChatSettingsRightsAndReactions(t *testing.T) {
	c := &tg.Channel{Noforwards: true, Signatures: true}
	c.SetDefaultBannedRights(tg.ChatBannedRights{SendPlain: true, PinMessages: true})
	f := &tg.ChannelFull{}
	f.SetSlowmodeSeconds(30)
	f.SetAvailableReactions(&tg.ChatReactionsSome{Reactions: []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: "👍"}, &tg.ReactionCustomEmoji{DocumentID: 99}}})
	var got map[string]any
	if err := json.Unmarshal(channelSettings(c, f), &got); err != nil {
		t.Fatal(err)
	}
	r := got["reactions"].(map[string]any)
	items := r["emoji"].([]any)
	if got["slow_mode_seconds"] != float64(30) || got["signatures"] != true || len(got["default_denied"].([]any)) != 2 || items[1].(map[string]any)["document_id"] != float64(99) {
		t.Fatalf("settings: %+v", got)
	}
}
