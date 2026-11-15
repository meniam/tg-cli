// Package config loads and validates the user configuration.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"tg-cli/internal/netconf"
)

type Config struct {
	Home           string             `toml:"home"`
	DefaultProfile string             `toml:"default_profile"`
	Files          Files              `toml:"files"`
	Behavior       Behavior           `toml:"behavior"`
	Profiles       map[string]Profile `toml:"profiles"`
	// Warnings contains diagnostics that do not reveal proxy credentials.
	Warnings []string `toml:"-"`
}
type Files struct {
	Database  string `toml:"database"`
	Session   string `toml:"session"`
	Log       string `toml:"log"`
	Deletions string `toml:"deletions"`
	Access    string `toml:"access"`
}
type Behavior struct {
	ThrottlePercent float64 `toml:"throttle_percent"`
	FloodAutoWait   string  `toml:"flood_autowait"`
	ExpectUser      string  `toml:"expect_user"`
}
type Profile struct {
	Client    string `toml:"client"`
	Device    string `toml:"device"`
	Transport string `toml:"transport"`
	Proxy     string `toml:"proxy"`
}

// Error identifies an invalid configuration as a usage error.
type Error struct{ Message string }

func (e *Error) Error() string                 { return e.Message }
func (e *Error) Code() int                     { return 2 }
func invalid(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

var fileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
var clientName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Load reads path; a missing file yields the defaults. It does not create files.
func Load(path string) (Config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, fmt.Errorf("find config directory: %w", err)
	}
	c := Config{Home: filepath.Join(dir, "tg-cli"), DefaultProfile: "default", Files: Files{"store.db", "session.json", "daemon.log", "deletions.jsonl", "access.json"}, Behavior: Behavior{ThrottlePercent: 60, FloodAutoWait: "10s"}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("stat config: %w", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Config{}, invalid("config file is writable by group or others")
	}
	err = toml.NewDecoder(f).DisallowUnknownFields().Decode(&c)
	if err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) && len(strict.Errors) > 0 {
			row, _ := strict.Errors[0].Position()
			return Config{}, invalid("unknown config key at line %d", row)
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, _ := de.Position()
			return Config{}, invalid("invalid TOML at line %d", row)
		}
		return Config{}, invalid("invalid TOML configuration")
	}
	{
		if c.Home == "~" || strings.HasPrefix(c.Home, "~/") {
			home, e := os.UserHomeDir()
			if e != nil {
				return Config{}, fmt.Errorf("find home directory: %w", e)
			}
			c.Home = filepath.Join(home, strings.TrimPrefix(c.Home, "~/"))
			if c.Home == filepath.Join(home, "~") {
				c.Home = home
			}
		} else if strings.HasPrefix(c.Home, "~") {
			return Config{}, invalid("home must not use ~user")
		}
		if !filepath.IsAbs(c.Home) {
			return Config{}, invalid("home must be an absolute path")
		}
		c.Home = filepath.Clean(c.Home)
	}
	if !profileName.MatchString(c.DefaultProfile) {
		return Config{}, invalid("invalid default_profile")
	}
	if err := c.Files.validate(); err != nil {
		return Config{}, err
	}
	if math.IsNaN(c.Behavior.ThrottlePercent) || c.Behavior.ThrottlePercent < 1 || c.Behavior.ThrottlePercent > 100 {
		return Config{}, invalid("behavior.throttle_percent must be between 1 and 100")
	}
	duration, err := time.ParseDuration(c.Behavior.FloodAutoWait)
	if err != nil || duration < 0 {
		return Config{}, invalid("behavior.flood_autowait must be a non-negative duration")
	}
	for name, p := range c.Profiles {
		if !profileName.MatchString(name) {
			return Config{}, invalid("invalid profile name")
		}
		if p.Client != "" && !clientName.MatchString(p.Client) {
			return Config{}, invalid("invalid profiles client")
		}
		if p.Device != "" {
			if strings.TrimSpace(p.Device) == "" || utf8.RuneCountInString(p.Device) > 64 {
				return Config{}, invalid("invalid profiles device")
			}
			for _, r := range p.Device {
				if unicode.IsControl(r) {
					return Config{}, invalid("invalid profiles device")
				}
			}
		}
		if p.Transport != "" {
			if _, err := netconf.ParseTransport(p.Transport); err != nil {
				return Config{}, invalid("invalid profiles transport")
			}
		}
		proxy, err := netconf.ParseProxy(p.Proxy)
		if err != nil {
			return Config{}, invalid("invalid profiles proxy")
		}
		if err := (netconf.Settings{Transport: netconf.Transport(p.Transport), Proxy: proxy}).Validate(); err != nil {
			return Config{}, invalid("invalid profiles transport and proxy combination")
		}
		if info.Mode().Perm()&0o044 != 0 && proxy != nil && (proxy.Password != "" || len(proxy.Secret) > 0) {
			c.Warnings = append(c.Warnings, "config file containing proxy credentials is readable by group or others")
		}
	}
	return c, nil
}

func (f Files) validate() error {
	names := []string{f.Database, f.Session, f.Log, f.Deletions, f.Access}
	reserved := map[string]bool{"daemon.sock": true, "daemon.lock": true, ".session-start.lock": true, "moderation.jsonl": true, "account.jsonl": true, f.Database + "-wal": true, f.Database + "-shm": true, f.Log + ".1": true, f.Log + ".2": true, f.Log + ".3": true}
	seen := make(map[string]bool)
	for _, name := range names {
		if !fileName.MatchString(name) || strings.Contains(name, "..") || reserved[name] || seen[name] {
			return invalid("invalid or conflicting files name")
		}
		seen[name] = true
	}
	return nil
}
