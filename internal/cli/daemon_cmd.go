package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"tg-cli/internal/clientpreset"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) daemonCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Short: "Run and control the background daemon"}
	start := a.annotate(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background and wait until it answers",
		Long: "Start the daemon in the background and wait until it answers on its socket.\n" +
			"The background process has no terminal, writes its journal to\n" +
			"the profile journal and keeps running after this command returns; stop it with\n" +
			"`tg daemon stop`. With --foreground the daemon runs in this terminal instead,\n" +
			"and returns {\"stopped\":true} after it stops.\n\n" +
			"Starting a profile that already has a running daemon is not an error: the\n" +
			"command reports the daemon that is already there and starts no second one,\n" +
			"because two processes must never share one Telegram session.\n\n" +
			"Application credentials come from TG_API_ID and TG_API_HASH (your own\n" +
			"application, https://my.telegram.org) or from --client, an official client\n" +
			"preset downloaded from the opentele project. The chosen preset is remembered\n" +
			"for the profile, so later starts need no flag. Credentials are never passed on\n" +
			"the command line of the background process, where `ps` would show them.\n\n" +
			"--device sets the device name in the list of Telegram devices. The default is\n" +
			"\"MacBook Pro M1 BRN\"; a name given by --device is remembered for the profile.\n\n" +
			"--idle-timeout stops the daemon after this long without a request. The default\n" +
			"is 0, no idle stop.",
		Args: cobra.NoArgs,
		RunE: a.daemonStart,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[backgroundStartedResult](), ResultVariants: []resultVariant{{Flag: "foreground", Result: typeOf[daemonStartedResult]()}}, Choices: []flagChoices{{Flag: "client", Values: []string{"desktop", "android", "ios", "macos", "web-z", "web-k"}}}})
	start.Flags().String("transport", "", "Telegram transport: tcp, wss or auto (remembered for the profile)")
	start.Flags().String("proxy", "", "SOCKS5, HTTP or Telegram proxy URL, or none (remembered for the profile)")
	start.Flags().String("client", "", "official client preset to use instead of your own application, for example desktop")
	start.Flags().String("device", "", "device name Telegram shows for this session, remembered for the profile (default MacBook Pro M1 BRN)")
	start.Flags().Bool("foreground", false, "run the daemon in this terminal instead of the background")
	start.Flags().String("journal", "", "internal journal path for a detached daemon")
	_ = start.Flags().MarkHidden("journal")
	start.Flags().Duration("idle-timeout", 0, "stop the daemon after this long without a request, like 30m; 0 means never")
	logs := a.annotate(&cobra.Command{
		Use: "logs", Short: "Read the profile journal without a running daemon",
		Long: "Read up to 10000 lines of the current journal. --tail selects the last N lines; --follow prints new lines as they arrive, including after rotation. In follow mode each update is a separate result in the selected format.",
		Args: cobra.NoArgs, RunE: a.daemonLogs,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[daemonLogsResult](), ItemPath: "lines"})
	logs.Flags().Int("tail", 0, "last N lines, 1 to 10000; 0 means up to 10000 lines")
	logs.Flags().Bool("follow", false, "print new lines until interrupted, including after rotation")
	cmd.AddCommand(start, logs, a.annotate(&cobra.Command{
		Use:   "status",
		Short: "Show the state of the daemon and of the account",
		Args:  cobra.NoArgs,
		RunE:  a.daemonStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.StatusResult]()}), a.annotate(&cobra.Command{
		Use:   "stop",
		Short: "Ask the daemon to stop",
		Args:  cobra.NoArgs,
		RunE:  a.daemonStop,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ShutdownResult]()}))
	return cmd
}

