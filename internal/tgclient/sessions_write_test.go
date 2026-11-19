package tgclient

import (
	"context"
	"errors"
	"strings"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"testing"
	"tg-cli/internal/rpc"
)

func TestFreshSessionCannotTerminate(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		req, ok := in.(*tg.AccountResetAuthorizationRequest)
		if !ok || req.Hash != 123 {
			t.Errorf("request %T %+v", in, in)
			return errors.New("wrong request")
		}
		return tgerr.New(400, "FRESH_RESET_AUTHORISATION_FORBIDDEN")
	}))
	err := terminateSessionAPI(context.Background(), api, 123)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeTelegram || !strings.Contains(re.Message, "too new to terminate") {
		t.Fatalf("error %v", err)
	}
}
