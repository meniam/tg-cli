package tgclient

import (
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"testing"
	"tg-cli/internal/rpc"
)

func TestFreshSessionCannotTerminate(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		if _, ok := in.(*tg.AccountResetAuthorizationRequest); !ok {
			t.Fatalf("request %T", in)
		}
		return tgerr.New(400, "FRESH_RESET_AUTHORISATION_FORBIDDEN")
	}))
	err := terminateSessionAPI(context.Background(), api, 123)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeTelegram {
		t.Fatalf("error %v", err)
	}
}
