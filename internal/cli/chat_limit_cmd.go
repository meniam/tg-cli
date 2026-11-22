package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// chatLimitCommand is the `chat limit` group: a cap on how many messages the
// local database keeps for one chat. The commands change only the local
// database; none of them calls Telegram.
func (a *app) chatLimitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "limit",
		Short: "Cap how many messages the local database keeps for a chat",
		Long: "A chat has no limit by default. With a limit, the Daemon deletes the oldest saved\n" +
			"messages of the chat once it holds more than the limit and leaves 95% of the limit,\n" +
			"so the cleanup does not run after every new message. It runs about once an hour and\n" +
			"right after a limit is set. Deleted messages do not come back, and the chat counts\n" +
			"as completely loaded, so a history load does not fetch them again. Telegram is\n" +
			"never changed.",
	}
	set := a.annotate(&cobra.Command{
		Use:   "set <chat> <messages>",
		Short: "Set the message limit of a chat",
		Long: "Set the limit, a whole number from 100 to 100000000, for a chat the local database\n" +
			"knows (a numeric id or a @username; put -- before a negative chat id). When the chat\n" +
			"holds more messages than the limit, the cleanup deletes the oldest ones for good, so\n" +
			"the command refuses with code 2 and the numbers until you repeat it with --yes.",
		Args: cobra.ExactArgs(2), RunE: a.chatLimitSet,
	}, dangerous, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ChatLimitSetResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "messages", Required: true}}})
	set.Flags().Bool("yes", false, "confirm the deletion when the chat holds more messages than the limit")
	off := a.annotate(&cobra.Command{
		Use:   "off <chat>",
		Short: "Remove the message limit of a chat",
		Long: "Remove the limit. Messages the cleanup already deleted do not come back. Put -- before\n" +
			"a negative chat id.",
		Args: cobra.ExactArgs(1), RunE: a.chatLimitOff,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ChatLimitOffResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	list := a.annotate(&cobra.Command{
		Use:   "list",
		Short: "List the chats with a message limit (read only)",
		Long:  "List the limited chats that access.json leaves open, by chat id, with the limit and the number of saved messages.",
		Args:  cobra.NoArgs, RunE: a.chatLimitList,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatLimitListResult](), ItemPath: "chats"})
	cmd.AddCommand(set, off, list)
	return cmd
}

func (a *app) chatLimitSet(cmd *cobra.Command, args []string) error {
	messages, err := strconv.Atoi(args[1])
	if err != nil {
		return fail(exitcode.Usage, "messages must be a whole number")
	}
	yes, _ := cmd.Flags().GetBool("yes")
	var res rpc.ChatLimitSetResult
	if err := a.callOne(cmd, rpc.MethodChatLimitSet, rpc.ChatLimitSetParams{Chat: args[0], Messages: messages, Confirm: yes}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) chatLimitOff(cmd *cobra.Command, args []string) error {
	var res rpc.ChatLimitOffResult
	if err := a.callOne(cmd, rpc.MethodChatLimitOff, rpc.ChatLimitOffParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) chatLimitList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var res rpc.ChatLimitListResult
	if err := a.callOne(cmd, rpc.MethodChatLimitList, rpc.ChatLimitListParams{}, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Chats, "")
}
