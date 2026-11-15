package cli

import (
	"github.com/spf13/cobra"
	"regexp"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

func withIdempotencyKey(cmd *cobra.Command, params any) (any, error) {
	flag := cmd.Flags().Lookup("idempotency-key")
	if flag == nil || !flag.Changed {
		return params, nil
	}
	key := flag.Value.String()
	if !idempotencyKeyPattern.MatchString(key) {
		return nil, fail(exitcode.Usage, "--idempotency-key must contain 1 to 64 characters A-Za-z0-9._:-")
	}
	switch p := params.(type) {
	case rpc.SendParams:
		p.IdempotencyKey = key
		return p, nil
	case rpc.SendMediaParams:
		p.IdempotencyKey = key
		return p, nil
	case rpc.SendStickerParams:
		p.IdempotencyKey = key
		return p, nil
	case rpc.SendInlineParams:
		p.IdempotencyKey = key
		return p, nil
	case rpc.ForwardParams:
		p.IdempotencyKey = key
		return p, nil
	case rpc.PollParams:
		p.IdempotencyKey = key
		return p, nil
	}
	return params, nil
}
