package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

// chatOverviewCommand reads the local chat overview. It never calls Telegram, so
// it answers without a connection and without a login, like `search`.
func (a *app) chatOverviewCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use:   "overview",
		Short: "Summarise stored chats from the local database (read only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := a.outputSettingsFor(cmd)
			if err != nil {
				return err
			}
			limit, _ := cmd.Flags().GetInt("limit")
			chatType, _ := cmd.Flags().GetString("type")
			var result rpc.ChatOverviewResult
			if err := a.callOne(cmd, rpc.MethodChatOverview, rpc.ChatOverviewParams{Limit: limit, Type: chatType}, &result); err != nil {
				return err
			}
			return emitList(a, cmd, settings, result, result.Chats, "")
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatOverviewResult](), Choices: chatTypeChoices(), ItemPath: "chats"})
	cmd.Flags().Int("limit", 50, "maximum number of chats, 1 to 500")
	cmd.Flags().String("type", "", "chat type: user, bot, group, supergroup or channel")
	return cmd
}
