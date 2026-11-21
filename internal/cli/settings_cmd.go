package cli

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type settingsTokenResetResult struct {
	Updated bool   `json:"updated"`
	Hint    string `json:"hint"`
}

func (a *app) settingsCommand() *cobra.Command {
	root := &cobra.Command{Use: "settings", Short: "Inspect and change profile settings"}
	list := a.annotate(&cobra.Command{Use: "list", Short: "List profile settings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		prefix, _ := cmd.Flags().GetString("prefix")
		showIDs, _ := cmd.Flags().GetBool("show-ids")
		phrase := ""
		if showIDs {
			var err error
			phrase, err = a.settingsIDsSecret()
			if err != nil {
				return err
			}
		}
		var result rpc.SettingsListResult
		if err := a.settingsCall(cmd, rpc.MethodSettingsList, rpc.SettingsListParams{Prefix: prefix, Passphrase: phrase}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.SettingsListResult]()})
	list.Flags().String("prefix", "", "filter settings by key prefix")
	list.Flags().Bool("show-ids", false, "show all access list IDs after owner passphrase")
	root.AddCommand(list)
	get := a.annotate(&cobra.Command{Use: "get <key>", Short: "Read a profile setting", Long: "Read a profile setting. Use --show-ids and the owner passphrase for all access list IDs.", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		phrase := ""
		showIDs, _ := cmd.Flags().GetBool("show-ids")
		if showIDs && args[0] != "access.allow" && args[0] != "access.block" && args[0] != "access.write" && args[0] != "access.admin" {
			return fail(exitcode.Usage, "--show-ids requires an access ID list")
		}
		if showIDs {
			var err error
			phrase, err = a.settingsIDsSecret()
			if err != nil {
				return err
			}
		}
		var result rpc.SettingValue
		if err := a.settingsCall(cmd, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: args[0], Passphrase: phrase}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.SettingValue](), Args: []argSpec{{Name: "key", Required: true}}})
	get.Flags().Bool("show-ids", false, "show all access list IDs after owner passphrase")
	root.AddCommand(get)
	for _, action := range []string{"set", "unset", "add", "remove"} {
		action := action
		use := action + " <key> <value>"
		argsCheck := cobra.ExactArgs(2)
		argSpecs := []argSpec{{Name: "key", Required: true}, {Name: "value", Required: true}}
		if action == "unset" {
			use = "unset <key>"
			argsCheck = cobra.ExactArgs(1)
			argSpecs = argSpecs[:1]
		}
		if action == "add" || action == "remove" {
			use = action + " <key> <value>..."
			argsCheck = cobra.MinimumNArgs(2)
			argSpecs[1].Variadic = true
		}
		root.AddCommand(a.annotate(&cobra.Command{Use: use, Short: strings.Title(action) + " a profile setting", Long: "Change a profile setting. Changes to access.* require the owner's passphrase.", Args: argsCheck, RunE: func(cmd *cobra.Command, args []string) error {
			key, ok := config.LookupSetting(args[0])
			if !ok {
				return fail(exitcode.Usage, "unknown setting %q", args[0])
			}
			var parsed json.RawMessage
			var values []json.RawMessage
			if action == "set" && (key.Type == "ids" || key.Type == "paths") {
				return fail(exitcode.Usage, "use add or remove to change a list")
			}
			if action == "set" {
				var err error
				parsed, err = config.ParseSettingString(key.Name, args[1])
				if err != nil {
					return fail(exitcode.Usage, "invalid setting value")
				}
			}
			if action == "add" || action == "remove" {
				if key.Type != "ids" && key.Type != "paths" {
					return fail(exitcode.Usage, "setting is not a list")
				}
				for _, v := range args[1:] {
					raw, err := config.ParseSettingString(key.Name, v)
					if err != nil {
						return fail(exitcode.Usage, "invalid setting value")
					}
					var items []json.RawMessage
					if json.Unmarshal(raw, &items) != nil {
						return fail(exitcode.Usage, "invalid setting value")
					}
					values = append(values, items...)
				}
			}
			phrase := ""
			if key.Protected {
				var err error
				phrase, err = a.settingsSecret("Owner passphrase: ")
				if err != nil {
					return err
				}
			}
			var params any
			var method string
			switch action {
			case "set":
				method = rpc.MethodSettingsSet
				params = rpc.SettingsSetParams{Key: key.Name, Value: parsed, Passphrase: phrase}
			case "unset":
				method = rpc.MethodSettingsUnset
				params = rpc.SettingsUnsetParams{Key: key.Name, Passphrase: phrase}
			case "add":
				method = rpc.MethodSettingsAdd
				params = rpc.SettingsListChangeParams{Key: key.Name, Values: values, Passphrase: phrase}
			case "remove":
				method = rpc.MethodSettingsRemove
				params = rpc.SettingsListChangeParams{Key: key.Name, Values: values, Passphrase: phrase}
			}
			var result rpc.SettingsWriteResult
			if err := a.settingsCall(cmd, method, params, &result); err != nil {
				return err
			}
			return a.success(cmd, result)
		}}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.SettingsWriteResult](), Args: argSpecs}))
	}
	token := &cobra.Command{Use: "token", Short: "Manage the owner passphrase"}
	for _, action := range []string{"init", "change"} {
		action := action
		token.AddCommand(a.annotate(&cobra.Command{Use: action, Short: strings.Title(action) + " the owner passphrase", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			old := ""
			if action == "change" {
				var err error
				old, err = a.settingsSecret("Current owner passphrase: ")
				if err != nil {
					return err
				}
			}
			phrase, err := a.settingsSecret("New owner passphrase: ")
			if err != nil {
				return err
			}
			confirm, err := a.settingsSecret("Repeat owner passphrase: ")
			if err != nil {
				return err
			}
			if phrase != confirm {
				return fail(exitcode.Usage, "passphrases do not match")
			}
			method := rpc.MethodSettingsTokenInit
			var params any = rpc.SettingsTokenInitParams{Passphrase: phrase}
			if action == "change" {
				method = rpc.MethodSettingsTokenChange
				params = rpc.SettingsTokenChangeParams{OldPassphrase: old, NewPassphrase: phrase}
			}
			var result rpc.SettingsTokenResult
			if err := a.settingsCall(cmd, method, params, &result); err != nil {
				return err
			}
			return a.success(cmd, result)
		}}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.SettingsTokenResult]()}))
	}
	token.AddCommand(a.annotate(&cobra.Command{
		Use: "reset", Short: "Reset the owner passphrase while the daemon is stopped",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.deps.ReadSecret == nil {
				return fail(exitcode.Usage, "a terminal is required for confirmation")
			}
			answer, err := a.deps.ReadSecret("Reset the owner passphrase? Type yes to confirm: ")
			if err != nil {
				return fail(exitcode.Usage, "cannot read confirmation from terminal")
			}
			if answer != "yes" {
				return fail(exitcode.Usage, "owner passphrase reset was not confirmed")
			}
			if err := a.guardLegacyHomeSettings(); err != nil {
				return err
			}
			_, paths, err := a.profile(cmd)
			if err != nil {
				return err
			}
			if err := daemon.ResetOwnerSecretOfflineWithConfig(cmd.Context(), paths, a.deps.ConfigPath); err != nil {
				var legacy *daemon.LegacySettingsError
				var input *daemon.MigrationInputError
				if errors.As(err, &legacy) || errors.As(err, &input) {
					return a.daemonStartError(err)
				}
				if errors.Is(err, daemon.ErrAlreadyRunning) || errors.Is(err, daemon.ErrDatabaseInUse) {
					return fail(exitcode.Failure, "stop the daemon and release the profile database before resetting the owner passphrase")
				}
				return fail(exitcode.Failure, "cannot reset owner passphrase")
			}
			return a.success(cmd, settingsTokenResetResult{Updated: true, Hint: "Run tg settings token init to set a new owner passphrase"})
		},
	}, dangerous, commandSpec{Safety: safetyLocalWrite, Result: typeOf[settingsTokenResetResult]()}))
	root.AddCommand(token)
	return root
}

