// Package config loads and validates the user configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

// Config contains the loaded settings and any non-fatal diagnostics.
type Config struct {
	Web Web `toml:"web"`
	// Home is the base directory for profile data.
	Home string `toml:"home"`
	// HomeSet reports that the file sets home itself. Home then holds that value;
	// otherwise it holds DefaultHome.
	HomeSet bool `toml:"-"`
	// DefaultProfile is used when no profile flag is supplied.
	DefaultProfile string `toml:"default_profile"`
	// Files holds profile file names.
	Files Files `toml:"files"`
	// Behavior holds operational settings.
	Behavior Behavior `toml:"behavior"`
	// BackgroundSync holds the scheduled history load settings.
	BackgroundSync BackgroundSync `toml:"background_sync"`
	CatchupEnabled bool           `toml:"-"`
	// SemanticSearch holds the local semantic index settings.
	SemanticSearch SemanticSearch `toml:"semantic_search"`
	// VoiceTranscription holds the local voice transcription settings.
	VoiceTranscription VoiceTranscription `toml:"voice_transcription"`
	// Profiles holds settings for each named profile.
	Profiles map[string]Profile `toml:"profiles"`
	// Legacy holds the old sections separately for migration.
	Legacy Legacy `toml:"-" json:"-"`
	// Warnings contains diagnostics that do not reveal proxy credentials.
	Warnings []string `toml:"-"`
}

// Legacy records the old sections and their explicitly set values.
type Legacy struct {
	Web                map[string]any
	Behavior           map[string]any
	BackgroundSync     map[string]any
	SemanticSearch     map[string]any
	VoiceTranscription map[string]any
	Profiles           map[string]map[string]any
}

// Files holds the file names within a profile directory.
type Files struct {
	// Database is the SQLite database file name.
	Database string `toml:"database"`
	// Session is the session file name.
	Session string `toml:"session"`
	// Log is the daemon log file name.
	Log string `toml:"log"`
	// Deletions is the deletion journal file name.
	Deletions string `toml:"deletions"`
	// Access is the access policy file name.
	Access string `toml:"access"`
}

// Behavior holds settings shared by all profiles.
type Behavior struct {
	// ThrottlePercent controls request rate.
	ThrottlePercent float64 `toml:"throttle_percent"`
	// FloodAutoWait is the maximum automatic flood wait duration.
	FloodAutoWait string `toml:"flood_autowait"`
	// ExpectUser identifies the expected account.
	ExpectUser string `toml:"expect_user"`
}

// Profile holds settings for one named profile.
type Profile struct {
	// Client is the official client preset name.
	Client string `toml:"client"`
	// Device is the device name shown to Telegram.
	Device string `toml:"device"`
	// Transport is the network transport.
	Transport string `toml:"transport"`
	// Proxy is the proxy address, possibly containing credentials.
	Proxy string `toml:"proxy"`
}

// ErrConfigExists means `tg config init` found a configuration file. The command
// never replaces it: it reports the path and leaves the file as it is.
var ErrConfigExists = errors.New("the configuration file already exists")

// Error identifies an invalid configuration as a usage error.
type Error struct {
	// Message describes the invalid configuration.
	Message string
}

