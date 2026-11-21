package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

// statusCommand is `tg status`: one text dashboard for a person. The daemon
// collects every number in one answer (the dashboard method); the command only
// prints it, so it is not a second source of truth next to `daemon status`.
func (a *app) statusCommand() *cobra.Command {
	return a.annotate(&cobra.Command{
		Use:   "status",
		Short: "Show a text dashboard of the daemon, the account, the database and the background history load (read only)",
		Long: "Print one readable block per topic: account, connection, access lists, database, history completeness, the background load, the semantic index, the voice queue, jobs, throttle and events.\n" +
			"The output is text for a person and has no JSON form: --format json, jsonl and toon are refused with exit code 2, --human is accepted and changes nothing.\n" +
			"The data comes from the daemon in one request that only reads, so the command works while the login is lost and makes no Telegram request. For the same facts as JSON use `daemon status`.\n" +
			"Without a running daemon the command prints a hint on stderr and exits with code 3.",
		Args: cobra.NoArgs,
		RunE: a.status,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Dashboard](), Formats: []output.Format{output.FormatHuman}})
}

func (a *app) status(cmd *cobra.Command, _ []string) error {
	if cmd.Flags().Changed("format") {
		format, _ := cmd.Flags().GetString("format")
		if format != string(output.FormatHuman) {
			return fail(exitcode.Usage, "tg status prints text for a person and has no --format %s form: use `tg daemon status` for JSON", format)
		}
	}
	var dash rpc.Dashboard
	if err := a.callOne(cmd, rpc.MethodDashboard, rpc.DashboardParams{}, &dash); err != nil {
		var ce *cmdError
		if errors.As(err, &ce) && ce.exit == exitcode.DaemonNotRunning {
			return a.statusDaemonNotRunning(cmd, ce)
		}
		return err
	}
	_, err := fmt.Fprint(a.deps.Stdout, renderDashboard(dash, time.Now()))
	return err
}

// statusDaemonNotRunning prints the plain hint on stderr, not an error envelope:
// the command has no machine form, and the exit code 3 is the signal.
func (a *app) statusDaemonNotRunning(cmd *cobra.Command, ce *cmdError) error {
	hint := "tg daemon start"
	if profile, err := cmd.Flags().GetString("profile"); err == nil && cmd.Flags().Changed("profile") && profile != daemon.DefaultProfile {
		hint += " --profile " + profile
	}
	fmt.Fprintf(a.deps.Stderr, "daemon is not running\nstart it with %s\n", hint)
	return &cmdError{exit: ce.exit, msg: ce.msg, printed: true}
}

const dashboardLabelWidth = 13

// renderDashboard is the whole text of `tg status`. It depends only on the
// answer of the daemon and on now: the location of now selects the time zone of
// every clock time, and nothing here reads the machine clock.
func renderDashboard(d rpc.Dashboard, now time.Time) string {
	var out strings.Builder
	out.WriteString(dashboardHeader(d, now))
	out.WriteString("\n\n")
	row := func(label string, lines ...string) {
		for i, line := range lines {
			prefix := strings.Repeat(" ", dashboardLabelWidth)
			if i == 0 {
				prefix = fmt.Sprintf("%-*s", dashboardLabelWidth, label)
			}
			out.WriteString(prefix + line + "\n")
		}
	}
	row("Account", dashboardAccount(d.Account))
	row("Connection", dashboardConnection(d.Connection))
	row("Access", dashboardAccess(d.Access))
	row("Database", dashboardDatabase(d.Database))
	row("History", dashboardHistory(d.History)...)
	row("Background", dashboardBackground(d.Background, now)...)
	row("Semantic", dashboardSemantic(d.SemanticSearch, now))
	row("Voice", dashboardVoice(d.Voice, now))
	row("Jobs", dashboardJobs(d.Jobs))
	row("Throttle", dashboardThrottle(d.Throttle))
	row("Events", fmt.Sprintf("%d in the last hour, %d stored", d.Events.LastHour, d.Events.Total))
	return out.String()
}

func dashboardHeader(d rpc.Dashboard, now time.Time) string {
	daemonPart := fmt.Sprintf("daemon pid %d", d.PID)
	if d.StartedAt > 0 {
		if up := now.Sub(time.Unix(d.StartedAt, 0)); up >= 0 {
			daemonPart += ", up " + shortDuration(up)
		}
	}
	return fmt.Sprintf("tg status  profile %s  %s", plainText(d.Profile), daemonPart)
}

