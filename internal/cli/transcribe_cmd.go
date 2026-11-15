package cli

import (
	"time"

	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) messageTranscribeCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "transcribe <chat> <id> | transcribe <message-link>", Short: "Transcribe a voice message or video note", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		chat, id, err := messageTarget(args)
		if err != nil {
			return err
		}
		wait, _ := cmd.Flags().GetDuration("wait")
		if wait < 0 || wait > 90*time.Second {
			return fail(exitcode.Usage, "wait must be between 0 and 90 seconds")
		}
		var result rpc.Transcription
		if err := a.callOne(cmd, rpc.MethodMessageTranscribe, rpc.TranscribeParams{Chat: chat, ID: id, WaitMS: wait.Milliseconds()}, &result); err != nil {
			return err
		}
		if result.Pending {
			a.warn(cmd, "transcription is pending; repeat the command later")
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Transcription](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Required: false}}})
	cmd.Flags().Duration("wait", 30*time.Second, "wait for transcription, up to 90s")
	return cmd
}
