package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func (a *app) chatStatsCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "stats <chat>", Short: "Read channel or supergroup statistics", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		graph, _ := cmd.Flags().GetString("graph")
		var result rpc.ChatStats
		if err := a.callOne(cmd, rpc.MethodChatStats, rpc.ChatStatsParams{Chat: args[0], Graph: graph}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatStats](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.Flags().String("graph", "", "load a named statistics graph")
	return cmd
}
func (a *app) chatBoostsCommand() *cobra.Command {
	return a.annotate(&cobra.Command{Use: "boosts <chat>", Short: "Read channel or supergroup boost status", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var result rpc.ChatBoosts
		if err := a.callOne(cmd, rpc.MethodChatBoosts, rpc.ChatRefParams{Chat: args[0]}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatBoosts](), Args: []argSpec{{Name: "chat", Required: true}}})
}