func (e *Error) Error() string                 { return e.Message }
func (e *Error) Code() int                     { return 2 }
func invalid(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

var fileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
var clientName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reservedProfile is never a profile: its directory next to the configuration
// file holds the databases of all profiles.
const reservedProfile = "db"

// validProfileName reports whether name may be a profile. The reserved name is
// reported separately, so that the caller can explain it.
func validProfileName(name string) bool {
	return profileName.MatchString(name) && name != reservedProfile
}

// invalidProfileName names the reason a profile name is rejected: the reserved
// word means something else than a broken name.
func invalidProfileName(name, key string) error {
	if name == reservedProfile {
		return invalid("%s: the name %s is reserved for the directory of profile databases next to the config", key, reservedProfile)
	}
	return invalid("invalid %s", key)
}

// Load reads path; a missing file yields the defaults. It does not create files.
func Load(path string) (Config, error) {
	home, err := DefaultHome()
	if err != nil {
		return Config{}, err
	}
	c := defaults(home)
	entry, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("inspect config: %w", err)
	}
	if !entry.Mode().IsRegular() {
		return Config{}, invalid("config must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("stat config: %w", err)
	}
	if !os.SameFile(entry, info) || !info.Mode().IsRegular() {
		return Config{}, invalid("config file changed during inspection")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Config{}, invalid("config file is writable by group or others")
	}
	content, err := io.ReadAll(f)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	err = toml.NewDecoder(bytes.NewReader(content)).DisallowUnknownFields().Decode(&c)
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
	// Decode explicitly set legacy values separately for migration.
	var probe struct {
		Web                map[string]any            `toml:"web"`
		Home               *string                   `toml:"home"`
		Behavior           map[string]any            `toml:"behavior"`
		BackgroundSync     map[string]any            `toml:"background_sync"`
		SemanticSearch     map[string]any            `toml:"semantic_search"`
		VoiceTranscription map[string]any            `toml:"voice_transcription"`
		Profiles           map[string]map[string]any `toml:"profiles"`
	}
	if err := toml.Unmarshal(content, &probe); err != nil {
		return Config{}, invalid("invalid legacy configuration")
	}
	c.HomeSet = probe.Home != nil
	c.Legacy = Legacy{Web: probe.Web, Behavior: probe.Behavior, BackgroundSync: probe.BackgroundSync, SemanticSearch: probe.SemanticSearch, VoiceTranscription: probe.VoiceTranscription, Profiles: probe.Profiles}
	c.Home, err = validateHome(c.Home)
	if err != nil {
		return Config{}, err
	}
	if !validProfileName(c.DefaultProfile) {
		return Config{}, invalidProfileName(c.DefaultProfile, "default_profile")
	}
	if err := c.Files.validate(); err != nil {
		return Config{}, err
	}
	if err := validateBehavior(c.Behavior); err != nil {
		return Config{}, err
	}
	if _, err := c.Web.TTL(); err != nil {
		return Config{}, err
	}
	if _, err := c.BackgroundSync.Resolve(); err != nil {
		return Config{}, err
	}
	if _, err := c.SemanticSearch.Resolve(); err != nil {
		return Config{}, err
	}
	if _, err := c.VoiceTranscription.Resolve(); err != nil {
		return Config{}, err
	}
	c.Web = defaultWeb()
	c.VoiceTranscription = defaultVoiceTranscription()
	for name, p := range c.Profiles {
		proxy, err := validateProfile(name, p)
		if err != nil {
			return Config{}, err
		}
		if info.Mode().Perm()&0o044 != 0 && proxy != nil && (proxy.Password != "" || len(proxy.Secret) > 0) {
			c.Warnings = append(c.Warnings, "config file containing proxy credentials is readable by group or others")
		}
	}
	return c, nil
}

func validateBehavior(b Behavior) error {
	if math.IsNaN(b.ThrottlePercent) || b.ThrottlePercent < 1 || b.ThrottlePercent > 100 {
		return invalid("behavior.throttle_percent must be between 1 and 100")
	}
	duration, err := time.ParseDuration(b.FloodAutoWait)
	if err != nil || duration < 0 {
		return invalid("behavior.flood_autowait must be a non-negative duration")
	}
	return nil
}

func defaults(home string) Config {
	return Config{Web: defaultWeb(), Home: home, DefaultProfile: "default", Files: Files{"store.db", "session.json", "daemon.log", "deletions.jsonl", "access.json"}, Behavior: Behavior{ThrottlePercent: 60, FloodAutoWait: "10s"}, BackgroundSync: defaultBackgroundSync(), CatchupEnabled: true, SemanticSearch: defaultSemanticSearch(), VoiceTranscription: defaultVoiceTranscription()}
}

func validateHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	} else if strings.HasPrefix(path, "~") {
		return "", invalid("home must not use ~user")
	}
	if !filepath.IsAbs(path) {
		return "", invalid("home must be an absolute path")
	}
	return filepath.Clean(path), nil
}

func validateProfile(name string, p Profile) (*netconf.Proxy, error) {
	if !validProfileName(name) {
		return nil, invalidProfileName(name, "profile name")
	}
	if p.Client != "" && !clientName.MatchString(p.Client) {
		return nil, invalid("invalid profiles client")
	}
	if p.Device != "" {
		if strings.TrimSpace(p.Device) == "" || utf8.RuneCountInString(p.Device) > 64 {
			return nil, invalid("invalid profiles device")
		}
		for _, r := range p.Device {
			if unicode.IsControl(r) {
				return nil, invalid("invalid profiles device")
			}
		}
	}
	if p.Transport != "" {
		if _, err := netconf.ParseTransport(p.Transport); err != nil {
			return nil, invalid("invalid profiles transport")
		}
	}
	proxy, err := netconf.ParseProxy(p.Proxy)
	if err != nil {
		return nil, invalid("invalid profiles proxy")
	}
	if err := (netconf.Settings{Transport: netconf.Transport(p.Transport), Proxy: proxy}).Validate(); err != nil {
		return nil, invalid("invalid profiles transport and proxy combination")
	}
	return proxy, nil
}

func (f Files) validate() error {
	names := []string{f.Database, f.Session, f.Log, f.Deletions, f.Access}
	reserved := map[string]bool{
		// The data directory of the profile.
		"daemon.sock": true, "daemon.lock": true, ".session-start.lock": true, "moderation.jsonl": true, "account.jsonl": true,
		// The database directory next to the configuration file: the record of
		// the data directory and the database lock are service files there.
		"data-dir": true, "db.lock": true,
		f.Database + "-wal": true, f.Database + "-shm": true, f.Log + ".1": true, f.Log + ".2": true, f.Log + ".3": true,
	}
	// The temporary copy of a move is .<name>.move-<pid>.tmp, and a file name
	// cannot start with a dot, so no configured name can be mistaken for one.
	seen := make(map[string]bool)
	for _, name := range names {
		if !fileName.MatchString(name) || strings.Contains(name, "..") || reserved[name] || seen[name] {
			return invalid("invalid or conflicting files name")
		}
		seen[name] = true
	}
	return nil
}
