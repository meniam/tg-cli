package tgclient

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

// TestAudit10_TopicsCursorNeedsADate: the next page cursor carries the date of the
// last returned topic. When Telegram sends that topic without its message and not
// ordered by creation date, the date is unknown and the call must fail instead of
// returning a cursor that restarts the list.
func TestAudit10_TopicsCursorNeedsADate(t *testing.T) {
	response := forumResponse([]forumItem{{201, false}, {202, false}, {203, false}}, 3)
	response.Messages = nil
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		if _, ok := input.(*tg.MessagesGetForumTopicsRequest); !ok {
			t.Fatalf("req = %T", input)
		}
		return respond(output, response)
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	result, err := topicsAPI(context.Background(), api, peer, "", 2)
	e := audit10RPCError(err)
	if e == nil || e.Code != rpc.CodeTelegram || e.Message != "Telegram did not return the message date needed for the topic cursor" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
