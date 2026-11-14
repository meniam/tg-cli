package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

func (a *app) chatCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "chat", Short: "Read chats and load local history"}

	list := a.annotate(&cobra.Command{
		Use:   "list",
		Short: "List chats, newest first (read only)",
		Args:  cobra.NoArgs,
		RunE:  a.chatList,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatListResult](), Choices: chatTypeChoices(), ItemPath: "chats"})
	list.Flags().Int("limit", 50, "maximum number of chats, 1 to 500; 0 means 50")
	list.Flags().String("type", "", "chat type: user, bot, group, supergroup or channel")

	resolve := a.annotate(&cobra.Command{
		Use:   "resolve <chat>",
		Short: "Find a chat by @username, id, phone number of a contact or t.me link (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.chatResolve,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Chat](), Args: []argSpec{{Name: "chat", Required: true}}})
	info := a.annotate(&cobra.Command{
		Use:   "info <chat>",
		Short: "Show a chat with its description and size (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.chatInfo,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatInfo](), Args: []argSpec{{Name: "chat", Required: true}}})
	invite := a.annotate(&cobra.Command{
		Use:   "invite-info <link>",
		Short: "Show what an invite link leads to, without joining (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.chatInviteInfo,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.InviteInfo](), Args: []argSpec{{Name: "link", Required: true}}})

	cmd.AddCommand(list, resolve, info, invite, a.chatMembersCommand(), a.chatTopicsCommand(), a.chatCommonCommand(), a.chatMarkReadCommand(), a.chatTypingCommand(), a.chatDraftCommand(), a.chatSyncCommand())
	return cmd
}

func (a *app) chatList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	chatType, _ := cmd.Flags().GetString("type")
	var res rpc.ChatListResult
	if err := a.callOne(cmd, rpc.MethodChatList, rpc.ChatListParams{Limit: limit, Type: chatType}, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Chats, "")
}
