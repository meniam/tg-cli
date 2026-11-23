package cli

import (
	"time"

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
			"Without a depth flag the load goes back to the first message. --messages, --days,\n" +
			"--months and --since stop it earlier, at a page boundary; such a load never marks\n" +
			"the chat complete, and the next load goes on from the oldest saved message.\n" +
			"Use chat sync status to check progress. Put -- before a negative chat id.",
		Args: cobra.ExactArgs(1), RunE: a.chatSyncStart,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.HistorySyncJob](), Args: []argSpec{{Name: "chat", Required: true}}})
	addDepthFlags(start)
	status := a.annotate(&cobra.Command{
		Use: "status [<chat>]", Short: "Show background history load status",
		Long: "Without a chat, list current daemon jobs; JSONL prints one job per line.\n" +
			"With a chat, show one job or idle progress from the local database; JSONL is unavailable.\n" +
			"The source of a job is manual, background or catchup.\n" +
			"Put -- before a negative chat id.",
		Args: cobra.MaximumNArgs(1), RunE: a.chatSyncStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.HistorySyncJobs](), ChatResult: typeOf[rpc.HistorySyncJob](), ItemPath: "jobs", Args: []argSpec{{Name: "chat", Required: false}}})
	cancel := a.annotate(&cobra.Command{
		Use: "cancel [<chat>]", Short: "Cancel a background history load",
		Long: "Cancel a queued or running history load. Already saved messages remain.\n" +
			"With --all, cancel every queued job and the running one instead of one chat.\n" +
			"Put -- before a negative chat id.",
		Args: cobra.MaximumNArgs(1), RunE: a.chatSyncCancel,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.HistorySyncCancelAllResult](), ChatResult: typeOf[rpc.HistorySyncJob](), Args: []argSpec{{Name: "chat", Required: false}}})
	cancel.Flags().Bool("all", false, "cancel every queued and the running history load")
	queue := a.annotate(&cobra.Command{
		Use: "queue [<chat>...]", Short: "Queue the history load of several chats",
		Long: "Queue a load for each chat. Give the chats as arguments (up to 100), or pick them from\n" +
			"the dialog list, newest first, with the filters, not both. The daemon loads one chat at\n" +
			"a time; the rest wait in its queue. A chat that is loaded already, or has a job, is\n" +
			"skipped; with named chats the answer lists it with a skip action. Closed chats are\n" +
			"never picked, and a closed or unknown named chat fails the whole call before anything\n" +
			"is queued. --active-days keeps the chats with a message newer than N days. --unsynced\n" +
			"keeps only the chats that were never loaded, not the partly loaded ones. The depth\n" +
			"flags work as in chat sync start. --dry-run lists the chats and starts nothing.\n" +
			"Put -- before a negative chat id.",
		Args: cobra.ArbitraryArgs, RunE: a.chatSyncQueue,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.HistorySyncQueueResult](), ItemPath: "chats", Args: []argSpec{{Name: "chat", Variadic: true}}})
	queue.Flags().String("type", "", "chat type: user, bot, group, supergroup or channel")
	queue.Flags().Int("active-days", 0, "only chats with a message in the last N days")
	queue.Flags().Bool("unsynced", false, "only chats that were never loaded")
	queue.Flags().Int("max-chats", 0, "most chats to queue, 1 to 500 (default 50)")
	queue.Flags().Bool("dry-run", false, "list the chats and queue nothing")
	addDepthFlags(queue)
	catchup := a.annotate(&cobra.Command{
		Use: "catchup", Short: "Load messages missed during daemon downtime",
		Long: "Queue catchup for chats with messages missed during daemon downtime.\n--dry-run lists the chats and queues nothing.",
		Args: cobra.NoArgs, RunE: a.chatSyncCatchup,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.HistorySyncCatchupResult](), ItemPath: "chats"})
	catchup.Flags().Bool("dry-run", false, "list the chats and queue nothing")
	cmd.AddCommand(start, status, cancel, queue, catchup)
	return cmd
}

func (a *app) chatSyncCatchup(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	var result rpc.HistorySyncCatchupResult
	if err := a.callOne(cmd, rpc.MethodHistorySyncCatchup, rpc.HistorySyncCatchupParams{DryRun: dry}, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Chats, "")
}

// addDepthFlags adds the flags that stop a history load before the first message.
func addDepthFlags(cmd *cobra.Command) {
	cmd.Flags().Int("messages", 0, "stop once the chat has this many saved messages, 1 to 100000")
	cmd.Flags().Int("days", 0, "stop once the oldest saved message is older than N days")
	cmd.Flags().Int("months", 0, "stop once the oldest saved message is older than N calendar months")
	cmd.Flags().String("since", "", "stop once the oldest saved message is this old or older (RFC 3339 or YYYY-MM-DD)")
}

