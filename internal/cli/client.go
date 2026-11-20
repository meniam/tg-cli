package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

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
	if !cmd.Flags().Changed("profile") {
		profile = a.config.DefaultProfile
	}
	if err := a.moveLegacyHome(cmd); err != nil {
		return "", daemon.Paths{}, err
	}
	home := a.deps.Getenv("TG_CLI_HOME")
	if home == "" {
		home = a.config.Home
	}
	files := a.config.Files
	// The database root sits next to the configuration file: the history does
	// not move with home or TG_CLI_HOME.
	dbRoot := filepath.Join(filepath.Dir(a.deps.ConfigPath), "db")
	paths, err := daemon.ResolvePaths(home, dbRoot, profile, daemon.FileNames{Database: files.Database, Session: files.Session, Log: files.Log, Deletions: files.Deletions, Access: files.Access})
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
	// The socket path names the profile and nothing secret: it is the one fact
	// that tells a lost daemon from a wrong profile.
	a.diagnose(cmd, "connect profile=%s socket=%s", profile, paths.Socket)
	dial := a.deps.DialRPC
	if dial == nil {
		dial = rpc.Dial
	}
	c, err := dial(ctx, "unix", paths.Socket)
	if err != nil {
		if rpc.IsNotRunning(err) {
			return nil, fail(exitcode.DaemonNotRunning, "the daemon is not running for profile %q: start it with `tg daemon start`", profile)
		}
		return nil, fail(exitcode.Failure, "connect to the daemon: %v", err)
	}
	c.SetExpectUser(a.expectedUser(cmd))
	return c, nil
}

func (a *app) expectedUser(cmd *cobra.Command) string {
	expect, _ := cmd.Flags().GetString("expect-user")
	if cmd.Flags().Changed("expect-user") {
		return expect
	}
	if a.deps.Getenv != nil {
		if expect = a.deps.Getenv("TG_CLI_EXPECT_USER"); expect != "" {
			return expect
		}
	}
	return a.config.Behavior.ExpectUser
}

// call runs one RPC and turns failures into command errors with exit codes. It
// reports the call on stderr under --verbose.
func (a *app) call(ctx context.Context, cmd *cobra.Command, c *rpc.Client, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, a.callTimeout(method))
	defer cancel()
	started := time.Now()
	a.diagnose(cmd, "call method=%s", method)
	milliseconds, callErr := c.CallWithThrottle(ctx, method, params, result)
	a.throttledMS += milliseconds
	err := translate(callErr)
	a.diagnose(cmd, "call method=%s took=%s failed=%t", method, time.Since(started).Round(time.Millisecond), err != nil)
	return err
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpc.Error
	switch {
	case errors.As(err, &rpcErr):
		e := fail(exitForRPC(rpcErr), "%s", describe(rpcErr))
		e.data, _ = rpcErr.Data.(map[string]any)
		return e
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
	case rpc.CodeOffline:
		return exitcode.Offline
	case rpc.CodeNotAuthorized:
		return exitcode.NotAuthorized
	case rpc.CodeFloodWait:
		return exitcode.FloodWait
	case rpc.CodeTelegram:
		return exitcode.Telegram
	case rpc.CodeNotFound:
		return exitcode.NotFound
	case rpc.CodeCursorExpired:
		return exitcode.CursorExpired
	case rpc.CodeWriteDisabled:
		return exitcode.WriteDisabled
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
