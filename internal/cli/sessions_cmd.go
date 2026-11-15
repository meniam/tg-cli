package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) terminateCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "terminate <id>", Short: "Terminate another account session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		dry, _ := cmd.Flags().GetBool("dry-run")
		p := rpc.TerminateParams{ID: args[0], Yes: yes, DryRun: dry}
		if err := rpc.ValidateTerminateParams(p); err != nil {
			return fail(exitcode.Usage, "%v", err)
		}
		var out rpc.TerminateResult
		return a.writeCall(cmd, rpc.MethodAccountTerminate, p, &out)
	}}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.TerminateResult](), Args: []argSpec{{Name: "id", Required: true}}})
	cmd.Flags().Bool("yes", false, "confirm session termination")
	cmd.Flags().Bool("dry-run", false, "preview without terminating the session")
	return cmd
}
