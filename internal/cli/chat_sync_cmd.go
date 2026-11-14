package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

func (a *app) chatSyncCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "sync", Short: "Load one chat's history into the local database"}
	start := a.annotate(&cobra.Command{
		Use: "start <chat>", Short: "Start or resume a background history load",
		Long: "Start loading one chat's history in the daemon. The command returns immediately.\n" +
			"Use chat sync status to check progress. Put -- before a negative chat id.",
		Args: cobra.ExactArgs(1), RunE: a.chatSyncStart,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.HistorySyncJob](), Args: []argSpec{{Name: "chat", Required: true}}})
	status := a.annotate(&cobra.Command{
		Use: "status [<chat>]", Short: "Show background history load status",
		Long: "Without a chat, list current daemon jobs; JSONL prints one job per line.\n" +
			"With a chat, show one job or idle progress from the local database; JSONL is unavailable.\n" +
			"Put -- before a negative chat id.",
		Args: cobra.MaximumNArgs(1), RunE: a.chatSyncStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.HistorySyncJobs](), ChatResult: typeOf[rpc.HistorySyncJob](), ItemPath: "jobs", Args: []argSpec{{Name: "chat", Required: false}}})
	cancel := a.annotate(&cobra.Command{
		Use: "cancel <chat>", Short: "Cancel a background history load",
		Long: "Cancel a queued or running history load. Already saved messages remain.\n" +
			"Put -- before a negative chat id.",
		Args: cobra.ExactArgs(1), RunE: a.chatSyncCancel,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.HistorySyncJob](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.AddCommand(start, status, cancel)
	return cmd
}

func (a *app) chatSyncStart(cmd *cobra.Command, args []string) error {
	var job rpc.HistorySyncJob
	if err := a.callOne(cmd, rpc.MethodHistorySyncStart, rpc.HistorySyncParams{Chat: args[0]}, &job); err != nil {
		return err
	}
	if err := a.success(cmd, job); err != nil {
		return err
	}
	a.warn(cmd, "check progress with `tg chat sync status -- %q`", args[0])
	return nil
}

func (a *app) chatSyncStatus(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		if settings.format == output.FormatJSONL {
			return fail(exitcode.Usage, "--format jsonl requires chat sync status without a chat")
		}
		var job rpc.HistorySyncJob
		if err := a.callOne(cmd, rpc.MethodHistorySyncStatus, rpc.HistorySyncStatusParams{Chat: args[0]}, &job); err != nil {
			return err
		}
		return a.success(cmd, job)
	}
	var result rpc.HistorySyncJobs
	if err := a.callOne(cmd, rpc.MethodHistorySyncStatus, rpc.HistorySyncStatusParams{}, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Jobs, "")
}

func (a *app) chatSyncCancel(cmd *cobra.Command, args []string) error {
	var job rpc.HistorySyncJob
	if err := a.callOne(cmd, rpc.MethodHistorySyncCancel, rpc.HistorySyncParams{Chat: args[0]}, &job); err != nil {
		return err
	}
	return a.success(cmd, job)
}
