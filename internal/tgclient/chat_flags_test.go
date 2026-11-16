package tgclient

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

// TestChatFlags covers what a chat info reads from a Telegram object: the creation
// date, scam, fake and the restriction with its reasons. A basic group carries
// none of those flags, and an object without them reads as false and 0.
func TestChatFlags(t *testing.T) {
	restricted := channel(9, "Restricted", "restricted", false, true)
	restricted.Restricted = true
	restricted.Date = 1700000000
	restricted.SetRestrictionReason([]tg.RestrictionReason{{Platform: "ios", Reason: "porno", Text: "Not available"}})
	scam := channel(10, "Scam", "scam", true, false)
	scam.Scam, scam.Fake = true, true
	scam.Date = 1600000000
	clean := channel(11, "Clean", "", true, false)
	group := &tg.Chat{ID: 4, Title: "Group", Date: 1500000000}

	for _, tc := range []struct {
		name        string
		peer        peers.Peer
		created     int64
		scam, fake  bool
		restricted  bool
		reasonCount int
		reasonText  string
	}{
		{name: "restricted supergroup", created: 1700000000, restricted: true, reasonCount: 1, reasonText: "Not available"},
		{name: "scam channel", created: 1600000000, scam: true, fake: true},
		{name: "clean channel"},
		{name: "basic group", created: 1500000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := testPeers(tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return nil })))
			var peer peers.Peer
			switch tc.name {
			case "restricted supergroup":
				peer = mgr.Channel(restricted)
			case "scam channel":
				peer = mgr.Channel(scam)
			case "clean channel":
				peer = mgr.Channel(clean)
			default:
				peer = mgr.Chat(group)
			}
			gotScam, gotFake, gotRestricted, reasons := restrictionFlags(peer)
			if created := createdUnix(peer); created != tc.created {
				t.Errorf("created_at = %d, want %d", created, tc.created)
			}
			if gotScam != tc.scam || gotFake != tc.fake || gotRestricted != tc.restricted {
				t.Errorf("flags = scam=%v fake=%v restricted=%v", gotScam, gotFake, gotRestricted)
			}
			if len(reasons) != tc.reasonCount {
				t.Fatalf("reasons = %+v, want %d", reasons, tc.reasonCount)
			}
			if tc.reasonCount > 0 {
				if reasons[0] != (rpc.RestrictionReason{Platform: "ios", Reason: "porno", Text: "Not available"}) {
					t.Errorf("reason = %+v", reasons[0])
				}
			}
		})
	}
}
