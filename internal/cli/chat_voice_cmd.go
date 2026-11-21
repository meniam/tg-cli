package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

// chatVoiceCommand is the `chat voice` group: the marks that tell the Daemon
// which chats besides the personal ones it transcribes. A mark lives in the local
// database; none of these commands calls Telegram.
func (a *app) chatVoiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "voice",
		Short: "Mark chats whose voice messages the Daemon transcribes",
		Long: "Personal chats need no mark. A marked chat gets its new and synced voice messages and\n" +
			"video notes transcribed, and its videos too when the mark has --video. The marks\n" +
			"take effect only with voice_transcription.enabled = true.",
	}
	on := a.annotate(&cobra.Command{
		Use:   "on <chat>",
		Short: "Mark a chat for transcription",
		Long: "Mark a chat the local database knows (a numeric id or a @username). Put -- before a\n" +
			"negative chat id. Marking a marked chat again changes only its --video flag.\n" +
			"--backfill also queues the voice messages and video notes already saved for the chat\n" +
			"that have no text yet (and the videos with --video) at the lowest priority, and\n" +
			"reports how many it queued. With the setting off the mark is stored, but --backfill\n" +
			"fails with code 2.",
		Args: cobra.ExactArgs(1), RunE: a.chatVoiceOn,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ChatVoiceOnResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	on.Flags().Bool("video", false, "transcribe the videos of the chat too")
	on.Flags().Bool("backfill", false, "queue the messages already saved for the chat")
	off := a.annotate(&cobra.Command{
		Use:   "off <chat>",
		Short: "Remove the mark of a chat",
		Long: "Remove the mark. Jobs already in the queue stay; `voice cancel` skips them. Put --\n" +
			"before a negative chat id.",
		Args: cobra.ExactArgs(1), RunE: a.chatVoiceOff,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ChatVoiceOffResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	list := a.annotate(&cobra.Command{
		Use:   "list",
		Short: "List the marked chats (read only)",
		Long:  "List the marked chats that access.json leaves open, by chat id, with the video flag.",
		Args:  cobra.NoArgs, RunE: a.chatVoiceList,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ChatVoiceListResult](), ItemPath: "chats"})
	cmd.AddCommand(on, off, list)
	return cmd
}

func (a *app) chatVoiceOn(cmd *cobra.Command, args []string) error {
	video, _ := cmd.Flags().GetBool("video")
	backfill, _ := cmd.Flags().GetBool("backfill")
	var res rpc.ChatVoiceOnResult
	if err := a.callOne(cmd, rpc.MethodChatVoiceOn, rpc.ChatVoiceOnParams{Chat: args[0], Video: video, Backfill: backfill}, &res); err != nil {
		return err
	}
	if !res.Enabled {
		a.warn(cmd, "voice transcription is off: set voice_transcription.enabled = true and restart the Daemon for the mark to take effect")
	}
	return a.success(cmd, res)
}

func (a *app) chatVoiceOff(cmd *cobra.Command, args []string) error {
	var res rpc.ChatVoiceOffResult
	if err := a.callOne(cmd, rpc.MethodChatVoiceOff, rpc.ChatVoiceOffParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) chatVoiceList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var res rpc.ChatVoiceListResult
	if err := a.callOne(cmd, rpc.MethodChatVoiceList, rpc.ChatVoiceListParams{}, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Chats, "")
}
