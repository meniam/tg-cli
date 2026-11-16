package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

// chatFullCommand drives the background job that reads the full data of groups and
// channels: one Telegram request per chat, behind the shared throttle of the
// account. It only reads; `start` and `cancel` write the local database and the job
// state, `status` reads them.
func (a *app) chatFullCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "full", Short: "Read the full data of groups and channels in the background"}
	start := a.annotate(&cobra.Command{
		Use:   "start [<chat>]",
		Short: "Start the full-data job",
		Long: "Queue reading the description, the member count and the settings of groups and channels.\n" +
			"Without a chat: every allowed group and channel without a complete read yet.\n" +
			"With --refresh every allowed group and channel is read again.\n" +
			"The command returns at once; check progress with chat full status.\n" +
			"Put -- before a negative chat id.",
		Args: cobra.MaximumNArgs(1), RunE: a.chatFullStart,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ChatFullJob](), Args: []argSpec{{Name: "chat", Required: false}}})
	start.Flags().Bool("refresh", false, "read every allowed group and channel again")
	status := a.annotate(&cobra.Command{
		Use:   "status",
		Short: "Show the full-data job",
		Long:  "Report the job of this daemon run: state, total, done, failed and the chat being read.",
		Args:  cobra.NoArgs, RunE: a.chatFullStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatFullJob]()})
	cancel := a.annotate(&cobra.Command{
		Use:   "cancel",
		Short: "Cancel the full-data job",
		Long:  "Stop the job after the chat it is reading. What was saved stays.",
		Args:  cobra.NoArgs, RunE: a.chatFullCancel,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ChatFullJob]()})
	cmd.AddCommand(start, status, cancel)
	return cmd
}

func (a *app) chatFullStart(cmd *cobra.Command, args []string) error {
	refresh, _ := cmd.Flags().GetBool("refresh")
	chat := ""
	if len(args) > 0 {
		chat = args[0]
	}
	var job rpc.ChatFullJob
	if err := a.callOne(cmd, rpc.MethodChatFullStart, rpc.ChatFullStartParams{Chat: chat, Refresh: refresh}, &job); err != nil {
		return err
	}
	return a.success(cmd, job)
}

func (a *app) chatFullStatus(cmd *cobra.Command, _ []string) error {
	var job rpc.ChatFullJob
	if err := a.callOne(cmd, rpc.MethodChatFullStatus, rpc.ChatFullStatusParams{}, &job); err != nil {
		return err
	}
	return a.success(cmd, job)
}

func (a *app) chatFullCancel(cmd *cobra.Command, _ []string) error {
	var job rpc.ChatFullJob
	if err := a.callOne(cmd, rpc.MethodChatFullCancel, rpc.ChatFullCancelParams{}, &job); err != nil {
		return err
	}
	return a.success(cmd, job)
}
