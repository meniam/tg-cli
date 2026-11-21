package cli

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/voice/gigaam"
)

// voiceInstallResult is the answer of `voice install`. Missing names the parts
// of the engine that are not in place (ffmpeg, runtime, model, vad); no path
// and no file name is reported.
type voiceInstallResult struct {
	// Installed is true when nothing is missing and the engine can run.
	Installed bool `json:"installed"`
	// Checked is true for `--check`, which downloads nothing.
	Checked bool     `json:"checked"`
	Missing []string `json:"missing"`
}

// voiceCommand is the `voice` group: the local engine and its queue. None of
// its commands asks Telegram to transcribe; the worker only downloads files.
func (a *app) voiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "voice",
		Short: "Install the local speech engine and manage the transcription queue",
		Long: "The Daemon transcribes voice messages, video notes and videos with a local speech\n" +
			"engine (GigaAM, Russian only) through one persistent queue. `install` downloads\n" +
			"the engine, `status` counts the queue, `list` shows its jobs with their reasons,\n" +
			"`cancel`, `retry` and `backfill` manage it.\n" +
			"The sources that queue jobs by themselves need [voice_transcription] enabled = true.",
	}
	install := a.annotate(&cobra.Command{
		Use:   "install",
		Short: "Download and verify the local speech engine",
		Long: "Download the sherpa-onnx runtime, the GigaAM model and the Silero VAD model into\n" +
			"<home>/models/gigaam (about 190 MB), only from the links and SHA-256 sums fixed in\n" +
			"the program; each archive is verified before it is unpacked. It runs in this process\n" +
			"and needs neither the Daemon nor Telegram. A repeat run fetches only what is missing.\n" +
			"--check downloads nothing: it reports what is missing, ffmpeg included.",
		Args: cobra.NoArgs, RunE: a.voiceInstall,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[voiceInstallResult]()})
	install.Flags().Bool("check", false, "only report what is missing, download nothing")
	status := a.annotate(&cobra.Command{
		Use:   "status",
		Short: "Show the voice queue and the engine",
		Long: "Report whether the engine is installed (and what is missing), the state of the\n" +
			"worker (disabled, idle, working, paused with the reason), the queue by state, the\n" +
			"downloads of today against the budget and the reason of the last failure. It reads\n" +
			"state in memory and the database and never calls Telegram.",
		Args: cobra.NoArgs, RunE: a.voiceStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.VoiceStatus]()})
	list := a.annotate(&cobra.Command{
		Use:   "list",
		Short: "List the jobs of the voice queue (read only)",
		Long: "List the jobs of the queue, newest enqueued first: the chat, the message id, the kind,\n" +
			"the source, the state, the reason (a machine word such as no_speech, no_audio or\n" +
			"convert), the attempts, the bytes downloaded and the times. It carries neither message\n" +
			"text nor transcriptions: read a text with `message transcribe` or `message get`. Chats\n" +
			"that access.json closes are not listed. --state, --chat and --kind narrow the list.\n" +
			"Pass next_cursor back with --cursor to continue. It reads the database and never calls\n" +
			"Telegram.",
		Args: cobra.NoArgs, RunE: a.voiceList,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.VoiceListResult](), Choices: voiceListChoices(), ItemPath: "jobs"})
	list.Flags().String("state", "", "keep jobs in this state: queued, running, done, failed or skipped")
	list.Flags().String("chat", "", "keep jobs of this chat (a numeric id or a @username the database knows)")
	list.Flags().String("kind", "", "keep jobs of this kind: voice, video_note or video")
	list.Flags().Int("limit", 50, "jobs per page, 1 to 200")
	list.Flags().String("cursor", "", "continue after a previous page, the value of next_cursor")
	cancel := a.annotate(&cobra.Command{
		Use:   "cancel [<chat> <id> | <message-link>]",
		Short: "Stop the running job and skip the queued ones",
		Long: "Without arguments stop the running job and skip every queued one. With a chat and a\n" +
			"message id (or a message link) cancel only that message's job. A skipped job can be\n" +
			"queued again with `voice retry`.",
		Args: cobra.MaximumNArgs(2), RunE: a.voiceCancel,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.VoiceCancelResult](), Args: []argSpec{{Name: "chat_or_link", Required: false}, {Name: "id", Required: false}}})
	retry := a.annotate(&cobra.Command{
		Use:   "retry <chat> <id> | retry <message-link>",
		Short: "Queue a finished or failed job again",
		Long: "Queue the job of one saved message again at the front of the queue, whatever its\n" +
			"earlier outcome. It does not wait for the text: read it with `message transcribe`.\n" +
			"It needs the engine installed.",
		Args: cobra.RangeArgs(1, 2), RunE: a.voiceRetry,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.VoiceRetryResult](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Required: false}}})
	backfill := a.annotate(&cobra.Command{
		Use:   "backfill",
		Short: "Queue the saved voice messages of the personal chats",
		Long: "Queue the saved voice messages and video notes of the personal chats that have no\n" +
			"text yet, at the lowest priority. Chats that access.json closes and your own\n" +
			"messages are left out. --dry-run counts them and queues nothing. Without\n" +
			"[voice_transcription] enabled = true the command fails (the dry run still counts).",
		Args: cobra.NoArgs, RunE: a.voiceBackfill,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.VoiceBackfillResult]()})
	backfill.Flags().Bool("dry-run", false, "count the messages and queue nothing")
	cmd.AddCommand(install, status, list, cancel, retry, backfill)
	return cmd
}

