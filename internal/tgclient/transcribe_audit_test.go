package tgclient

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

// audit10Reason returns the machine readable reason of an rpc error, or "" when
// the error carries no reason map.
func audit10Reason(e *rpc.Error) string {
	data, ok := e.Data.(map[string]any)
	if !ok {
		return ""
	}
	reason, _ := data["reason"].(string)
	return reason
}

func TestAudit10_TranscribeErrorsAndResult(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       string
		wantReason string
	}{
		{"premium", "PREMIUM_ACCOUNT_REQUIRED", "premium_required"},
		{"failed", "TRANSCRIPTION_FAILED", "transcription_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(400, tc.code) }))
			_, err := transcribeResolved(context.Background(), api, testPeers(api).User(user(9, "A", "", "", false)), 17)
			e := audit10RPCError(err)
			if e == nil || e.Code != rpc.CodeTelegram || audit10Reason(e) != tc.wantReason {
				t.Fatalf("err=%v reason=%q, want %q", err, audit10Reason(e), tc.wantReason)
			}
		})
	}
	t.Run("other error goes through the common mapping", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(420, "FLOOD_WAIT_9") }))
		_, err := transcribeResolved(context.Background(), api, testPeers(api).User(user(9, "A", "", "", false)), 17)
		if e := audit10RPCError(err); e == nil || e.Code != rpc.CodeFloodWait || audit10Reason(e) != "" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("success carries every field", func(t *testing.T) {
		reset := int(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Unix())
		api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			answer := &tg.MessagesTranscribedAudio{TranscriptionID: 8, Text: "hello", Pending: true}
			answer.SetTrialRemainsNum(2)
			answer.SetTrialRemainsUntilDate(reset)
			return respond(out, answer)
		}))
		got, err := transcribeResolved(context.Background(), api, testPeers(api).User(user(9, "A", "", "", false)), 17)
		if err != nil {
			t.Fatal(err)
		}
		if got.ChatID != 9 || got.ID != 17 || got.TranscriptionID != 8 || got.Text != "hello" || !got.Pending ||
			got.TrialRemains == nil || *got.TrialRemains != 2 || got.TrialResetsAt != formatDate(reset) || got.TrialResetsAt == "" {
			t.Fatalf("result=%+v", got)
		}
	})
}
