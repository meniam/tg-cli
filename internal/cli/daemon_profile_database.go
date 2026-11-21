package cli

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// applyDatabaseSettings overlays stored values on the legacy configuration.
// The legacy fields remain a fallback until the migration in the next stage.
func (a *app) applyDatabaseSettings(paths daemon.Paths, values map[string]json.RawMessage) error {
	name := filepath.Base(paths.Dir)
	_, a.storedClient = values["profile.client"]
	if a.config.Profiles == nil {
		a.config.Profiles = make(map[string]config.Profile)
	}
	profile := a.config.Profiles[name]
	fields := map[string]any{
		"profile.client": &profile.Client, "profile.device": &profile.Device, "profile.transport": &profile.Transport, "profile.proxy": &profile.Proxy,
		"behavior.throttle_percent": &a.config.Behavior.ThrottlePercent, "behavior.flood_autowait": &a.config.Behavior.FloodAutoWait, "behavior.expect_user": &a.config.Behavior.ExpectUser,
		"background_sync.enabled": &a.config.BackgroundSync.Enabled, "background_sync.window_start": &a.config.BackgroundSync.WindowStart, "background_sync.window_end": &a.config.BackgroundSync.WindowEnd,
		"background_sync.interval_min": &a.config.BackgroundSync.IntervalMin, "background_sync.interval_max": &a.config.BackgroundSync.IntervalMax, "background_sync.page_pause_min": &a.config.BackgroundSync.PagePauseMin, "background_sync.page_pause_max": &a.config.BackgroundSync.PagePauseMax,
		"semantic_search.enabled": &a.config.SemanticSearch.Enabled, "semantic_search.ollama_url": &a.config.SemanticSearch.OllamaURL, "semantic_search.model": &a.config.SemanticSearch.Model, "semantic_search.batch": &a.config.SemanticSearch.Batch, "semantic_search.interval": &a.config.SemanticSearch.Interval, "semantic_search.min_similarity": &a.config.SemanticSearch.MinSimilarity,
	}
	for key, dst := range fields {
		if value, ok := values[key]; ok {
			if err := json.Unmarshal(value, dst); err != nil {
				return fail(exitcode.Failure, "invalid profile settings")
			}
		}
	}
	a.config.Profiles[name] = profile
	return nil
}