// shortDuration keeps the two largest units: 3d 4h, 4h 12m, 12m 5s, 42s.
func shortDuration(d time.Duration) string {
	seconds := int64(d / time.Second)
	days, hours, minutes := seconds/86400, seconds%86400/3600, seconds%3600/60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func dashboardAccount(a rpc.DashboardAccount) string {
	if !a.Authorized || a.User == nil {
		return "not signed in"
	}
	name := ""
	switch {
	case a.User.Username != "":
		name = "@" + plainText(a.User.Username)
	case a.User.FirstName != "" || a.User.LastName != "":
		name = plainText(strings.TrimSpace(a.User.FirstName + " " + a.User.LastName))
	}
	if name == "" {
		return fmt.Sprintf("id %d, signed in", a.User.ID)
	}
	return fmt.Sprintf("%s (id %d), signed in", name, a.User.ID)
}

func dashboardConnection(c rpc.DashboardConnection) string {
	if c.Connection == "" {
		return "unavailable"
	}
	parts := []string{plainText(c.Connection)}
	switch {
	case c.Transport != "" && c.ActiveTransport != "" && c.ActiveTransport != c.Transport:
		parts = append(parts, fmt.Sprintf("%s (using %s)", plainText(c.Transport), plainText(c.ActiveTransport)))
	case c.Transport != "":
		parts = append(parts, plainText(c.Transport))
	}
	if c.DC > 0 {
		parts = append(parts, fmt.Sprintf("dc %d", c.DC))
	}
	parts = append(parts, fmt.Sprintf("reconnects %d", c.Reconnects))
	return strings.Join(parts, ", ")
}

func dashboardAccess(a rpc.AccessStatus) string {
	var lists string
	switch {
	case a.Closed:
		lists = "access file unreadable, every chat closed"
	case a.Enabled:
		lists = fmt.Sprintf("lists on (allow %d, block %d)", a.Allow, a.Block)
	default:
		lists = "lists off"
	}
	writes := "writes off"
	if a.Write > 0 {
		writes = "writes allowed in " + count(a.Write, "chat")
	}
	return lists + ", " + writes
}

func dashboardDatabase(db rpc.DashboardDatabase) string {
	return fmt.Sprintf("%.1f MB, schema %d, %s, %s", float64(db.SizeBytes)/1e6, db.SchemaVersion, count(int(db.Chats), "chat"), count(int(db.Messages), "message"))
}

func dashboardHistory(h rpc.DashboardHistory) []string {
	return []string{
		fmt.Sprintf("%d of %d chats loaded to the first message", h.Complete, h.Total),
		fmt.Sprintf("people %d of %d | in folders %d of %d | others %d of %d",
			h.People.Complete, h.People.Total, h.Folders.Complete, h.Folders.Total, h.Others.Complete, h.Others.Total),
	}
}

func dashboardBackground(b rpc.BackgroundSyncStatus, now time.Time) []string {
	if !b.Enabled || b.State == rpc.BackgroundSyncDisabled {
		return []string{"off (enable with tg settings set background_sync.enabled true; restart required)"}
	}
	var state string
	switch b.State {
	case rpc.BackgroundSyncWaitingWindow:
		state = "waiting for the window to open"
	case rpc.BackgroundSyncWaitingInterval:
		state = "waiting for the next run"
	case rpc.BackgroundSyncRunning:
		state = "running"
		if b.CurrentChatID != nil {
			state += fmt.Sprintf(" (chat %d)", *b.CurrentChatID)
		}
	default:
		state = plainText(strings.ReplaceAll(b.State, "_", " "))
	}
	lines := []string{fmt.Sprintf("on, window %s-%s, %s", plainText(b.Window.Start), plainText(b.Window.End), state)}
	var runs []string
	if b.NextRunAt != nil {
		runs = append(runs, "next run "+dashboardClock(*b.NextRunAt, now))
	}
	switch {
	case b.LastRunAt != nil && b.LastResult != "":
		// A skipped run picked no chat, so last_chat_id may still hold an earlier one.
		result := plainText(b.LastResult)
		if b.LastChatID != nil && b.LastResult != rpc.BackgroundSyncSkipped {
			result = fmt.Sprintf("chat %d, %s", *b.LastChatID, result)
		}
		runs = append(runs, fmt.Sprintf("last run %s (%s)", dashboardClock(*b.LastRunAt, now), result))
	case b.LastRunAt != nil:
		runs = append(runs, "last run "+dashboardClock(*b.LastRunAt, now))
	case b.State != rpc.BackgroundSyncRunning:
		runs = append(runs, "no run yet")
	}
	if len(runs) > 0 {
		lines = append(lines, strings.Join(runs, ", "))
	}
	return lines
}

// dashboardSemantic is the one line of the semantic indexer. The counters are
// printed only while the index is usable: after a failure they describe an
// older run and would mislead.
func dashboardSemantic(s rpc.SemanticIndexStatus, now time.Time) string {
	if !s.Enabled || s.State == rpc.SemanticIndexDisabled {
		return "off (enable with tg settings set semantic_search.enabled true; restart required)"
	}
	parts := []string{"on"}
	if s.Model != "" {
		parts = append(parts, plainText(s.Model))
	}
	progress := fmt.Sprintf("%d of %d fragments indexed", s.Indexed, s.Total)
	switch s.State {
	case rpc.SemanticIndexIndexing:
		parts = append(parts, progress, "indexing")
	case rpc.SemanticIndexIdle:
		parts = append(parts, progress)
		last := "no run yet"
		if s.FinishedAt != nil {
			last = "last run " + dashboardClock(*s.FinishedAt, now)
		}
		if s.Reason != "" {
			last += " (" + plainText(s.Reason) + ")"
		}
		parts = append(parts, last)
	default:
		state := plainText(strings.ReplaceAll(s.State, "_", " "))
		if s.Reason != "" {
			state += " (" + plainText(s.Reason) + ")"
		}
		parts = append(parts, state)
	}
	return strings.Join(parts, ", ")
}

// dashboardVoice is the one line of the local transcription queue: off, or on
// with the queue, today's work and the downloads, and the reason the queue waits
// or the last failure. Every word is a machine word or a number.
func dashboardVoice(v rpc.VoiceStatus, now time.Time) string {
	if !v.Enabled {
		return "off (enable with tg settings set voice_transcription.enabled true; restart required)"
	}
	if !v.Installed {
		line := "on, engine not installed"
		if len(v.Missing) > 0 {
			missing := make([]string, 0, len(v.Missing))
			for _, m := range v.Missing {
				missing = append(missing, plainText(m))
			}
			line += " (missing " + strings.Join(missing, ", ") + ")"
		}
		return line
	}
	parts := []string{"on", fmt.Sprintf("%d queued", v.Queue.Queued)}
	if v.Queue.Running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", v.Queue.Running))
	}
	parts = append(parts, fmt.Sprintf("%d done today", v.DoneToday), fmt.Sprintf("%d MB", v.BytesToday>>20))
	if v.Paused != "" {
		pause := "paused: " + plainText(v.Paused)
		if v.PausedUntil != nil {
			pause += " until " + dashboardClock(*v.PausedUntil, now)
		}
		parts = append(parts, pause)
	}
	if v.LastFailure != "" {
		parts = append(parts, "last failure: "+plainText(v.LastFailure))
	}
	return strings.Join(parts, ", ")
}

