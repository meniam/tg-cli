package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) chatUnreadCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "unread", Short: "List chats with unread messages (read only)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		limit, err := chatListLimit(cmd, 500)
		if err != nil {
			return err
		}
		archived, _ := cmd.Flags().GetBool("archived")
		var result rpc.UnreadChatsResult
		if err = a.callOne(cmd, rpc.MethodChatUnread, rpc.ChatUnreadParams{Limit: limit, Archived: archived}, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Chats, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.UnreadChatsResult](), ItemPath: "chats"})
	cmd.Flags().Int("limit", 100, "maximum unread chats, 1 to 500")
	cmd.Flags().Bool("archived", false, "read archived dialogs only")
	return cmd
}
func (a *app) messageUnreadCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "unread <chat>", Short: "Read unread incoming messages (read only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		p := rpc.MessageUnreadParams{Chat: args[0]}
		p.Limit, err = cliPageLimit(cmd)
		if err != nil {
			return err
		}
		p.Topic, _ = cmd.Flags().GetInt("topic")
		if p.Topic < 0 || (cmd.Flags().Changed("topic") && p.Topic == 0) {
			return fail(exitcode.Usage, "--topic must be a positive topic id")
		}
		p.Cursor, _ = cmd.Flags().GetString("cursor")
		var result rpc.UnreadMessagesResult
		if err = a.callOne(cmd, rpc.MethodMessageUnread, p, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Messages, result.NextCursor)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.UnreadMessagesResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "messages"})
	cmd.Flags().Int("limit", 20, "messages per page, 1 to 100")
	cmd.Flags().Int("topic", 0, "forum topic id")
	cmd.Flags().String("cursor", "", "continue from next_cursor")
	return cmd
}
