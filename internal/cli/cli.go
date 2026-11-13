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
	"tg-cli/internal/output"
)

// defaultCallTimeout bounds one RPC call, matching the daemon request deadline.
const defaultCallTimeout = 2 * time.Minute

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
	// CallTimeout bounds one RPC call; defaultCallTimeout when zero.
	CallTimeout time.Duration
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
	deps Deps
}

// Execute runs the command line and returns the process exit code. The result
// is always one JSON envelope on stdout; logs and prompts go to stderr.
func Execute(ctx context.Context, args []string, deps Deps) int {
	if deps.CallTimeout == 0 {
		deps.CallTimeout = defaultCallTimeout
	}
	a := &app{deps: deps}
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
	_ = output.Failure(deps.Stdout, exitcode.Name(exit), withIDHint(err.Error(), args))
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
		Use:           "tg-cli",
		Short:         "Console client for a personal Telegram account",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().String("profile", daemon.DefaultProfile, "account profile, each has its own session and daemon")
	root.AddCommand(a.daemonCommand(), a.authCommand(), a.chatCommand(), a.messageCommand(), a.noteCommand(), a.searchCommand())
	return root
}

func (a *app) success(data any) error {
	return output.Success(a.deps.Stdout, data)
}
