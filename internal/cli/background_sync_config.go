package cli

import (
	"strings"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// runDaemon starts the daemon in this process. Tests replace it to read the
// options the command built.
var runDaemon = daemon.Run

// backgroundSyncEnv names, per config key, the environment variable that
// overrides it.
var backgroundSyncEnv = struct{ enabled, windowStart, windowEnd, intervalMin, intervalMax, pagePauseMin, pagePauseMax string }{
	"TG_CLI_BACKGROUND_SYNC_ENABLED",
	"TG_CLI_BACKGROUND_SYNC_WINDOW_START",
	"TG_CLI_BACKGROUND_SYNC_WINDOW_END",
	"TG_CLI_BACKGROUND_SYNC_INTERVAL_MIN",
	"TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX",
	"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN",
	"TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MAX",
}

// daemonBackgroundSync is the only parser for the settings of the scheduled
// history load: config.toml first, then the TG_CLI_BACKGROUND_SYNC_*
// environment over it. The background parent validates the result; the
// foreground process parses the inherited environment again, as it does for
// the throttle.
func (a *app) daemonBackgroundSync() (daemon.BackgroundSyncConfig, error) {
	settings := a.config.BackgroundSync
	overridden := false
	for _, item := range []struct {
		name  string
		field *string
		check func(string) error
	}{
		{backgroundSyncEnv.windowStart, &settings.WindowStart, checkClock},
		{backgroundSyncEnv.windowEnd, &settings.WindowEnd, checkClock},
		{backgroundSyncEnv.intervalMin, &settings.IntervalMin, checkPositiveDuration},
		{backgroundSyncEnv.intervalMax, &settings.IntervalMax, checkPositiveDuration},
		{backgroundSyncEnv.pagePauseMin, &settings.PagePauseMin, checkPositiveDuration},
		{backgroundSyncEnv.pagePauseMax, &settings.PagePauseMax, checkPositiveDuration},
	} {
		value := strings.TrimSpace(a.deps.Getenv(item.name))
		if value == "" {
			continue
		}
		if err := item.check(value); err != nil {
			return daemon.BackgroundSyncConfig{}, fail(exitcode.Usage, "%s %v", item.name, err)
		}
		*item.field = value
		overridden = true
	}
	switch value := strings.TrimSpace(a.deps.Getenv(backgroundSyncEnv.enabled)); value {
	case "":
	case "true", "false":
		settings.Enabled = value == "true"
		overridden = true
	default:
		return daemon.BackgroundSyncConfig{}, fail(exitcode.Usage, "%s must be true or false", backgroundSyncEnv.enabled)
	}
	values, err := settings.Resolve()
	if err != nil {
		if overridden {
			return daemon.BackgroundSyncConfig{}, fail(exitcode.Usage, "%v (after the TG_CLI_BACKGROUND_SYNC_* overrides)", err)
		}
		return daemon.BackgroundSyncConfig{}, fail(exitcode.Usage, "%v", err)
	}
	return daemon.BackgroundSyncConfig{
		Enabled:      values.Enabled,
		WindowStart:  values.WindowStart,
		WindowEnd:    values.WindowEnd,
		IntervalMin:  values.IntervalMin,
		IntervalMax:  values.IntervalMax,
		PagePauseMin: values.PagePauseMin,
		PagePauseMax: values.PagePauseMax,
	}, nil
}

func checkClock(value string) error {
	_, err := config.ParseClock(value)
	return err
}

func checkPositiveDuration(value string) error {
	_, err := config.ParsePositiveDuration(value)
	return err
}
