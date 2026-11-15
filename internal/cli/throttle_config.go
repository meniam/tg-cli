package cli

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/throttle"
)

var throttlePercentNumber = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// daemonThrottle is the only parser for the daemon's throttle environment.
// The background parent validates it; the foreground process creates the
// limiter again from its inherited environment before creating Telegram.
func (a *app) daemonThrottle() (throttle.Config, *throttle.Limiter, error) {
	cfg := throttle.DefaultConfig()
	if a.config.Behavior.ThrottlePercent != 0 {
		cfg.Percent = a.config.Behavior.ThrottlePercent
	}
	if a.config.Behavior.FloodAutoWait != "" {
		wait, err := time.ParseDuration(a.config.Behavior.FloodAutoWait)
		if err != nil {
			return cfg, nil, fail(exitcode.Usage, "invalid behavior.flood_autowait")
		}
		cfg.FloodAutoWait = wait
	}
	if value := strings.TrimSpace(a.deps.Getenv("TG_CLI_THROTTLE_PERCENT")); value != "" {
		if !throttlePercentNumber.MatchString(value) {
			return cfg, nil, fail(exitcode.Usage, "TG_CLI_THROTTLE_PERCENT must be a decimal number from 1 to 100")
		}
		percent, err := strconv.ParseFloat(value, 64)
		if err != nil || percent < 1 || percent > 100 {
			return cfg, nil, fail(exitcode.Usage, "TG_CLI_THROTTLE_PERCENT must be a decimal number from 1 to 100")
		}
		cfg.Percent = percent
	}
	if value := strings.TrimSpace(a.deps.Getenv("TG_CLI_FLOOD_AUTOWAIT")); value != "" {
		wait, err := time.ParseDuration(value)
		if err != nil || wait < 0 {
			return cfg, nil, fail(exitcode.Usage, "TG_CLI_FLOOD_AUTOWAIT must be a non-negative Go duration, such as 10s or 0")
		}
		cfg.FloodAutoWait = wait
	}
	limiter, err := throttle.New(cfg, nil)
	if err != nil {
		return cfg, nil, fail(exitcode.Usage, "invalid throttle configuration")
	}
	return cfg, limiter, nil
}