// voiceListChoices are the allowed values of the --state and --kind flags of
// `voice list`. The daemon checks them, so the lists are repeated here for the
// schema and the usage error.
func voiceListChoices() []flagChoices {
	return []flagChoices{
		{Flag: "state", Values: []string{"queued", "running", "done", "failed", "skipped"}},
		{Flag: "kind", Values: []string{"voice", "video_note", "video"}},
	}
}

// voiceInstallProgress prints the steps of an installation on stderr: each step
// once, and the bytes of a download every 32 MiB.
func (a *app) voiceInstallProgress(cmd *cobra.Command) gigaam.ProgressFunc {
	lastStep := ""
	var lastMark int64
	return func(step string, bytes int64) {
		if step != lastStep {
			lastStep, lastMark = step, 0
			a.warn(cmd, "voice install: %s", step)
			return
		}
		if mark := bytes >> 25; mark > lastMark {
			lastMark = mark
			a.warn(cmd, "voice install: %s %d MiB", step, bytes>>20)
		}
	}
}

func (a *app) voiceInstall(cmd *cobra.Command, _ []string) error {
	_, values, err := a.voiceTranscriptionSettings()
	if err != nil {
		return err
	}
	home := a.voiceHome()
	check, _ := cmd.Flags().GetBool("check")
	if !check {
		err := gigaam.Install(cmd.Context(), gigaam.Options{Paths: gigaam.NewPaths(home), Progress: a.voiceInstallProgress(cmd)})
		if err != nil {
			return voiceInstallFailure(err)
		}
	}
	missing := daemon.VoiceEngineMissing(home, values.FFmpeg)
	if missing == nil {
		missing = []string{}
	}
	if !check && len(missing) > 0 {
		a.warn(cmd, "the engine is downloaded but not ready: missing %v", missing)
	}
	return a.success(cmd, voiceInstallResult{Installed: len(missing) == 0, Checked: check, Missing: missing})
}

// voiceInstallFailure turns an installation error into a short failure. The
// error of the installer may name a path or a link; only its class is shown.
func voiceInstallFailure(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fail(exitcode.Failure, "interrupted: run `tg voice install` again to continue")
	case errors.Is(err, gigaam.ErrDownload):
		return fail(exitcode.Failure, "the download failed: check the network and run `tg voice install` again")
	case errors.Is(err, gigaam.ErrSize), errors.Is(err, gigaam.ErrChecksum):
		return fail(exitcode.Failure, "the downloaded file does not match the pinned size or SHA-256; nothing was installed")
	case errors.Is(err, gigaam.ErrUnsafeArchive):
		return fail(exitcode.Failure, "the downloaded archive was refused as unsafe; nothing was installed")
	case errors.Is(err, gigaam.ErrIncomplete):
		return fail(exitcode.Failure, "the downloaded archive misses required files; nothing was installed")
	}
	return fail(exitcode.Failure, "the installation failed")
}

func (a *app) voiceStatus(cmd *cobra.Command, _ []string) error {
	var res rpc.VoiceStatus
	if err := a.callOne(cmd, rpc.MethodVoiceStatus, rpc.VoiceStatusParams{}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) voiceList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var p rpc.VoiceListParams
	p.State, _ = cmd.Flags().GetString("state")
	p.Chat, _ = cmd.Flags().GetString("chat")
	p.Kind, _ = cmd.Flags().GetString("kind")
	p.Cursor, _ = cmd.Flags().GetString("cursor")
	for _, choice := range voiceListChoices() {
		value, _ := cmd.Flags().GetString(choice.Flag)
		if value != "" && !slices.Contains(choice.Values, value) {
			return fail(exitcode.Usage, "--%s must be %s", choice.Flag, joinChoices(choice.Values))
		}
	}
	if p.Limit, err = chatListLimit(cmd, 200); err != nil {
		return err
	}
	var res rpc.VoiceListResult
	if err := a.callOne(cmd, rpc.MethodVoiceList, p, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Jobs, res.NextCursor)
}

// joinChoices writes values as "a, b, or c" for a usage error.
func joinChoices(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}
	return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
}

func (a *app) voiceCancel(cmd *cobra.Command, args []string) error {
	p := rpc.VoiceCancelParams{}
	if len(args) > 0 {
		chat, id, err := messageTarget(args)
		if err != nil {
			return err
		}
		p = rpc.VoiceCancelParams{Chat: chat, ID: id}
	}
	var res rpc.VoiceCancelResult
	if err := a.callOne(cmd, rpc.MethodVoiceCancel, p, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) voiceRetry(cmd *cobra.Command, args []string) error {
	chat, id, err := messageTarget(args)
	if err != nil {
		return err
	}
	var res rpc.VoiceRetryResult
	if err := a.callOne(cmd, rpc.MethodVoiceRetry, rpc.VoiceRetryParams{Chat: chat, ID: id}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) voiceBackfill(cmd *cobra.Command, _ []string) error {
	dry, _ := cmd.Flags().GetBool("dry-run")
	var res rpc.VoiceBackfillResult
	if err := a.callOne(cmd, rpc.MethodVoiceBackfill, rpc.VoiceBackfillParams{DryRun: dry}, &res); err != nil {
		return err
	}
	if dry && !res.Enabled {
		a.warn(cmd, "voice transcription is off: set [voice_transcription] enabled = true to queue these")
	}
	return a.success(cmd, res)
}
