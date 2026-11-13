package cli

import (
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
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
		Short: "Run the daemon in the foreground until it is stopped",
		Long: "Run the daemon in the foreground until it is stopped.\n\n" +
			"Application credentials come from TG_API_ID and TG_API_HASH (your own\n" +
			"application, https://my.telegram.org) or from --client, an official client\n" +
			"preset downloaded from the opentele project. The chosen preset is remembered\n" +
			"for the profile, so later starts need no flag.\n\n" +
			"--device sets the device name in the list of Telegram devices. The default is\n" +
			"\"MacBook Pro M1 BRN\"; a name given by --device is remembered for the profile.",
		Args: cobra.NoArgs,
		RunE: a.daemonStart,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[daemonStartedResult]()})
	start.Flags().String("client", "", "official client preset to use instead of your own application, for example desktop")
	start.Flags().String("device", "", "device name Telegram shows for this session, remembered for the profile (default MacBook Pro M1 BRN)")
	cmd.AddCommand(start, a.annotate(&cobra.Command{
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
	cfg := daemon.TelegramConfig{SessionPath: paths.Session}
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
		name = savedClient(paths.Client)
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
		if err := saveClient(paths, name); err != nil {
			return cfg, fail(exitcode.Failure, "remember the client preset: %v", err)
		}
	}
	a.warn(cmd, "note: using the %q official client preset. Telegram puts accounts of unofficial clients under observation (opentele documentation, quoting Telegram's API terms); the risk for this tool is not assessed.", name)
	cfg.AppID, cfg.AppHash = creds.AppID, creds.AppHash
	return cfg, nil
}

// savedClient returns the preset saved for the profile, or "".
func savedClient(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(string(data)); presetName.MatchString(name) {
		return name
	}
	return ""
}

func saveClient(paths daemon.Paths, name string) error {
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(paths.Client, []byte(name+"\n"), 0o600)
}

// maxDeviceName bounds the device name: Telegram shows it in a narrow list.
const maxDeviceName = 64

// deviceModel finds the device name: --device, else the one saved for the
// profile, else empty (the client then uses its default). A name given by
// flag is saved.
func (a *app) deviceModel(cmd *cobra.Command, paths daemon.Paths) (string, error) {
	name, _ := cmd.Flags().GetString("device")
	if name == "" {
		data, err := os.ReadFile(paths.Device)
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(data)), nil
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
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return "", fail(exitcode.Failure, "remember the device name: %v", err)
	}
	if err := os.WriteFile(paths.Device, []byte(name+"\n"), 0o600); err != nil {
		return "", fail(exitcode.Failure, "remember the device name: %v", err)
	}
	return name, nil
}

func (a *app) daemonStart(cmd *cobra.Command, _ []string) error {
	profile, paths, err := a.profile(cmd)
	if err != nil {
		return err
	}
	cfg, err := a.credentials(cmd, paths)
	if err != nil {
		return err
	}
	if cfg.DeviceModel, err = a.deviceModel(cmd, paths); err != nil {
		return err
	}
	tg, err := a.deps.NewTelegram(cfg)
	if err != nil {
		return fail(exitcode.Failure, "create the Telegram client: %v", err)
	}
	err = daemon.Run(cmd.Context(), daemon.Options{
		Profile:  profile,
		Paths:    paths,
		Telegram: tg,
		Log:      a.daemonLog(cmd),
	})
	if err != nil {
		return fail(exitcode.Failure, "%v", err)
	}
	return a.success(cmd, daemonStartedResult{Stopped: true})
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

// daemonStartedResult is what `daemon start` prints when the daemon has stopped.
type daemonStartedResult struct {
	Stopped bool `json:"stopped"`
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
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var res rpc.ShutdownResult
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodShutdown, nil, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}
