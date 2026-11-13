package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) profile(cmd *cobra.Command) (string, daemon.Paths, error) {
	profile, err := cmd.Flags().GetString("profile")
	if err != nil {
		return "", daemon.Paths{}, err
	}
	paths, err := daemon.ResolvePaths(a.deps.Getenv("TG_CLI_HOME"), profile)
	if err != nil {
		return "", daemon.Paths{}, fail(exitcode.Usage, "%v", err)
	}
	return profile, paths, nil
}

// connect dials the daemon of the profile selected by --profile.
func (a *app) connect(ctx context.Context, cmd *cobra.Command) (*rpc.Client, error) {
	profile, paths, err := a.profile(cmd)
	if err != nil {
		return nil, err
	}
	c, err := rpc.Dial(ctx, "unix", paths.Socket)
	if err != nil {
		if rpc.IsNotRunning(err) {
			return nil, fail(exitcode.DaemonNotRunning, "the daemon is not running for profile %q: start it with `tg-cli daemon start`", profile)
		}
		return nil, fail(exitcode.Failure, "connect to the daemon: %v", err)
	}
	return c, nil
}

// call runs one RPC and turns failures into command errors with exit codes.
func (a *app) call(ctx context.Context, c *rpc.Client, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, a.deps.CallTimeout)
	defer cancel()
	return translate(c.Call(ctx, method, params, result))
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpc.Error
	switch {
	case errors.As(err, &rpcErr):
		return fail(exitForRPC(rpcErr), "%s", describe(rpcErr))
	case errors.Is(err, context.DeadlineExceeded):
		return fail(exitcode.Failure, "the daemon did not answer in time")
	case errors.Is(err, context.Canceled):
		return fail(exitcode.Failure, "interrupted")
	case errors.Is(err, rpc.ErrClosed):
		return fail(exitcode.Failure, "the daemon closed the connection")
	}
	return fail(exitcode.Failure, "%v", err)
}

func exitForRPC(e *rpc.Error) int {
	switch e.Code {
	case rpc.CodeNotAuthorized:
		return exitcode.NotAuthorized
	case rpc.CodeFloodWait:
		return exitcode.FloodWait
	case rpc.CodeTelegram:
		return exitcode.Telegram
	case rpc.CodeNotFound:
		return exitcode.NotFound
	case rpc.CodeInvalidParams, rpc.CodeMethodNotFound:
		return exitcode.Usage
	}
	return exitcode.Failure
}

func describe(e *rpc.Error) string {
	if e.Code == rpc.CodeFloodWait {
		if data, ok := e.Data.(map[string]any); ok {
			if secs, ok := data["retry_after_seconds"].(float64); ok {
				return fmt.Sprintf("%s (retry after %d seconds)", e.Message, int(secs))
			}
		}
	}
	return e.Message
}
