package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func (a *app) chatChangesCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "changes <chat>", Short: "Show locally stored chat changes (read only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		limit, err := cliPageLimit(cmd)
		if err != nil {
			return err
		}
		var result rpc.ChatChangesResult
		if err = a.callOne(cmd, rpc.MethodChatChanges, rpc.ChatChangesParams{Chat: args[0], Limit: limit}, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Versions, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatChangesResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "versions"})
	cmd.Flags().Int("limit", 20, "maximum versions, 1 to 100")
	return cmd
}
