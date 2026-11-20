package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// BackgroundSync holds the settings of the scheduled history load. The values
// are kept as the owner wrote them; Resolve checks and converts them.
type BackgroundSync struct {
	// Enabled turns the scheduler on. Off by default: the daemon then makes no
	// new request to Telegram.
	Enabled bool `toml:"enabled"`
	// WindowStart is the local time of day, "HH:MM", when loading may begin.
	WindowStart string `toml:"window_start"`
	// WindowEnd is the local time of day, "HH:MM", when a running load stops.
	WindowEnd string `toml:"window_end"`
	// IntervalMin and IntervalMax bound the random wait between two loads.
	IntervalMin string `toml:"interval_min"`
	IntervalMax string `toml:"interval_max"`
	// PagePauseMin and PagePauseMax bound the random pause between two pages
	// of one scheduled load.
	PagePauseMin string `toml:"page_pause_min"`
	PagePauseMax string `toml:"page_pause_max"`
}

// BackgroundSyncValues is a checked BackgroundSync. The window is the offset
// of each boundary from local midnight.
type BackgroundSyncValues struct {
	Enabled                                          bool
	WindowStart, WindowEnd, IntervalMin, IntervalMax time.Duration
	PagePauseMin, PagePauseMax                       time.Duration
}

// MinBackgroundPagePause is the shortest pause between pages of a scheduled
// load: the manual load already pauses 2 seconds, and a scheduled one is slower.
const MinBackgroundPagePause = time.Second

func defaultBackgroundSync() BackgroundSync {
	return BackgroundSync{
		WindowStart: "08:00", WindowEnd: "20:00",
		IntervalMin: "40m", IntervalMax: "3h",
		PagePauseMin: "2s", PagePauseMax: "8s",
	}
}

// ParseClock reads "HH:MM" on a 24-hour clock into an offset from midnight.
// Exactly two digits per part: "8:00" and "24:00" are rejected.
func ParseClock(value string) (time.Duration, error) {
	hours, minutes, found := strings.Cut(value, ":")
	if !found || len(hours) != 2 || len(minutes) != 2 {
		return 0, fmt.Errorf("must be a time of day as HH:MM, 00:00 to 23:59")
	}
	h, errH := strconv.Atoi(hours)
	m, errM := strconv.Atoi(minutes)
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 || hours[0] == '+' || minutes[0] == '+' {
		return 0, fmt.Errorf("must be a time of day as HH:MM, 00:00 to 23:59")
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}

// ParsePositiveDuration reads a Go duration that must be above zero.
func ParsePositiveDuration(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("must be a positive duration such as 40m or 3h")
	}
	return d, nil
}

// Resolve checks every setting and the order of each pair, and returns the
// converted values. An error names the config key; it is a usage error.
func (b BackgroundSync) Resolve() (BackgroundSyncValues, error) {
	v := BackgroundSyncValues{Enabled: b.Enabled}
	var err error
	for _, item := range []struct {
		key   string
		value string
		out   *time.Duration
		clock bool
	}{
		{"window_start", b.WindowStart, &v.WindowStart, true},
		{"window_end", b.WindowEnd, &v.WindowEnd, true},
		{"interval_min", b.IntervalMin, &v.IntervalMin, false},
		{"interval_max", b.IntervalMax, &v.IntervalMax, false},
		{"page_pause_min", b.PagePauseMin, &v.PagePauseMin, false},
		{"page_pause_max", b.PagePauseMax, &v.PagePauseMax, false},
	} {
		if item.clock {
			*item.out, err = ParseClock(item.value)
		} else {
			*item.out, err = ParsePositiveDuration(item.value)
		}
		if err != nil {
			return BackgroundSyncValues{}, invalid("background_sync.%s %v", item.key, err)
		}
	}
	if v.WindowStart >= v.WindowEnd {
		return BackgroundSyncValues{}, invalid("background_sync.window_start must be earlier than background_sync.window_end")
	}
	if v.IntervalMin > v.IntervalMax {
		return BackgroundSyncValues{}, invalid("background_sync.interval_min must not exceed background_sync.interval_max")
	}
	if v.PagePauseMin < MinBackgroundPagePause {
		return BackgroundSyncValues{}, invalid("background_sync.page_pause_min must be at least %s", MinBackgroundPagePause)
	}
	if v.PagePauseMin > v.PagePauseMax {
		return BackgroundSyncValues{}, invalid("background_sync.page_pause_min must not exceed background_sync.page_pause_max")
	}
	return v, nil
}
