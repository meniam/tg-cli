package cli

import (
	"time"

	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) messageTranscribeCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use:   "transcribe <chat> <id> | transcribe <message-link>",
		Short: "Transcribe a voice message, video note or video with the local engine",
		Long: "Put the message at the front of the local voice queue and wait for the text. The\n" +
			"local speech engine (tg voice install) writes it; Telegram is asked only for the\n" +
			"file, never to transcribe. A stored text is returned at once (cached). When the\n" +
			"job is not done within --wait the answer is pending: true and the job goes on;\n" +
			"ask again later. --telegram asks Telegram itself instead (needs Premium) and\n" +
			"replaces a local text; it is the only path that calls messages.transcribeAudio.",
		Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
			chat, id, err := messageTarget(args)
			if err != nil {
				return err
			}
			wait, _ := cmd.Flags().GetDuration("wait")
			viaTelegram, _ := cmd.Flags().GetBool("telegram")
			maxWait := time.Duration(rpc.MaxTranscribeWaitMS) * time.Millisecond
			if viaTelegram {
				maxWait = time.Duration(rpc.MaxTelegramTranscribeWaitMS) * time.Millisecond
			}
			if wait < 0 || wait > maxWait {
				return fail(exitcode.Usage, "wait must be between 0 and %d seconds", int(maxWait/time.Second))
			}
			var result rpc.Transcription
			if err := a.callOne(cmd, rpc.MethodMessageTranscribe, rpc.TranscribeParams{Chat: chat, ID: id, WaitMS: wait.Milliseconds(), Telegram: viaTelegram}, &result); err != nil {
				return err
			}
			if result.Pending {
				a.warn(cmd, "transcription is pending; repeat the command later")
			}
			return a.success(cmd, result)
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Transcription](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Required: false}}})
	cmd.Flags().Duration("wait", 60*time.Second, "wait for the text, up to 600s (up to 90s with --telegram)")
	cmd.Flags().Bool("telegram", false, "ask Telegram to transcribe instead of the local engine (needs Premium)")
	return cmd
}
