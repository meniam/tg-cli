package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) chatStateCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "state [chat]", Short: "Show saved or current dialog state (read only)", Long: "Show the state of one chat or the first dialogs. Use -- before a negative chat id.\nUse --local to read the saved state without a Telegram connection.", Args: cobra.MaximumNArgs(1), RunE: a.chatState}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatStatesResult](), Args: []argSpec{{Name: "chat", Required: false}}, ItemPath: "states"})
	cmd.Flags().Int("limit", 100, "maximum dialogs when no chat is given, 1 to 500")
	cmd.Flags().Bool("local", false, "read saved state without Telegram")
	return cmd
}
func (a *app) chatState(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := chatListLimit(cmd, 500)
	if err != nil {
		return err
	}
	if len(args) > 0 && cmd.Flags().Changed("limit") {
		return fail(exitcode.Usage, "--limit cannot be combined with a chat")
	}
	local, _ := cmd.Flags().GetBool("local")
	p := rpc.ChatStateParams{Limit: limit, Local: local}
	if len(args) > 0 {
		p.Chat = args[0]
		p.Limit = 0
	}
	var result rpc.ChatStatesResult
	if err = a.callOne(cmd, rpc.MethodChatState, p, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.States, "")
}
