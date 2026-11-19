package rpc

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestErrorRedactionAtServerBoundary(t *testing.T) {
	var log bytes.Buffer
	s := NewServer(slog.New(slog.NewTextHandler(&log, nil)))
	original := NewError(CodeTelegram, "password=private")
	result := s.toError("test", original)
	if strings.Contains(result.Message, "private") || original.Message != "password=private" {
		t.Fatalf("result=%q original=%q", result.Message, original.Message)
	}
	result = s.toError("test", &Error{Code: CodeTelegram, Message: "request failed", Cause: errors.New("socks5://user:private@host"), Data: map[string]any{"detail": "password=private"}})
	if strings.Contains(log.String(), "private") || strings.Contains(result.Data.(map[string]any)["detail"].(string), "private") {
		t.Fatalf("result=%+v log=%q", result, log.String())
	}
	result = s.toError("test", errors.New("secret=private"))
	if result.Message != "internal error" || strings.Contains(log.String(), "private") {
		t.Fatalf("result=%q log=%q", result.Message, log.String())
	}
	// The journal keeps the redacted cause of both kinds of failure: a typed
	// error with a cause and a plain error. The first call had no cause.
	if n := strings.Count(log.String(), "handler failed"); n != 2 {
		t.Fatalf("handler failed records = %d, want 2: %q", n, log.String())
	}
	if !strings.Contains(log.String(), "[redacted]") {
		t.Fatalf("the journal does not show the redaction: %q", log.String())
	}
	if result := s.toError("test", NewError(CodeTelegram, "password=private")); result.Message != "[redacted]" {
		t.Fatalf("message = %q, want [redacted]", result.Message)
	}
}
