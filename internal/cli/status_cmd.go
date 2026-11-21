package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

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
		Short: "Show the daemon’s status, queues and active settings",
		Long: "Show the vector index, transcription queue, history, Telegram connection, storage and active settings.\n" +
			"The output is text for a person and has no JSON form: --format json, jsonl and toon are refused with exit code 2, --human is accepted and changes nothing.\n" +
			"The data comes from the daemon in one request that only reads, so the command works while the login is lost and makes no Telegram request. For machine-readable daemon state use `daemon status`.\n" +
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

var dashboardNumbers = message.NewPrinter(language.English)

// renderDashboard formats one daemon snapshot in the local time zone of now.
func renderDashboard(d rpc.Dashboard, now time.Time) string {
	var out strings.Builder
	section := func(label, summary string) {
		fmt.Fprintf(&out, "\n%-17s%s\n", label, summary)
	}
	row := func(label string, values ...string) {
		for _, value := range values {
			fmt.Fprintf(&out, "  %-15s%s\n", label, value)
			label = ""
		}
	}
	fmt.Fprintf(&out, "TG STATUS · %s\n", plainText(d.Profile))
	connection := dashboardState(d.Connection.Connection)
	if d.Connection.Connection == "ready" {
		connection = "online"
	}
	fmt.Fprintf(&out, "Telegram %s · %s · PID %d", connection, dashboardAccount(d.Account), d.PID)
	if up := now.Sub(time.Unix(d.StartedAt, 0)); d.StartedAt > 0 && up >= 0 {
		fmt.Fprintf(&out, " · up %s", shortDuration(up))
	}
	out.WriteByte('\n')

	index := d.SemanticSearch
	coverage := dashboardHasCoverage(index, d.Settings == nil)
	section("VECTOR INDEX", dashboardSemantic(index, coverage))
	if coverage {
		label := "Coverage"
		if !dashboardIndexHealthy(index.State) {
			label = "Last measured"
		}
		row(label, dashboardNumbers.Sprintf("%d / %d fragments · %d remaining", index.Indexed, index.Total, max(index.Total-index.Indexed, 0)))
		if index.StartedAt != nil || index.FinishedAt != nil {
			label := "This run"
			if index.State != rpc.SemanticIndexIndexing {
				label = "Last run"
			}
			row(label, dashboardNumbers.Sprintf("%d embedded · %d removed", index.Embedded, index.Removed))
		}
	}
	if timing := dashboardIndexTiming(index, now); timing != "" {
		row("Timing", timing)
	}
	if d.Settings != nil && d.Settings.Semantic.Model != "" {
		cfg := d.Settings.Semantic
		row("Settings", fmt.Sprintf("%s via Ollama · batch %d · every %s", plainText(cfg.Model), cfg.Batch, dashboardDuration(cfg.Interval)))
		similarity := "off"
		if cfg.MinSimilarity > 0 {
			similarity = fmt.Sprintf("%.2f", cfg.MinSimilarity)
		}
		row("", "Minimum similarity "+similarity)
	} else if index.Model != "" {
		row("Model", plainText(index.Model))
	}

	voice := d.Voice
	engine := "GigaAM"
	if d.Settings != nil && d.Settings.Voice.Engine == "gigaam-v3" {
		engine = "GigaAM v3"
	} else if d.Settings != nil && d.Settings.Voice.Engine != "" {
		engine = plainText(d.Settings.Voice.Engine)
	}
	section("TRANSCRIPTION", dashboardVoice(voice, engine, now))
	if voice.State != rpc.VoiceStateUnavailable && (voice.Enabled || voice.DailyFiles > 0) {
		q := voice.Queue
		row("Queue", dashboardNumbers.Sprintf("%d waiting · %d running", q.Queued, q.Running))
		row("Outcomes", dashboardNumbers.Sprintf("%d done · %d failed · %d skipped", q.Done, q.Failed, q.Skipped))
		if voice.DailyFiles > 0 && voice.DailyBytes > 0 {
			row("Today", dashboardNumbers.Sprintf("%d / %d files · %d / %d MiB downloaded",
				voice.FilesToday, voice.DailyFiles, voice.BytesToday>>20, voice.DailyBytes>>20))
		}
	}
	if current := voice.Current; current != nil {
		parts := []string{dashboardState(current.Kind)}
		if current.Stage != "" {
			parts = append(parts, dashboardState(current.Stage))
		}
		if elapsed := now.Sub(time.Unix(current.StartedAt, 0)); current.StartedAt > 0 && elapsed >= 0 {
			parts = append(parts, shortDuration(elapsed)+" elapsed")
		}
		parts = append(parts, fmt.Sprintf("chat %d · message %d", current.ChatID, current.MessageID))
		row("Current", strings.Join(parts, " · "))
	}
	if voice.LastFailure != "" {
		row("Last failure", dashboardState(voice.LastFailure))
	}
	if d.Settings != nil {
		cfg := d.Settings.Voice
		row("Limits", fmt.Sprintf("Voice %d MiB · video note %d MiB · video %d MiB / %d min",
			cfg.MaxVoiceMiB, cfg.MaxVideoNoteMiB, cfg.MaxVideoMiB, cfg.MaxVideoMinutes))
		row("Settings", fmt.Sprintf("%d threads · %s between downloads", cfg.Threads, dashboardDuration(cfg.Pause)))
	}

	h := d.History
	section("HISTORY", dashboardNumbers.Sprintf("%d / %d chats loaded to the first message", h.Complete, h.Total))
	row("Coverage", dashboardNumbers.Sprintf("People %d/%d · folders %d/%d · other %d/%d",
		h.People.Complete, h.People.Total, h.Folders.Complete, h.Folders.Total, h.Others.Complete, h.Others.Total))
	row("Schedule", dashboardBackground(d.Background, now))
	if d.Settings != nil {
		cfg := d.Settings.Background
		row("Settings", fmt.Sprintf("%s–%s between runs · %s–%s between pages",
			dashboardDuration(cfg.IntervalMin), dashboardDuration(cfg.IntervalMax),
			dashboardDuration(cfg.PagePauseMin), dashboardDuration(cfg.PagePauseMax)))
	}
	row("Jobs", dashboardJobs(d.Jobs)...)

	section("TELEGRAM", dashboardConnection(d.Connection))
	row("Requests", dashboardThrottle(d.Throttle))
	if d.Settings != nil {
		row("Settings", dashboardTelegramSettings(*d.Settings)...)
	}
	row("Access", dashboardAccess(d.Access))

	section("STORAGE", dashboardNumbers.Sprintf("%.1f MB · schema %d", float64(d.Database.SizeBytes)/1e6, d.Database.SchemaVersion))
	row("Local data", dashboardCount(d.Database.Chats, "chat")+" · "+dashboardCount(d.Database.Messages, "message"))
	row("Events", dashboardNumbers.Sprintf("%d in the last hour · %d stored", d.Events.LastHour, d.Events.Total))
	return out.String()
}

