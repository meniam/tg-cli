package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

func isPartial(data any) bool {
	value, ok := data.(interface{ IsPartial() bool })
	return ok && value.IsPartial()
}

func printPartialSummary(w io.Writer, cmd *cobra.Command, data any) error {
	var failed, total int
	switch v := data.(type) {
	case *rpc.DeleteResult:
		failed, total = len(v.Errors), len(v.Errors)+len(v.Deleted)
	case *rpc.ForwardResult:
		failed, total = len(v.Errors), len(v.Errors)+len(v.Messages)
	case *rpc.ScheduledResult:
		failed = len(v.Errors)
		total = len(cmd.Flags().Args()) - 1
		if total < failed {
			total = failed
		}
	}
	_, err := fmt.Fprintf(w, "partial: %d of %d failed\n", failed, total)
	return err
}
