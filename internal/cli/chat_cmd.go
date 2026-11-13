package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

func (a *app) chatCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "chat", Short: "Read the chats of the account"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List chats, newest first (read only)",
		Args:  cobra.NoArgs,
		RunE:  a.chatList,
	}
	list.Flags().Int("limit", 50, "maximum number of chats, 1 to 500")
	list.Flags().String("type", "", "only chats of this type: user, bot, group, supergroup, channel")
	addFormatFlag(list)

	resolve := &cobra.Command{
		Use:   "resolve <chat>",
		Short: "Find a chat by @username, id, phone number of a contact or t.me link (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.chatResolve,
	}
	info := &cobra.Command{
		Use:   "info <chat>",
		Short: "Show a chat with its description and size (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.chatInfo,
	}
	invite := &cobra.Command{
		Use:   "invite-info <link>",
		Short: "Show what an invite link leads to, without joining (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.chatInviteInfo,
	}

	cmd.AddCommand(list, resolve, info, invite)
	return cmd
}

func (a *app) chatList(cmd *cobra.Command, _ []string) error {
	f, err := format(cmd)
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")
	chatType, _ := cmd.Flags().GetString("type")
	var res rpc.ChatListResult
	if err := a.callOne(cmd, rpc.MethodChatList, rpc.ChatListParams{Limit: limit, Type: chatType}, &res); err != nil {
		return err
	}
	return emitList(a, f, res, res.Chats, "")
}
