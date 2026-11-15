package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

func TestTranscribeCommand(t *testing.T) {
	r := runDiscoveryCLI(t, []string{"message", "transcribe", "https://t.me/example/7", "--wait", "0s"}, func(method string, raw json.RawMessage) (any, error) {
		var p rpc.TranscribeParams
		if err := json.Unmarshal(raw, &p); err != nil || method != rpc.MethodMessageTranscribe || p.Chat != "@example" || p.ID != 7 || p.WaitMS != 0 {
			t.Fatalf("request: %s %+v %v", method, p, err)
		}
		return rpc.Transcription{ChatID: 42, ID: 7, Pending: true}, nil
	}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"pending":true`) || !strings.Contains(r.stderr, "pending") {
		t.Fatalf("result: %+v", r)
	}
	r = runDiscoveryCLI(t, []string{"message", "transcribe", "42", "7"}, func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.TranscribeParams
		if err := json.Unmarshal(raw, &p); err != nil || p.WaitMS != 30000 {
			t.Fatalf("default wait: %+v %v", p, err)
		}
		return rpc.Transcription{ChatID: 42, ID: 7, Text: "ready"}, nil
	}, nil)
	if r.code != 0 {
		t.Fatalf("default: %+v", r)
	}
	for _, wait := range []string{"-1s", "91s"} {
		r = runDiscoveryCLI(t, []string{"message", "transcribe", "42", "7", "--wait", wait}, nil, nil)
		if r.code != 2 || r.method != "" {
			t.Fatalf("wait %s: %+v", wait, r)
		}
	}
}

func TestTranscribeErrorDetails(t *testing.T) {
	r := runDiscoveryCLI(t, []string{"message", "transcribe", "42", "7", "--wait", "0s"}, func(string, json.RawMessage) (any, error) {
		return nil, &rpc.Error{Code: rpc.CodeTelegram, Message: "unavailable", Data: map[string]any{"reason": "premium_required", "trial_remains": 0}}
	}, nil)
	if r.code != exitcode.Telegram || !strings.Contains(r.stdout, `"reason":"premium_required"`) || !strings.Contains(r.stdout, `"trial_remains":0`) {
		t.Fatalf("RPC error: %+v", r)
	}
	translated := translate(&rpc.Error{Code: rpc.CodeTelegram, Message: "unavailable", Data: map[string]any{"reason": "premium_required", "trial_remains": float64(0), "trial_resets_at": "2026-01-01T00:00:00Z"}}).(*cmdError)
	if translated.exit != exitcode.Telegram {
		t.Fatalf("exit: %d", translated.exit)
	}
	b, err := json.Marshal(output.FailureDataWithDetails("telegram", translated.msg, 0, translated.data))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Error output.ErrorBody `json:"error"`
	}
	if err = json.Unmarshal(b, &got); err != nil || got.Error.Reason != "premium_required" || got.Error.TrialRemains == nil || *got.Error.TrialRemains != 0 || got.Error.TrialResetsAt == "" {
		t.Fatalf("error body: %s %v", b, err)
	}
	b, _ = json.Marshal(output.FailureDataWithDetails("telegram", "failed", 0, nil))
	var plain map[string]any
	_ = json.Unmarshal(b, &plain)
	if _, ok := plain["error"].(map[string]any)["reason"]; ok {
		t.Fatalf("unexpected reason: %s", b)
	}
	b, _ = json.Marshal(output.FailureDataWithDetails("flood_wait", "wait", 0, map[string]any{"retry_after_seconds": float64(3)}))
	if !strings.Contains(string(b), `"retry_after_seconds":3`) {
		t.Fatalf("retry details: %s", b)
	}
}
