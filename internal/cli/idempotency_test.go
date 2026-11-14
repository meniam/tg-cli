package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func TestIdempotencyKeyFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"valid:1._-", true}, {"", false}, {"bad key", false}, {strings.Repeat("a", 65), false}} {
		cmd := &cobra.Command{}
		cmd.Flags().String("idempotency-key", "", "")
		if err := cmd.Flags().Set("idempotency-key", tc.value); err != nil {
			t.Fatal(err)
		}
		got, err := withIdempotencyKey(cmd, rpc.SendParams{})
		if (err == nil) != tc.valid {
			t.Fatalf("key %q: %v", tc.value, err)
		}
		if err == nil && got.(rpc.SendParams).IdempotencyKey != tc.value {
			t.Fatal("key not passed")
		}
	}
}
