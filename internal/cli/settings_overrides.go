package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// settingsOverrides records the startup sources, not the values in config.toml.
func (a *app) settingsOverrides(cmd *cobra.Command) (map[string]daemon.SettingOverride, error) {
	out := map[string]daemon.SettingOverride{}
	for _, key := range config.SettingKeys() {
		// The web viewer applies its process-local flags and environment on launch.
		if strings.HasPrefix(key.Flag, "web ") {
			continue
		}
		if key.Env != "" && a.deps.Getenv != nil {
			value := a.deps.Getenv(key.Env)
			if key.Name != "profile.proxy" && key.Name != "behavior.expect_user" {
				value = strings.TrimSpace(value)
			}
			if value != "" {
				v, err := config.ParseSettingString(key.Name, value)
				if err != nil {
					return nil, fail(exitcode.Usage, "invalid setting override for %s", key.Name)
				}
				out[key.Name] = daemon.SettingOverride{Value: v, Source: "env"}
			}
		}
		if key.Flag == "" || strings.HasPrefix(key.Flag, "search ") {
			continue
		}
		_, flagName, _ := strings.Cut(key.Flag, "--")
		flag := cmd.Flags().Lookup(flagName)
		if flag != nil && flag.Changed {
			v, err := config.ParseSettingString(key.Name, flag.Value.String())
			if err != nil {
				return nil, fail(exitcode.Usage, "invalid setting override for %s", key.Name)
			}
			out[key.Name] = daemon.SettingOverride{Value: v, Source: "flag"}
		}
	}
	return out, nil
}
