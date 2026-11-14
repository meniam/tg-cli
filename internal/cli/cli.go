package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// defaultCallTimeout bounds one RPC call, matching the daemon request deadline.
const defaultCallTimeout = 2 * time.Minute
const mediaCallTimeout = 600 * time.Second

func (a *app) callTimeout(method string) time.Duration {
	if method == rpc.MethodMessageSendMedia {
		return mediaCallTimeout
	}
	return a.deps.CallTimeout
}

// Deps are the things the commands need from the outside world. Tests replace
// them; main fills them with the real ones.
type Deps struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Getenv reads environment variables.
	Getenv func(string) string
	// ReadLine prints prompt to stderr and reads a line of input.
	ReadLine func(prompt string) (string, error)
	// ReadSecret does the same without echoing the input.
	ReadSecret func(prompt string) (string, error)
	// NewTelegram creates the Telegram client of the daemon.
	NewTelegram func(daemon.TelegramConfig) (daemon.Telegram, error)
	// FetchPresets downloads the file with the official client presets.
	FetchPresets func(context.Context) ([]byte, error)
	// LaunchDaemon starts the detached daemon process of a background start.
	// It is a field so that tests start nothing for real.
	LaunchDaemon func(args []string, logPath string) (int, func() error, error)
	// CallTimeout bounds one RPC call; defaultCallTimeout when zero.
	CallTimeout time.Duration
	// DialRPC replaces the daemon connection in transport-level tests.
	DialRPC func(context.Context, string, string) (*rpc.Client, error)
}

// cmdError carries the exit code of a failed command.
type cmdError struct {
	exit int
	msg  string
}

func (e *cmdError) Error() string { return e.msg }

func fail(exit int, format string, args ...any) *cmdError {
	return &cmdError{exit: exit, msg: fmt.Sprintf(format, args...)}
}

type app struct {
	deps        Deps
	throttledMS int64
	// specs holds the annotation of every runnable command, filled while the
	// command tree is built and read by `schema`.
	specs map[*cobra.Command]commandSpec
}

// Execute runs the command line and returns the process exit code. A result is
// one envelope on stdout in the chosen format, or one item per line for JSONL;
// logs and prompts go to stderr.
func Execute(ctx context.Context, args []string, deps Deps) int {
	if deps.CallTimeout == 0 {
		deps.CallTimeout = defaultCallTimeout
	}
	a := &app{deps: deps, specs: map[*cobra.Command]commandSpec{}}
	root := a.rootCommand()
	root.SetArgs(args)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitcode.OK
	}
	exit := exitcode.Usage // cobra reports unknown commands and flags as plain errors
	var ce *cmdError
	if errors.As(err, &ce) {
		exit = ce.exit
	}
	// The command that failed may be a subcommand, an unknown name or nothing at
	// all: the format is read from wherever cobra stopped.
	a.failure(root, exit, withIDHint(err.Error(), args))
	return exit
}

// negativeID matches a chat id of a group or a channel, as typed on the command line.
var negativeID = regexp.MustCompile(`^-[0-9]{3,}$`)

// withIDHint explains the one parsing trap of the command line: a negative id
// looks like a flag, so it needs -- before it.
func withIDHint(message string, args []string) string {
	if !strings.Contains(message, "unknown shorthand flag") {
		return message
	}
	for _, arg := range args {
		if negativeID.MatchString(arg) {
			return message + ": put -- before negative chat ids, like `chat info -- " + arg + "`"
		}
	}
	return message
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "tg",
		Short:         "Console client for a personal Telegram account",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	// Reject output usage errors before any command can read, write or delete data.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		_, err := a.outputSettingsFor(cmd)
		return err
	}
	root.PersistentFlags().String("expect-user", "", "require account id or @username (or TG_CLI_EXPECT_USER)")
	root.PersistentFlags().String("profile", daemon.DefaultProfile, "account profile, each has its own session and daemon")
	addOutputFlags(root)
	root.AddCommand(
		a.daemonCommand(),
		a.authCommand(),
		a.chatCommand(),
		a.userCommand(),
		a.contactCommand(),
		a.messageCommand(),
		a.folderCommand(),
		a.accountCommand(),
		a.noteCommand(),
		a.searchCommand(),
		a.versionCommand(),
		a.schemaCommand(root),
	)
	return root
}
