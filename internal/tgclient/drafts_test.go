package tgclient

import (
	"github.com/gotd/td/tg"
	"testing"
)

func TestDraftFromTL(t *testing.T) {
	empty := draftFromTL(42, &tg.DraftMessageEmpty{})
	if !empty.Empty || empty.ChatID != 42 {
		t.Fatalf("empty %+v", empty)
	}
	d := draftFromTL(42, &tg.DraftMessage{Message: "hello", Date: 100, ReplyTo: &tg.InputReplyToMessage{ReplyToMsgID: 7}})
	if d.Empty || d.Text != "hello" || d.ReplyTo != 7 || d.Date == "" {
		t.Fatalf("draft %+v", d)
	}
}
