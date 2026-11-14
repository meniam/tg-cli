package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func (a *app) messageScheduledCommand() *cobra.Command {
	root := &cobra.Command{Use: "scheduled", Short: "Manage scheduled messages"}
	list := a.annotate(&cobra.Command{Use: "list <chat>", Short: "List scheduled messages", Args: cobra.ExactArgs(1), RunE: a.scheduledList}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ScheduledResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "messages"})
	makeWrite := func(name, method string) *cobra.Command {
		cmd := a.annotate(&cobra.Command{Use: name + " <chat> <id>...", Short: "Manage scheduled messages", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := messageIDs(args[1:])
			if err != nil {
				return err
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			var res rpc.ScheduledResult
			return a.writeCall(cmd, method, rpc.ScheduledParams{Chat: args[0], IDs: ids, DryRun: dry}, &res)
		}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.ScheduledResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true, Variadic: true}}})
		cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
		return cmd
	}
	root.AddCommand(list, makeWrite("delete", rpc.MethodMessageScheduledDelete), makeWrite("send-now", rpc.MethodMessageScheduledSendNow))
	return root
}
func (a *app) scheduledList(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var result rpc.ScheduledResult
	if err := a.callOne(cmd, rpc.MethodMessageScheduledList, rpc.ScheduledParams{Chat: args[0]}, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Messages, "")
}
