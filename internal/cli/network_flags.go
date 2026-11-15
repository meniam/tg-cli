package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
)

func networkFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
func (a *app) networkSettings(cmd *cobra.Command, paths daemon.Paths, save bool) (netconf.Settings, error) {
	var s netconf.Settings
	transport, err := networkFile(paths.Transport)
	if err != nil {
		return s, fail(exitcode.Failure, "read network settings")
	}
	if transport == "" {
		transport = "tcp"
	}
	proxyValue, err := networkFile(paths.Proxy)
	if err != nil {
		return s, fail(exitcode.Failure, "read network settings")
	}
	if cmd.Flags().Changed("transport") {
		transport, _ = cmd.Flags().GetString("transport")
	}
	if env := a.deps.Getenv("TG_CLI_PROXY"); env != "" {
		proxyValue = env
	}
	if cmd.Flags().Changed("proxy") {
		proxyValue, _ = cmd.Flags().GetString("proxy")
	}
	s.Transport, err = netconf.ParseTransport(transport)
	if err != nil {
		return s, fail(exitcode.Usage, "%v", err)
	}
	s.Proxy, err = netconf.ParseProxy(proxyValue)
	if err != nil {
		return s, fail(exitcode.Usage, "invalid proxy address")
	}
	if err = s.Validate(); err != nil {
		return s, fail(exitcode.Usage, "%v", err)
	}
	if save {
		if err = os.MkdirAll(paths.Dir, 0700); err != nil {
			return s, fail(exitcode.Failure, "save network settings")
		}
		for _, f := range []struct{ path, value string }{{paths.Transport, string(s.Transport)}, {paths.Proxy, s.Proxy.Encode()}} {
			file, openErr := os.OpenFile(f.path, os.O_WRONLY|os.O_CREATE, 0600)
			if openErr != nil {
				return s, fail(exitcode.Failure, "save network settings")
			}
			writeErr := file.Chmod(0600)
			if writeErr == nil {
				writeErr = file.Truncate(0)
			}
			if writeErr == nil {
				_, writeErr = file.WriteString(f.value + "\n")
			}
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				return s, fail(exitcode.Failure, "save network settings")
			}
		}
	}
	return s, nil
}
func (a *app) checkRunningNetwork(cmd *cobra.Command, paths daemon.Paths) error {
	if !cmd.Flags().Changed("transport") && !cmd.Flags().Changed("proxy") && a.deps.Getenv("TG_CLI_PROXY") == "" {
		return nil
	}
	requested, err := a.networkSettings(cmd, paths, false)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var status rpc.StatusResult
	if err = a.call(cmd.Context(), cmd, c, rpc.MethodStatus, nil, &status); err != nil {
		return err
	}
	if status.ConnectionInfo == nil {
		return fail(exitcode.Usage, "the daemon is already running; stop it first")
	}
	transport := string(requested.Transport)
	if requested.Proxy != nil && requested.Proxy.Kind == netconf.MTProxy {
		transport = "mtproxy"
	}
	saved, err := networkFile(paths.Proxy)
	if err != nil {
		return fail(exitcode.Failure, "read network settings")
	}
	if saved == "" {
		saved = "none"
	}
	if status.Transport != transport || status.Proxy != requested.Proxy.Redacted() || saved != requested.Proxy.Encode() {
		return fail(exitcode.Usage, "the daemon is already running with transport %s; stop it first", status.Transport)
	}
	return nil
}
