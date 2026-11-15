package tgclient

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestResolvePhoneRequestAndNotFound(t *testing.T) {
	for _, missing := range []bool{false, true} {
		calls := 0
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
			calls++
			req, ok := input.(*tg.ContactsResolvePhoneRequest)
			if !ok || req.Phone != "15551234567" {
				t.Fatalf("request: %T", input)
			}
			if missing {
				return tgerr.New(400, "PHONE_NOT_OCCUPIED")
			}
			return respond(out, &tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: 7}, Users: []tg.UserClass{user(7, "Ann", "", "", false)}})
		}))
		v, err := resolvePhoneAPI(context.Background(), api, testPeers(api), "15551234567")
		if missing {
			if rpcErrorCode(t, err) != rpc.CodeNotFound {
				t.Fatalf("not found: %v", err)
			}
		} else if err != nil || v.ID != 7 {
			t.Fatalf("resolved: %+v %v", v, err)
		}
		if calls != 1 {
			t.Fatalf("calls: %d", calls)
		}
	}
}