// prepareDaemonSettings reads and validates the startup snapshot while the
// profile database is locked. A background parent also saves explicit changes.
func (a *app) prepareDaemonSettings(ctx context.Context, cmd *cobra.Command, paths daemon.Paths, db daemon.ProfileDatabase, opts *daemon.Options, createClient bool) error {
	if err := daemon.CheckMigratedSettingsSources(ctx, db, paths.Access, a.config.Legacy); err != nil {
		var legacy *daemon.LegacySettingsError
		if errors.As(err, &legacy) {
			return fail(exitcode.Usage, "%v", legacy)
		}
		return fail(exitcode.Failure, "cannot inspect legacy settings")
	}
	stored, err := db.Settings(ctx)
	if err != nil {
		return fail(exitcode.Failure, "cannot read profile settings")
	}
	if err := a.applyDatabaseSettings(paths, stored); err != nil {
		return err
	}
	cfg, err := a.credentials(cmd, paths)
	if err != nil {
		return err
	}
	cfg.Network, err = a.networkSettings(cmd, paths, false)
	if err != nil {
		return err
	}
	cfg.DeviceModel, err = a.deviceModel(cmd, paths)
	if err != nil {
		return err
	}
	_, cfg.Throttle, err = a.daemonThrottle()
	if err != nil {
		return err
	}
	background, err := a.daemonBackgroundSync()
	if err != nil {
		return err
	}
	semantic, err := a.daemonSemanticSearch()
	if err != nil {
		return err
	}
	voice, err := a.daemonVoiceTranscription()
	if err != nil {
		return err
	}
	overrides, err := a.settingsOverrides(cmd)
	if err != nil {
		return err
	}
	// Report the values actually passed to the client after normalization.
	for name, value := range map[string]string{
		"profile.device":    cfg.DeviceModel,
		"profile.transport": string(cfg.Network.Transport),
		"profile.proxy":     cfg.Network.Proxy.Encode(),
	} {
		if override, ok := overrides[name]; ok {
			override.Value, _ = json.Marshal(value)
			overrides[name] = override
		}
	}
	changes := map[string]json.RawMessage{}
	if value, _ := cmd.Flags().GetString("client"); value != "" {
		changes["profile.client"], _ = json.Marshal(value)
	}
	if value, _ := cmd.Flags().GetString("device"); value != "" {
		changes["profile.device"], _ = json.Marshal(cfg.DeviceModel)
	}
	if cmd.Flags().Changed("transport") || cmd.Flags().Changed("proxy") || a.deps.Getenv("TG_CLI_PROXY") != "" {
		changes["profile.transport"], _ = json.Marshal(string(cfg.Network.Transport))
		changes["profile.proxy"], _ = json.Marshal(cfg.Network.Proxy.Encode())
	}
	sources, _ := cmd.Flags().GetStringArray("startup-source")
	startupSources := make(map[string]string, len(sources))
	for _, entry := range sources {
		name, source, ok := strings.Cut(entry, "=")
		if !ok {
			return fail(exitcode.Usage, "invalid startup setting source")
		}
		if _, seen := startupSources[name]; seen {
			return fail(exitcode.Usage, "invalid startup setting source")
		}
		switch name {
		case "profile.client", "profile.device", "profile.transport":
			if source != "flag" {
				return fail(exitcode.Usage, "invalid startup setting source")
			}
		case "profile.proxy":
			if source != "flag" && source != "env" {
				return fail(exitcode.Usage, "invalid startup setting source")
			}
		default:
			return fail(exitcode.Usage, "invalid startup setting source")
		}
		if _, found := stored[name]; !found {
			if _, pending := changes[name]; !pending {
				return fail(exitcode.Failure, "startup setting is unavailable")
			}
		}
		startupSources[name] = source
	}
	if len(changes) > 0 {
		if opts.Log == nil {
			journal, err := daemon.OpenJournal(paths.Log)
			if err != nil {
				return fail(exitcode.Failure, "cannot open daemon journal")
			}
			defer journal.Close()
			opts.Log = slog.New(slog.NewTextHandler(journal, nil))
		}
		if err := db.SetSettings(ctx, changes); err != nil {
			var invalid *config.Error
			if errors.As(err, &invalid) {
				return fail(exitcode.Usage, "invalid profile settings")
			}
			return fail(exitcode.Failure, "cannot save profile settings")
		}
		if opts.Log != nil {
			keys := make([]string, 0, len(changes))
			for key := range changes {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				opts.Log.Info("setting updated", "key", key, "source", "daemon_start")
			}
		}
		stored, err = db.Settings(ctx)
		if err != nil {
			return fail(exitcode.Failure, "cannot read profile settings")
		}
		if err := a.applyDatabaseSettings(paths, stored); err != nil {
			return err
		}
	}
	// The parent persists these values before launching the child; only their
	// source crosses argv, never the values or proxy credentials.
	for name, source := range startupSources {
		value, found := stored[name]
		if !found {
			return fail(exitcode.Failure, "startup setting is unavailable")
		}
		if _, present := overrides[name]; !present {
			overrides[name] = daemon.SettingOverride{Value: value, Source: source}
		}
	}
	opts.ClientPreset = a.config.Profiles[filepath.Base(paths.Dir)].Client
	if opts.ClientPreset == "" && !a.storedClient && a.deps.Getenv("TG_API_ID") == "" && a.deps.Getenv("TG_API_HASH") == "" {
		key, _ := config.LookupSetting("profile.client")
		if err := json.Unmarshal(key.Default, &opts.ClientPreset); err != nil {
			return fail(exitcode.Failure, "invalid default client preset")
		}
	}
	opts.TelegramConfig = cfg
	opts.SettingsOverrides = overrides
	opts.BackgroundSync = background
	opts.SemanticSearch = semantic
	opts.VoiceTranscription = voice
	if createClient {
		opts.Telegram, opts.NewTelegram, err = a.telegramFactory(cfg)
		if err != nil {
			return fail(exitcode.Failure, "create the Telegram client: %v", err)
		}
	}
	return nil
}