// dashboardClock prints HH:MM for a moment of the day of now and the date with
// the time for any other day, in the zone of now.
func dashboardClock(unix int64, now time.Time) string {
	at := time.Unix(unix, 0).In(now.Location())
	if at.Format(time.DateOnly) == now.Format(time.DateOnly) {
		return at.Format("15:04")
	}
	return at.Format("2006-01-02 15:04")
}

func dashboardJobs(j rpc.DashboardJobs) string {
	sync := "none"
	if len(j.Sync) > 0 {
		items := make([]string, 0, len(j.Sync))
		for _, job := range j.Sync {
			items = append(items, fmt.Sprintf("chat %d %s (%s, %s, %d loaded)", job.ChatID, plainText(strings.ReplaceAll(job.State, "_", " ")), plainText(job.Source), count(job.Pages, "page"), job.Loaded))
		}
		sync = strings.Join(items, "; ")
	}
	full := "none"
	switch j.Full.State {
	case rpc.ChatFullQueued, rpc.ChatFullRunning, rpc.ChatFullWaitingFlood:
		full = fmt.Sprintf("%s, %d of %s", plainText(strings.ReplaceAll(j.Full.State, "_", " ")), j.Full.Done, count(j.Full.Total, "chat"))
	}
	return "chat sync: " + sync + " | chat full: " + full
}

func dashboardThrottle(t rpc.DashboardThrottle) string {
	wait := "no flood wait"
	if t.FloodWaitSeconds > 0 {
		wait = "flood wait " + shortDuration(time.Duration(t.FloodWaitSeconds)*time.Second)
	}
	return fmt.Sprintf("%s, %d queued", wait, t.Queued)
}

// count writes a number with its noun in the plural form of English: 1 chat, 2 chats.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// plainText replaces control characters with a space: a name of the account is
// text of Telegram, and an escape sequence in it must not reach the terminal.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
