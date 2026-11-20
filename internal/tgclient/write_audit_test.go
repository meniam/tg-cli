package tgclient

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/throttle"
)

// TestAudit10_WritesAreNotRetriedByTheThrottleMiddleware sends write requests
// through the production middleware. A message edit belongs to the general
// bucket, which retries a short FLOOD_WAIT for reads, so only the writeRequest
// rule keeps the edit from being sent twice.
func TestAudit10_WritesAreNotRetriedByTheThrottleMiddleware(t *testing.T) {
	user := &tg.InputPeerUser{UserID: 42}
	for _, request := range []bin.Encoder{
		&tg.MessagesSendMessageRequest{Peer: user},
		&tg.MessagesEditMessageRequest{Peer: user, ID: 5},
		&tg.MessagesForwardMessagesRequest{FromPeer: user, ToPeer: user},
		&tg.MessagesDeleteMessagesRequest{ID: []int{1}},
	} {
		t.Run(fmt.Sprintf("%T", request), func(t *testing.T) {
			limiter, err := throttle.New(throttle.DefaultConfig(), nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			invoke := throttleMiddleware(limiter).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
				calls++
				return tgerr.New(420, "FLOOD_WAIT_1")
			}))
			err = invoke(context.Background(), request, nil)
			if _, flood := tgerr.AsFloodWait(err); !flood || calls != 1 {
				t.Fatalf("calls=%d err=%v, want the flood returned after one request", calls, err)
			}
		})
	}
}