// depthFromFlags reads the depth flags into the RPC parameters. The flags
// exclude each other; --days and --months become an absolute time from now.
func depthFromFlags(cmd *cobra.Command, now time.Time) (messages int, since string, err error) {
	set := 0
	for _, name := range []string{"messages", "days", "months", "since"} {
		if cmd.Flags().Changed(name) {
			set++
		}
	}
	if set > 1 {
		return 0, "", fail(exitcode.Usage, "use only one of --messages, --days, --months and --since")
	}
	if cmd.Flags().Changed("messages") {
		messages, _ = cmd.Flags().GetInt("messages")
		if messages < 1 || messages > 100000 {
			return 0, "", fail(exitcode.Usage, "--messages must be between 1 and 100000")
		}
	}
	if cmd.Flags().Changed("days") {
		days, _ := cmd.Flags().GetInt("days")
		if days < 1 || days > 36500 {
			return 0, "", fail(exitcode.Usage, "--days must be between 1 and 36500")
		}
		since = now.AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	}
	if cmd.Flags().Changed("months") {
		months, _ := cmd.Flags().GetInt("months")
		if months < 1 || months > 1200 {
			return 0, "", fail(exitcode.Usage, "--months must be between 1 and 1200")
		}
		since = now.AddDate(0, -months, 0).UTC().Format(time.RFC3339)
	}
	if cmd.Flags().Changed("since") {
		text, _ := cmd.Flags().GetString("since")
		t, err := time.Parse(time.RFC3339, text)
		if err != nil {
			day, dayErr := time.ParseInLocation("2006-01-02", text, now.Location())
			if dayErr != nil {
				return 0, "", fail(exitcode.Usage, "--since %q is neither RFC 3339 nor YYYY-MM-DD", text)
			}
			t = day
		}
		if t.After(now) {
			return 0, "", fail(exitcode.Usage, "--since must not be in the future")
		}
		since = t.UTC().Format(time.RFC3339)
	}
	return messages, since, nil
}

func (a *app) chatSyncStart(cmd *cobra.Command, args []string) error {
	messages, since, err := depthFromFlags(cmd, time.Now())
	if err != nil {
		return err
	}
	var job rpc.HistorySyncJob
	if err := a.callOne(cmd, rpc.MethodHistorySyncStart, rpc.HistorySyncParams{Chat: args[0], Messages: messages, Since: since}, &job); err != nil {
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
	all, _ := cmd.Flags().GetBool("all")
	switch {
	case all && len(args) > 0:
		return fail(exitcode.Usage, "use either a chat or --all, not both")
	case !all && len(args) == 0:
		return fail(exitcode.Usage, "pass a chat or --all")
	case all:
		var result rpc.HistorySyncCancelAllResult
		if err := a.callOne(cmd, rpc.MethodHistorySyncCancelAll, struct{}{}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}
	var job rpc.HistorySyncJob
	if err := a.callOne(cmd, rpc.MethodHistorySyncCancel, rpc.HistorySyncParams{Chat: args[0]}, &job); err != nil {
		return err
	}
	return a.success(cmd, job)
}

func (a *app) chatSyncQueue(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	messages, since, err := depthFromFlags(cmd, time.Now())
	if err != nil {
		return err
	}
	var p rpc.HistorySyncQueueParams
	p.Type, _ = cmd.Flags().GetString("type")
	p.ActiveDays, _ = cmd.Flags().GetInt("active-days")
	p.Unsynced, _ = cmd.Flags().GetBool("unsynced")
	p.MaxChats, _ = cmd.Flags().GetInt("max-chats")
	p.DryRun, _ = cmd.Flags().GetBool("dry-run")
	p.Messages, p.Since = messages, since
	p.Chats = args
	if len(args) > 0 {
		if len(args) > 100 {
			return fail(exitcode.Usage, "name at most 100 chats")
		}
		for _, name := range []string{"type", "active-days", "unsynced", "max-chats"} {
			if cmd.Flags().Changed(name) {
				return fail(exitcode.Usage, "name chats or filter the dialog list, not both: --%s cannot go with chat arguments", name)
			}
		}
	}
	if cmd.Flags().Changed("active-days") && (p.ActiveDays < 1 || p.ActiveDays > 36500) {
		return fail(exitcode.Usage, "--active-days must be between 1 and 36500")
	}
	if cmd.Flags().Changed("max-chats") && (p.MaxChats < 1 || p.MaxChats > 500) {
		return fail(exitcode.Usage, "--max-chats must be between 1 and 500")
	}
	var result rpc.HistorySyncQueueResult
	if err := a.callOne(cmd, rpc.MethodHistorySyncQueue, p, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Chats, "")
}
