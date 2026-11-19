package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func audit02KeyCmd(t *testing.T, value *string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("idempotency-key", "", "")
	if value != nil {
		if err := cmd.Flags().Set("idempotency-key", *value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func TestIdempotencyKeyFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"valid:1._-", true}, {"", false}, {"bad key", false}, {strings.Repeat("a", 64), true}, {strings.Repeat("a", 65), false}} {
		value := tc.value
		got, err := withIdempotencyKey(audit02KeyCmd(t, &value), rpc.SendParams{})
		if (err == nil) != tc.valid {
			t.Fatalf("key %q: %v", tc.value, err)
		}
		if err == nil && got.(rpc.SendParams).IdempotencyKey != tc.value {
			t.Fatal("key not passed")
		}
	}
}

// TestAudit02_IdempotencyKeyReachesEveryParamsType covers the whole switch of
// withIdempotencyKey: a type that loses its case would send without a key.
func TestAudit02_IdempotencyKeyReachesEveryParamsType(t *testing.T) {
	const key = "key-1"
	for _, tc := range []struct {
		name   string
		params any
		key    func(any) string
	}{
		{"send", rpc.SendParams{Chat: "42"}, func(v any) string { return v.(rpc.SendParams).IdempotencyKey }},
		{"send media", rpc.SendMediaParams{Chat: "42"}, func(v any) string { return v.(rpc.SendMediaParams).IdempotencyKey }},
		{"send sticker", rpc.SendStickerParams{Chat: "42"}, func(v any) string { return v.(rpc.SendStickerParams).IdempotencyKey }},
		{"send inline", rpc.SendInlineParams{Chat: "42"}, func(v any) string { return v.(rpc.SendInlineParams).IdempotencyKey }},
		{"forward", rpc.ForwardParams{From: "42"}, func(v any) string { return v.(rpc.ForwardParams).IdempotencyKey }},
		{"poll", rpc.PollParams{Chat: "42"}, func(v any) string { return v.(rpc.PollParams).IdempotencyKey }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := key
			got, err := withIdempotencyKey(audit02KeyCmd(t, &value), tc.params)
			if err != nil || tc.key(got) != key {
				t.Fatalf("key = %q, err %v", tc.key(got), err)
			}
			// Without the flag the params come back unchanged and without a key.
			got, err = withIdempotencyKey(audit02KeyCmd(t, nil), tc.params)
			if err != nil || tc.key(got) != "" {
				t.Fatalf("without the flag: key = %q, err %v", tc.key(got), err)
			}
		})
	}
	// A command without the flag at all (no Lookup result) must not fail.
	got, err := withIdempotencyKey(&cobra.Command{}, rpc.SendParams{Chat: "42"})
	if err != nil || got.(rpc.SendParams).IdempotencyKey != "" {
		t.Fatalf("no flag defined: %+v %v", got, err)
	}
}