func dashboardDuration(value string) string {
	if minutes, ok := strings.CutSuffix(value, "m0s"); ok {
		value = minutes + "m"
	}
	if hours, ok := strings.CutSuffix(value, "h0m"); ok {
		value = hours + "h"
	}
	return plainText(value)
}

func dashboardState(value string) string {
	if value == "" {
		return "unavailable"
	}
	return plainText(strings.ReplaceAll(value, "_", " "))
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
		if a.AuthLost != "" {
			return "not signed in (" + dashboardState(a.AuthLost) + ")"
		}
		return "not signed in"
	}
	name := strings.TrimSpace(a.User.FirstName + " " + a.User.LastName)
	if a.User.Username != "" {
		name = "@" + a.User.Username
	}
	if name == "" {
		return fmt.Sprintf("signed in (id %d)", a.User.ID)
	}
	return "signed in as " + plainText(name)
}

func dashboardConnection(c rpc.DashboardConnection) string {
	if c.Transport == "" {
		return dashboardState(c.Connection)
	}
	parts := []string{strings.ToUpper(plainText(c.Transport))}
	if c.ActiveTransport != "" && c.ActiveTransport != c.Transport {
		parts[0] += " using " + strings.ToUpper(plainText(c.ActiveTransport))
	}
	if c.DC > 0 {
		parts = append(parts, fmt.Sprintf("DC %d", c.DC))
	}
	parts = append(parts, dashboardCount(int64(c.Reconnects), "reconnect"))
	return strings.Join(parts, " · ")
}

