package daemon

import (
	"errors"
	"strings"
	"testing"
	"tg-cli/internal/rpc"
)

func TestLoginErrorsDoNotEchoInput(t *testing.T) {
	for _, err := range []error{errors.New("12345 password=hunter2"), rpc.NewError(rpc.CodeTelegram, "12345"), rpc.NewError(rpc.CodeTelegram, "hunter2")} {
		got := safeLoginError(err).Error()
		if strings.Contains(got, "12345") || strings.Contains(got, "hunter2") {
			t.Fatalf("secret in %q", got)
		}
	}
	if got := safeLoginError(rpc.NewError(rpc.CodeTelegram, "PHONE_CODE_INVALID")).Error(); !strings.Contains(got, "PHONE_CODE_INVALID") {
		t.Fatal(got)
	}
}
