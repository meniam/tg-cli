package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
)

func (a *app) networkSettings(cmd *cobra.Command, paths daemon.Paths, save bool) (netconf.Settings, error) {
	var s netconf.Settings
	profile := a.config.Profiles[filepath.Base(paths.Dir)]
	transport := profile.Transport
	if transport == "" {
		transport = "tcp"
	}
	proxyValue := profile.Proxy
	var err error
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
		changes := map[string]string{}
		if cmd.Flags().Changed("transport") {
			changes["transport"] = string(s.Transport)
		}
		if cmd.Flags().Changed("proxy") || a.deps.Getenv("TG_CLI_PROXY") != "" {
			changes["proxy"] = s.Proxy.Encode()
		}
		if len(changes) > 0 {
			if err := a.saveProfile(paths, changes, false); err != nil {
				return s, err
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
	saved := a.config.Profiles[filepath.Base(paths.Dir)].Proxy
	if saved == "" {
		saved = "none"
	}
	if status.Transport != transport || status.Proxy != requested.Proxy.Redacted() || saved != requested.Proxy.Encode() {
		return fail(exitcode.Usage, "the daemon is already running with transport %s; stop it first", status.Transport)
	}
	return nil
}
