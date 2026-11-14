package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) chatTypingCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "typing <chat>", Short: "Set a typing indicator", Args: cobra.ExactArgs(1), RunE: a.chatTyping}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.TypingResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.Flags().String("action", "typing", "typing action")
	cmd.Flags().Int("topic", 0, "forum topic id")
	cmd.Flags().Bool("cancel", false, "cancel the indicator")
	cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
	return cmd
}
func (a *app) chatTyping(cmd *cobra.Command, args []string) error {
	action, _ := cmd.Flags().GetString("action")
	topic, _ := cmd.Flags().GetInt("topic")
	cancel, _ := cmd.Flags().GetBool("cancel")
	dry, _ := cmd.Flags().GetBool("dry-run")
	if cancel {
		if cmd.Flags().Changed("action") && action != "cancel" {
			return fail(exitcode.Usage, "--cancel conflicts with --action")
		}
		action = "cancel"
	}
	p := rpc.TypingParams{Chat: args[0], Action: action, Topic: topic, DryRun: dry}
	if err := rpc.ValidateTypingParams(p); err != nil {
		return fail(exitcode.Usage, "%v", err)
	}
	var result rpc.TypingResult
	return a.writeCall(cmd, rpc.MethodChatTyping, p, &result)
}
