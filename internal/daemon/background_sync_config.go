package daemon

import "time"

// BackgroundSyncConfig holds the settings of the scheduled history load. The
// caller checks them (see config.BackgroundSync); the scheduler trusts them.
// Without Enabled the daemon makes no new Telegram request for this feature.
type BackgroundSyncConfig struct {
	// Enabled turns the scheduler on.
	Enabled bool
	// WindowStart and WindowEnd are offsets from local midnight: loading runs
	// from WindowStart until WindowEnd.
	WindowStart, WindowEnd time.Duration
	// IntervalMin and IntervalMax bound the random wait between two runs.
	IntervalMin, IntervalMax time.Duration
	// PagePauseMin and PagePauseMax bound the random pause between two pages
	// of a scheduled load.
	PagePauseMin, PagePauseMax time.Duration
}