var presetName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// credentials finds the Telegram application credentials. The sources, in the
// order they are tried:
//
//  1. TG_API_ID and TG_API_HASH: an application of the user.
//  2. --client <preset>: an official client preset, downloaded from opentele.
//  3. The preset saved for the profile by an earlier `daemon start --client`.
//
// Giving both (1) and (2) is an error: it is unclear which one is meant.
func (a *app) credentials(cmd *cobra.Command, paths daemon.Paths) (daemon.TelegramConfig, error) {
	cfg := daemon.TelegramConfig{SessionPath: paths.Session, StatePath: paths.Database}
	flagClient, _ := cmd.Flags().GetString("client")
	idText, hash := a.deps.Getenv("TG_API_ID"), a.deps.Getenv("TG_API_HASH")

	switch {
	case flagClient != "" && (idText != "" || hash != ""):
		return cfg, fail(exitcode.Usage, "use either --client or TG_API_ID and TG_API_HASH, not both")
	case idText != "" || hash != "":
		id, err := strconv.Atoi(idText)
		if err != nil || id <= 0 || hash == "" {
			return cfg, fail(exitcode.Usage, "TG_API_ID must be a positive number and TG_API_HASH must not be empty")
		}
		cfg.AppID, cfg.AppHash = id, hash
		return cfg, nil
	}

	name := flagClient
	if name == "" {
		name = a.config.Profiles[filepath.Base(paths.Dir)].Client
	}
	if name == "" {
		return cfg, fail(exitcode.Usage, "no application credentials: set TG_API_ID and TG_API_HASH (an application from https://my.telegram.org) or start with --client <preset>, for example --client desktop")
	}
	if !presetName.MatchString(name) {
		return cfg, fail(exitcode.Usage, "invalid client preset name %q", name)
	}
	content, err := a.deps.FetchPresets(cmd.Context())
	if err != nil {
		return cfg, fail(exitcode.Failure, "fetch the client presets: %v", err)
	}
	creds, err := clientpreset.Resolve(content, name)
	if err != nil {
		return cfg, fail(exitcode.Usage, "%v", err)
	}
	if flagClient != "" {
		if err := a.saveProfile(paths, map[string]string{"client": name}, false); err != nil {
			return cfg, err
		}
	}
	a.warn(cmd, "note: using the %q official client preset. Telegram puts accounts of unofficial clients under observation (opentele documentation, quoting Telegram's API terms); the risk for this tool is not assessed.", name)
	cfg.AppID, cfg.AppHash = creds.AppID, creds.AppHash
	return cfg, nil
}

// maxDeviceName bounds the device name: Telegram shows it in a narrow list.
const maxDeviceName = 64

// deviceModel finds the device name: --device, else the one saved for the
// profile, else empty (the client then uses its default). A name given by
// flag is saved.
func (a *app) deviceModel(cmd *cobra.Command, paths daemon.Paths) (string, error) {
	name, _ := cmd.Flags().GetString("device")
	if name == "" {
		return a.config.Profiles[filepath.Base(paths.Dir)].Device, nil
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxDeviceName {
		return "", fail(exitcode.Usage, "the device name must be 1 to %d characters", maxDeviceName)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fail(exitcode.Usage, "the device name must not contain control characters")
		}
	}
	if err := a.saveProfile(paths, map[string]string{"device": name}, false); err != nil {
		return "", err
	}
	return name, nil
}