func (a *app) settingsIDsSecret() (string, error) {
	phrase, err := a.settingsSecret("Owner passphrase: ")
	if err != nil {
		return "", err
	}
	if phrase == "" {
		return "", fail(exitcode.Usage, "owner passphrase is required")
	}
	return phrase, nil
}

func (a *app) settingsSecret(prompt string) (string, error) {
	if a.deps.ReadSecret == nil {
		return "", fail(exitcode.Usage, "a terminal is required for the owner passphrase")
	}
	value, err := a.deps.ReadSecret(prompt)
	if err != nil {
		return "", fail(exitcode.Usage, "cannot read owner passphrase from terminal")
	}
	return value, nil
}

func (a *app) settingsCall(cmd *cobra.Command, method string, params, result any) error {
	if err := a.guardLegacyHomeSettings(); err != nil {
		return err
	}
	client, err := a.connect(cmd.Context(), cmd)
	if err == nil {
		defer client.Close()
		return a.call(cmd.Context(), cmd, client, method, params, result)
	}
	var ce *cmdError
	if !errors.As(err, &ce) || ce.exit != exitcode.DaemonNotRunning {
		return err
	}
	profile, _, err := a.profile(cmd)
	if err != nil {
		return err
	}
	home := a.config.Home
	if a.deps.Getenv != nil {
		if v := a.deps.Getenv("TG_CLI_HOME"); v != "" {
			home = v
		}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fail(exitcode.Failure, "cannot encode settings request")
	}
	overrides, err := a.settingsOverrides(cmd)
	if err != nil {
		return err
	}
	value, err := daemon.OfflineSettings(cmd.Context(), a.deps.ConfigPath, home, profile, method, raw, overrides)
	if err != nil {
		var rpcErr *rpc.Error
		if errors.As(err, &rpcErr) {
			return translate(err)
		}
		return a.daemonStartError(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fail(exitcode.Failure, "cannot encode settings response")
	}
	if json.Unmarshal(data, result) != nil {
		return fail(exitcode.Failure, "cannot decode settings response")
	}
	return nil
}
