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

// Only the five whitelisted messages survive, each with its own code.
func TestAudit05_LoginErrorWhitelist(t *testing.T) {
	for _, message := range []string{"PHONE_CODE_INVALID", "PHONE_CODE_EXPIRED", "PHONE_NUMBER_INVALID", "PASSWORD_HASH_INVALID", "SESSION_PASSWORD_NEEDED"} {
		got := audit05GuardCode(t, safeLoginError(rpc.NewError(rpc.CodeTelegram, message)))
		if got != rpc.CodeTelegram {
			t.Errorf("%s: code %d", message, got)
		}
		if text := safeLoginError(rpc.NewError(rpc.CodeTelegram, message)).Error(); !strings.Contains(text, message) {
			t.Errorf("%s was rewritten to %q", message, text)
		}
	}
}

// A flood wait keeps its Data so the caller can wait, but the upstream text
// (which may echo the input) is replaced.
func TestAudit05_LoginErrorFloodWaitKeepsData(t *testing.T) {
	upstream := &rpc.Error{Code: rpc.CodeFloodWait, Message: "FLOOD_WAIT_30 for +15551234567", Data: map[string]any{"retry_after_seconds": 30}}
	err := safeLoginError(upstream)
	var got *rpc.Error
	if !errors.As(err, &got) || got.Code != rpc.CodeFloodWait {
		t.Fatalf("flood wait: %v", err)
	}
	data, ok := got.Data.(map[string]any)
	if !ok || data["retry_after_seconds"] != 30 {
		t.Fatalf("data lost: %#v", got.Data)
	}
	if strings.Contains(got.Message, "15551234567") || !strings.Contains(got.Message, "FLOOD_WAIT") {
		t.Fatalf("message %q", got.Message)
	}
}

func TestAudit05_LoginErrorNotAuthorizedHidesText(t *testing.T) {
	err := safeLoginError(rpc.NewError(rpc.CodeNotAuthorized, "AUTH_KEY_UNREGISTERED hunter2"))
	if audit05GuardCode(t, err) != rpc.CodeNotAuthorized || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("not authorized: %v", err)
	}
}