func (a *app) daemonStart(cmd *cobra.Command, _ []string) error {
	if err := a.migrateProfiles(); err != nil {
		return err
	}
	profile, paths, err := a.profile(cmd)
	if err != nil {
		return err
	}
	warnings, err := daemon.CheckFileNames(paths)
	if err != nil {
		var conflict *daemon.FileConflictError
		if errors.As(err, &conflict) {
			return fail(exitcode.Usage, "%v", err)
		}
		return fail(exitcode.Failure, "%v", err)
	}
	for _, warning := range warnings {
		fmt.Fprintln(a.deps.Stderr, "warning: "+warning)
	}
	foreground, _ := cmd.Flags().GetBool("foreground")
	journalPath, _ := cmd.Flags().GetString("journal")
	if journalPath != "" && (!foreground || journalPath != paths.Log) {
		return fail(exitcode.Usage, "--journal requires --foreground and the current profile journal")
	}
	idle, _ := cmd.Flags().GetDuration("idle-timeout")
	if idle < 0 {
		return fail(exitcode.Usage, "--idle-timeout must not be negative")
	}
	_, limiter, err := a.daemonThrottle()
	if err != nil {
		return err
	}
	if !foreground {
		return a.startBackground(cmd, profile, paths, idle)
	}
	if _, pingErr := daemon.Ping(cmd.Context(), paths.Socket); pingErr == nil {
		if err := a.checkRunningNetwork(cmd, paths); err != nil {
			return err
		}
		return fail(exitcode.Failure, "a daemon is already running for this profile")
	}
	cfg, err := a.credentials(cmd, paths)
	if err != nil {
		return err
	}
	cfg.Network, err = a.networkSettings(cmd, paths, true)
	if err != nil {
		return err
	}
	cfg.Throttle = limiter
	if cfg.DeviceModel, err = a.deviceModel(cmd, paths); err != nil {
		return err
	}
	tg, newTelegram, err := a.telegramFactory(cfg)
	if err != nil {
		return fail(exitcode.Failure, "create the Telegram client: %v", err)
	}
	logger := a.daemonLog(cmd)
	if journalPath != "" {
		journal, err := daemon.OpenJournal(journalPath)
		if err != nil {
			return fail(exitcode.Failure, "%v", err)
		}
		defer journal.Close()
		level := slog.LevelInfo
		if a.settings(cmd).quiet {
			level = slog.LevelWarn
		}
		logger = slog.New(slog.NewTextHandler(journal, &slog.HandlerOptions{Level: level}))
	}
	err = daemon.Run(cmd.Context(), daemon.Options{
		FileWarningsReported: true,
		ClientPreset:         a.config.Profiles[profile].Client,
		Profile:              profile,
		Paths:                paths,
		Telegram:             tg,
		NewTelegram:          newTelegram,
		TelegramConfig:       cfg,
		Log:                  logger,
		IdleTimeout:          idle,
	})
	if err != nil {
		return a.daemonStartError(err)
	}
	return a.success(cmd, daemonStartedResult{Stopped: true})
}

