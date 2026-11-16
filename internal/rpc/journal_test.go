package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRequestJournalRecordsOutcomeWithoutParameters(t *testing.T) {
	var journal bytes.Buffer
	s := NewServer(slog.New(slog.NewTextHandler(&journal, nil)))
	s.Handle("ok", func(context.Context, json.RawMessage) (any, error) { return "message body", nil })
	s.Handle("failed", func(context.Context, json.RawMessage) (any, error) {
		return nil, NewError(CodeInvalidParams, "secret error text")
	})
	s.Handle("panic", func(context.Context, json.RawMessage) (any, error) { panic("secret panic text") })
	for _, tc := range []struct {
		method string
		code   int
	}{{"ok", 0}, {"failed", CodeInvalidParams}, {"panic", CodeInternal}, {"secret unknown method", CodeMethodNotFound}} {
		response := s.call(context.Background(), Request{Method: tc.method, Params: json.RawMessage(`{"password":"secret parameter"}`)})
		if tc.code == 0 && response.Error != nil {
			t.Errorf("%s: unexpected error %+v", tc.method, response.Error)
		}
		if tc.code != 0 && (response.Error == nil || response.Error.Code != tc.code) {
			t.Errorf("%s: response = %+v, want code %d", tc.method, response, tc.code)
		}
	}
	var requests, failures []string
	for _, line := range strings.Split(strings.TrimSpace(journal.String()), "\n") {
		if strings.Contains(line, "msg=request") {
			requests = append(requests, line)
		} else {
			failures = append(failures, line)
		}
	}
	if len(requests) != 4 {
		t.Fatalf("request records = %d: %q", len(requests), journal.String())
	}
	requestText := strings.Join(requests, "\n")
	for _, want := range []string{"method=ok", "method=failed", "method=panic", "code=ok", "code=-32602", "code=-32603", "code=-32601", "duration_ms="} {
		if !strings.Contains(requestText, want) {
			t.Errorf("missing %q: %q", want, requestText)
		}
	}
	// Request records carry no parameters, results, error text or unknown method names.
	for _, secret := range []string{"secret parameter", "message body", "secret error text", "secret panic text", "secret unknown method"} {
		if strings.Contains(requestText, secret) {
			t.Errorf("request record leaked %q", secret)
		}
	}
	// Debug records for unexpected failures keep the cause but never parameters or results.
	failureText := strings.Join(failures, "\n")
	if !strings.Contains(failureText, "handler failed") || !strings.Contains(failureText, "secret panic text") {
		t.Errorf("missing the cause of the panic: %q", failureText)
	}
	if !strings.Contains(failureText, "handler panic") || !strings.Contains(failureText, "stack=") {
		t.Errorf("missing the panic stack: %q", failureText)
	}
	for _, secret := range []string{"secret parameter", "message body", "secret error text", "secret unknown method"} {
		if strings.Contains(failureText, secret) {
			t.Errorf("failure record leaked %q", secret)
		}
	}
}

func TestTranslationJournalCountsWithoutText(t *testing.T) {
	var journal bytes.Buffer
	s := NewServer(slog.New(slog.NewTextHandler(&journal, nil)))
	s.Handle(MethodMessageTranslate, func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	s.call(context.Background(), Request{Method: MethodMessageTranslate, Params: json.RawMessage(`{"ids":[7,9],"to_lang":"secret-language","chat":"secret-chat"}`)})
	got := journal.String()
	if !strings.Contains(got, "id_count=2") || strings.Contains(got, "secret-") {
		t.Fatalf("journal: %s", got)
	}
}
