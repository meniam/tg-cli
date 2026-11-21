package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/web"
)

func (a *app) webCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use: "web", Short: "Open the local account viewer", Args: cobra.NoArgs, RunE: a.runWeb,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[web.Result](), Formats: []output.Format{output.FormatJSON}})
	cmd.Flags().String("listen", "", "listen address with port (default loopback, scan ports 1313 to 1412)")
	cmd.Flags().Bool("open", false, "open the viewer in the default browser")
	return cmd
}

func (a *app) runWeb(cmd *cobra.Command, _ []string) error {
	profile, paths, err := a.profile(cmd)
	if err != nil {
		return err
	}
	client, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer client.Close()
	var settings rpc.SettingsListResult
	if err := a.call(cmd.Context(), cmd, client, rpc.MethodSettingsList, rpc.SettingsListParams{Prefix: "web."}, &settings); err != nil {
		return err
	}
	var address string
	var open bool
	for _, setting := range settings.Settings {
		switch setting.Key {
		case "web.listen":
			err = json.Unmarshal(setting.Value, &address)
		case "web.open":
			err = json.Unmarshal(setting.Value, &open)
		}
		if err != nil {
			return fail(exitcode.Failure, "invalid web setting %s", setting.Key)
		}
	}
	if a.deps.Getenv != nil {
		if value := a.deps.Getenv("TG_CLI_WEB_LISTEN"); value != "" {
			address = value
		}
		if value := a.deps.Getenv("TG_CLI_WEB_OPEN"); value != "" && !cmd.Flags().Changed("open") {
			var err error
			open, err = strconv.ParseBool(value)
			if err != nil {
				return fail(exitcode.Usage, "TG_CLI_WEB_OPEN must be a boolean")
			}
		}
	}
	explicit := cmd.Flags().Changed("listen")
	if explicit {
		address, _ = cmd.Flags().GetString("listen")
	}
	if cmd.Flags().Changed("open") {
		open, _ = cmd.Flags().GetBool("open")
	}
	remote, err := web.ValidateListen(address, explicit)
	if err != nil {
		return fail(exitcode.Usage, "%v", err)
	}
	var status rpc.StatusResult
	err = a.call(cmd.Context(), cmd, client, rpc.MethodStatus, nil, &status)
	if err != nil {
		return err
	}
	if !status.Authorized || status.AuthLost != "" {
		return fail(exitcode.NotAuthorized, "log in with tg auth login before starting the web viewer")
	}
	var previews struct {
		Enabled bool `json:"enabled"`
	}
	if err := a.call(cmd.Context(), cmd, client, rpc.MethodWebThumbEnabled, nil, &previews); err == nil && !previews.Enabled {
		fmt.Fprintln(a.deps.Stderr, "warning: thumbnail cache requires the configuration directory's downloads folder in Access")
	}
	client.Close()
	dial := a.deps.DialRPC
	if dial == nil {
		dial = rpc.Dial
	}
	expected := a.expectedUser(cmd)
	if expected == "" && status.User != nil {
		expected = strconv.FormatInt(status.User.ID, 10)
	}
	if remote {
		fmt.Fprintln(a.deps.Stderr, "warning: the web viewer is exposed over unencrypted HTTP")
	}
	opts := web.Options{
		Listen: address, Profile: profile, ExplicitListen: explicit,
		Dial: func(ctx context.Context) (*rpc.Client, error) {
			client, err := dial(ctx, "unix", paths.Socket)
			if err == nil {
				client.SetExpectUser(expected)
			}
			return client, err
		},
		Ready: func(result web.Result) error {
			if err := a.success(cmd, result); err != nil {
				return err
			}
			if open {
				launcher := "xdg-open"
				if runtime.GOOS == "darwin" {
					launcher = "open"
				}
				process := exec.CommandContext(cmd.Context(), launcher, result.URL)
				if err := process.Start(); err != nil {
					fmt.Fprintln(a.deps.Stderr, "warning: could not open the default browser")
				} else {
					go func() { _ = process.Wait() }()
				}
			}
			return nil
		},
	}
	if err := web.Run(cmd.Context(), opts); err != nil {
		return fail(exitcode.Failure, "web server: %v", err)
	}
	return nil
}
