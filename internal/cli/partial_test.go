package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestPartialWriteExitAndFields(t *testing.T) {
	for _, format := range []string{"json", "toon", "human"} {
		t.Run(format, func(t *testing.T) {
			r := runWriteCLI(t, []string{"message", "forward", "42", "42", "1", "2", "--format", format, "--fields", "messages.id"}, "", func(string, json.RawMessage) (any, error) {
				return rpc.ForwardResult{Batch: rpc.Batch{Partial: true, Errors: []rpc.ItemError{{ID: 2, Code: "not_found", Message: "missing"}}}, ChatID: 42, Messages: []rpc.ForwardedMessage{{ID: 10, Date: "2026-01-01T00:00:00Z"}}}, nil
			}, nil)
			if r.code != 1 || !strings.Contains(r.stdout, "partial") || !strings.Contains(r.stdout, "not_found") || strings.Count(r.stdout, "not_found") != 1 {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestErrorOutputRedactsClientAndRPCText(t *testing.T) {
	secret := "socks5://user:secret@host"
	for _, tc := range []struct {
		name  string
		reply func(string, json.RawMessage) (any, error)
		dial  error
	}{
		{name: "rpc", reply: func(string, json.RawMessage) (any, error) { return nil, rpc.NewError(rpc.CodeTelegram, secret) }},
		{name: "dial", dial: errors.New(secret)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, []string{"message", "forward", "42", "42", "1"}, "", tc.reply, tc.dial)
			if r.code == 0 || strings.Contains(r.stdout+r.stderr, "user:secret") {
				t.Fatalf("output=%+v", r)
			}
		})
	}
}

func TestScheduledHumanPartialCount(t *testing.T) {
	r := runWriteCLI(t, []string{"message", "scheduled", "send-now", "42", "1", "2", "3", "--human"}, "", func(string, json.RawMessage) (any, error) {
		return rpc.ScheduledResult{Batch: rpc.Batch{Partial: true, Errors: []rpc.ItemError{{ID: 3, Code: "flood_wait", Message: "wait"}}}, ChatID: 42}, nil
	}, nil)
	if r.code != 1 || !strings.Contains(r.stdout, "partial: 1 of 3 failed") {
		t.Fatalf("%+v", r)
	}
}
