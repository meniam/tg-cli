package cli

import (
	"encoding/json"
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
	proxyFlag := cmd.Flags().Changed("proxy")
	transportFlag := cmd.Flags().Changed("transport")
	proxyEnv := a.deps.Getenv("TG_CLI_PROXY")
	if !transportFlag && !proxyFlag && proxyEnv == "" {
		return nil
	}
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var status rpc.StatusResult
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodStatus, nil, &status); err != nil {
		return err
	}
	if status.ConnectionInfo == nil {
		return fail(exitcode.Usage, "the daemon is already running; stop it first")
	}
	var transport, proxy rpc.SettingValue
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "profile.transport"}, &transport); err != nil {
		return err
	}
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "profile.proxy"}, &proxy); err != nil {
		return err
	}
	if transportFlag {
		requested, _ := cmd.Flags().GetString("transport")
		parsed, err := netconf.ParseTransport(requested)
		if err != nil {
			return fail(exitcode.Usage, "%v", err)
		}
		var actual string
		if json.Unmarshal(transport.Value, &actual) != nil {
			return fail(exitcode.Failure, "daemon network settings are unavailable")
		}
		if actual == "" {
			actual = "tcp"
		}
		if string(parsed) != actual {
			return fail(exitcode.Usage, "the daemon is already running; stop it first")
		}
	}
	if proxyFlag || proxyEnv != "" {
		requested := proxyEnv
		if proxyFlag {
			requested, _ = cmd.Flags().GetString("proxy")
		}
		parsed, err := netconf.ParseProxy(requested)
		if err != nil {
			return fail(exitcode.Usage, "invalid proxy address")
		}
		// The RPC deliberately hides proxy credentials, so a nonempty proxy
		// cannot be proven equal to the running one.
		if parsed != nil || status.Proxy != "" {
			return fail(exitcode.Usage, "the daemon is already running; stop it first")
		}
	}
	return nil
}