func dashboardTelegramSettings(s rpc.DashboardSettings) []string {
	client := "Own application"
	if s.ClientPreset != "" {
		client = plainText(s.ClientPreset) + " preset"
	}
	parts := []string{client}
	if s.Throttle != nil {
		wait := "flood auto-wait off"
		if s.Throttle.FloodAutoWait != "0s" {
			wait = "flood auto-wait up to " + dashboardDuration(s.Throttle.FloodAutoWait)
		}
		parts = append(parts, fmt.Sprintf("throttle %g%%", s.Throttle.Percent), wait)
	}
	proxy := "Proxy off"
	if s.Proxy != "" {
		proxy = "Proxy " + plainText(s.Proxy)
	}
	idle := "idle timeout off"
	if s.IdleTimeout != "0s" {
		idle = "idle timeout " + dashboardDuration(s.IdleTimeout)
	}
	lines := []string{strings.Join(parts, " · "), proxy + " · " + idle}
	if s.Device != "" {
		lines = append(lines, "Device "+plainText(s.Device))
	}
	return lines
}

func dashboardAccess(a rpc.AccessStatus) string {
	if a.Closed {
		return "Unavailable · all chats closed"
	}
	filter := "Read filter off"
	if a.Enabled {
		filter = fmt.Sprintf("Read filter on (%d allowed, %d blocked)", a.Allow, a.Block)
	}
	writes := "writes off"
	if a.Write > 0 {
		writes = "writes allowed in " + dashboardCount(int64(a.Write), "chat")
	}
	return filter + " · " + writes
}

func dashboardBackground(b rpc.BackgroundSyncStatus, now time.Time) string {
	window := plainText(b.Window.Start) + "–" + plainText(b.Window.End)
	if !b.Enabled || b.State == rpc.BackgroundSyncDisabled {
		return "Off · window " + window
	}
	parts := []string{"On", window}
	if b.State == rpc.BackgroundSyncRunning {
		running := "running"
		if b.CurrentChatID != nil {
			running += fmt.Sprintf(" in chat %d", *b.CurrentChatID)
		}
		parts = append(parts, running)
	}
	if b.NextRunAt != nil {
		next := "next run " + dashboardClock(*b.NextRunAt, now)
		if b.State == rpc.BackgroundSyncWaitingWindow {
			next += " (window closed)"
		}
		parts = append(parts, next)
	}
	if b.LastRunAt != nil {
		last := "last run " + dashboardClock(*b.LastRunAt, now)
		if b.LastResult != "" {
			result := dashboardState(b.LastResult)
			if b.LastChatID != nil && b.LastResult != rpc.BackgroundSyncSkipped {
				result = fmt.Sprintf("chat %d, %s", *b.LastChatID, result)
			}
			last += " (" + result + ")"
		}
		parts = append(parts, last)
	}
	return strings.Join(parts, " · ")
}

func dashboardHasCoverage(s rpc.SemanticIndexStatus, legacy bool) bool {
	if !s.Enabled {
		return false
	}
	if s.CoverageMeasured {
		return true
	}
	// Older daemons do not say whether their counters are from the current run.
	return legacy && dashboardIndexHealthy(s.State) &&
		(s.Total > 0 || s.FinishedAt != nil)
}

func dashboardIndexHealthy(state string) bool {
	return state == rpc.SemanticIndexIdle || state == rpc.SemanticIndexIndexing
}