// daemonStartError reports the exit class of a failed start. A configuration
// problem of the profile keeps the usage class; every other failure, including a
// broken move, is a plain failure. The error types carry their own code, so the
// caller does not have to know each of them.
func (a *app) daemonStartError(err error) error {
	if errors.Is(err, daemon.ErrSessionInUse) {
		return fail(exitcode.Usage, "%v", err)
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == exitcode.Usage {
		return fail(exitcode.Usage, "%v", err)
	}
	return fail(exitcode.Failure, "%v", err)
}

// telegramFactory starts with the concrete initial transport and keeps the
// original configuration available to the daemon for later replacements.
func (a *app) telegramFactory(cfg daemon.TelegramConfig) (daemon.Telegram, func(context.Context, daemon.TelegramConfig) (daemon.Telegram, error), error) {
	initial := cfg
	initial.Network = daemon.InitialNetwork(cfg.Network)
	client, err := a.deps.NewTelegram(initial)
	if err != nil {
		return nil, nil, err
	}
	factory := func(ctx context.Context, cfg daemon.TelegramConfig) (daemon.Telegram, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return a.deps.NewTelegram(cfg)
	}
	return client, factory, nil
}

// startBackground launches the daemon as a separate process and waits for its
// socket. The process re-runs this command with --foreground, so it finds the
// credentials the way a foreground start does: from the environment and the
// profile files, never from its arguments.
func (a *app) startBackground(cmd *cobra.Command, profile string, paths daemon.Paths, idle time.Duration) error {
	// A running daemon needs no credentials from this process. Check it before
	// resolving keys or saving a new device name.
	if pid, err := daemon.Ping(cmd.Context(), paths.Socket); err == nil {
		if err := a.checkRunningNetwork(cmd, paths); err != nil {
			return err
		}
		return a.backgroundStarted(cmd, profile, daemon.Background{PID: pid, Socket: paths.Socket, Log: paths.Log})
	} else if !rpc.IsNotRunning(err) {
		return fail(exitcode.Failure, "ask the daemon for its state: %v", err)
	}
	if _, err := a.networkSettings(cmd, paths, true); err != nil {
		return err
	}
	// The credentials are resolved here first, so a wrong or missing preset is
	// an answer of this command and not a silent failure of a process the user
	// cannot see. Only the client preset name is passed on, and it is saved for
	// the profile, so the child needs no credential either way.
	if _, err := a.credentials(cmd, paths); err != nil {
		return err
	}
	if _, err := a.deviceModel(cmd, paths); err != nil {
		return err
	}
	// The database is prepared here, in the parent, before the child is launched:
	// a big move must not eat the StartTimeout of the child, and a failure has to
	// be an answer of this command rather than a line in a journal. The order is
	// the order of Run: the record of the data directory is checked before the
	// move, because a move deletes the old file. The child finds the database in
	// place and does nothing.
	var moved bool
	err := daemon.WithProfileLock(paths, func() error {
		if err := daemon.CheckDatabaseBinding(paths); err != nil {
			return err
		}
		var moveErr error
		moved, moveErr = daemon.MoveLegacyDatabase(paths)
		if moveErr != nil {
			return moveErr
		}
		return daemon.BindDatabase(paths)
	})
	if err != nil {
		return a.daemonStartError(err)
	}
	if moved {
		// The child logs the line in its journal only when it runs in the
		// foreground, so the parent reports the move it did itself. The answer of
		// the command does not change: this is a hint, and --quiet silences it.
		a.warn(cmd, "note: moved the database from %s to %s", paths.LegacyDatabase, paths.Database)
	}
	args := []string{"daemon", "start", "--foreground", "--profile", profile}
	args = append(args, "--journal", paths.Log)
	if idle > 0 {
		args = append(args, "--idle-timeout", idle.String())
	}
	bg, err := daemon.Start(cmd.Context(), daemon.StartOptions{
		Paths:       paths,
		IdleTimeout: idle,
		Args:        args,
		Launcher:    a.deps.LaunchDaemon,
	})
	if err != nil {
		// An error of a background start is never hidden: --quiet silences
		// hints, not the reason a daemon did not come up.
		var exited interface{ ExitCode() int }
		if errors.As(err, &exited) && exited.ExitCode() == exitcode.Usage {
			return fail(exitcode.Usage, "%v", err)
		}
		return fail(exitcode.Failure, "%v", err)
	}
	return a.backgroundStarted(cmd, profile, bg)
}

func (a *app) backgroundStarted(cmd *cobra.Command, profile string, bg daemon.Background) error {
	a.warn(cmd, "daemon is running in the background, journal: %s", bg.Log)
	return a.success(cmd, backgroundStartedResult{
		Running: true,
		PID:     bg.PID,
		Profile: profile,
		Socket:  bg.Socket,
		Log:     bg.Log,
	})
}

// daemonLog is the journal of the daemon on stderr. --quiet keeps it to the
// lines that report a problem: the start and stop lines are the progress of the
// command, a warning is not.
func (a *app) daemonLog(cmd *cobra.Command) *slog.Logger {
	level := slog.LevelInfo
	if a.settings(cmd).quiet {
		level = slog.LevelWarn
	}
	return slog.New(slog.NewTextHandler(a.deps.Stderr, &slog.HandlerOptions{Level: level}))
}

// daemonStartedResult is what `daemon start --foreground` prints when the daemon
// has stopped.
type daemonStartedResult struct {
	Stopped bool `json:"stopped"`
}

// backgroundStartedResult is what a background `daemon start` prints: the daemon
// is up, and these are the facts a caller needs to reach it and its journal.
type backgroundStartedResult struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Profile string `json:"profile"`
	Socket  string `json:"socket"`
	Log     string `json:"log"`
}

func (a *app) daemonStatus(cmd *cobra.Command, _ []string) error {
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var st rpc.StatusResult
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodStatus, nil, &st); err != nil {
		return err
	}
	return a.success(cmd, st)
}

func (a *app) daemonStop(cmd *cobra.Command, _ []string) error {
	profile, paths, err := a.profile(cmd)
	if err != nil {
		return err
	}
	// The process id comes from the daemon itself, so the stop can tell whether
	// the process is really gone rather than only whether the socket closed.
	pid, err := daemon.Ping(cmd.Context(), paths.Socket)
	if err != nil {
		if rpc.IsNotRunning(err) {
			return fail(exitcode.DaemonNotRunning, "the daemon is not running for profile %q: start it with `tg daemon start`", profile)
		}
		return fail(exitcode.Failure, "ask the daemon for its state: %v", err)
	}
	if err := daemon.Stop(cmd.Context(), paths.Socket, pid, daemon.StopTimeout); err != nil {
		return fail(exitcode.Failure, "%v", err)
	}
	return a.success(cmd, rpc.ShutdownResult{Stopping: true})
}