func dashboardSemantic(s rpc.SemanticIndexStatus, coverage bool) string {
	if !s.Enabled || s.State == rpc.SemanticIndexDisabled {
		return "Off"
	}
	state := "On · " + dashboardState(s.State)
	switch s.State {
	case rpc.SemanticIndexIndexing:
		state = "Indexing"
	case rpc.SemanticIndexIdle:
		state = "Idle"
		if s.FinishedAt == nil {
			state = "Waiting for first run"
		}
	case rpc.SemanticIndexUnavailable:
		state = "Unavailable"
	case rpc.SemanticIndexModelMissing:
		state = "Model missing"
	case rpc.SemanticIndexError:
		state = "Error"
	}
	if coverage && dashboardIndexHealthy(s.State) && s.Total > 0 {
		state += fmt.Sprintf(" · %.1f%%", 100*float64(s.Indexed)/float64(s.Total))
	}
	if s.Reason != "" {
		state += " · " + plainText(s.Reason)
	}
	return state
}

func dashboardIndexTiming(s rpc.SemanticIndexStatus, now time.Time) string {
	var timing []string
	if s.State == rpc.SemanticIndexIndexing && s.StartedAt != nil {
		timing = append(timing, "started "+dashboardClock(*s.StartedAt, now))
	}
	if s.State != rpc.SemanticIndexIndexing && s.FinishedAt != nil {
		timing = append(timing, "last run "+dashboardClock(*s.FinishedAt, now))
	}
	if s.NextRunAt != nil {
		timing = append(timing, "next run "+dashboardClock(*s.NextRunAt, now))
	}
	return strings.Join(timing, " · ")
}

func dashboardVoice(v rpc.VoiceStatus, engine string, now time.Time) string {
	if v.State == rpc.VoiceStateUnavailable {
		return "Unavailable · queue could not be read"
	}
	if !v.Enabled && (v.State == rpc.VoiceStateDisabled || v.State == "") {
		return "Off"
	}
	if !v.Installed {
		line := "Engine unavailable"
		if len(v.Missing) > 0 {
			line += " · missing " + plainText(strings.Join(v.Missing, ", "))
		}
		return line
	}
	if v.Paused != "" {
		pause := "Paused · " + dashboardState(v.Paused)
		if v.PausedUntil != nil {
			pause += " until " + dashboardClock(*v.PausedUntil, now)
		}
		return pause
	}
	state := "Idle"
	if v.State == rpc.VoiceStateWorking {
		state = "Working"
	}
	if !v.Enabled {
		state += " · automatic transcription off"
	}
	return state + " · " + engine + " ready"
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

func dashboardJobs(j rpc.DashboardJobs) []string {
	full := "chat details idle"
	switch j.Full.State {
	case rpc.ChatFullQueued, rpc.ChatFullRunning, rpc.ChatFullWaitingFlood:
		full = dashboardNumbers.Sprintf("chat details %s (%d / %d chats)", dashboardState(j.Full.State), j.Full.Done, j.Full.Total)
	}
	if len(j.Sync) == 0 {
		return []string{"History sync idle · " + full}
	}
	lines := []string{fmt.Sprintf("History sync · %s · %s", dashboardCount(int64(len(j.Sync)), "job"), full)}
	for _, job := range j.Sync {
		lines = append(lines, dashboardNumbers.Sprintf("Chat %s · %s (%s) · %s · %d messages loaded",
			fmt.Sprint(job.ChatID), dashboardState(job.State), plainText(job.Source), dashboardCount(int64(job.Pages), "page"), job.Loaded))
	}
	return lines
}

func dashboardThrottle(t rpc.DashboardThrottle) string {
	wait := "no flood wait"
	if t.FloodWaitSeconds > 0 {
		wait = "flood wait " + shortDuration(time.Duration(t.FloodWaitSeconds)*time.Second)
	}
	return dashboardNumbers.Sprintf("%d active · %d waiting · %s", t.ActiveRequests, t.Queued, wait)
}

func dashboardCount(n int64, noun string) string {
	if n != 1 {
		noun += "s"
	}
	return dashboardNumbers.Sprintf("%d %s", n, noun)
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
