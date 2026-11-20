package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// appDir is the name of the tool directory inside a configuration directory.
const appDir = "tg-cli"

// DefaultHome returns the base directory of the profile data when neither
// `home` in the configuration nor TG_CLI_HOME sets one.
//
//   - macOS: ~/.config/tg-cli, next to the configuration file and the db
//     directory. The user config directory of macOS (~/Library/Application
//     Support) is not used.
//   - Linux and other systems: tg-cli inside os.UserConfigDir, which is
//     $XDG_CONFIG_HOME or ~/.config.
func DefaultHome() (string, error) {
	return defaultHome(runtime.GOOS, os.UserHomeDir, os.UserConfigDir)
}

// LegacyHome returns the previous default data directory, tg-cli inside
// os.UserConfigDir, when it differs from DefaultHome: on macOS it is
// ~/Library/Application Support/tg-cli. It returns an empty string when the two
// are the same (Linux) or when a directory cannot be found.
func LegacyHome() string {
	return legacyHome(runtime.GOOS, os.UserHomeDir, os.UserConfigDir)
}

func defaultHome(goos string, userHome, userConfig func() (string, error)) (string, error) {
	if goos == "darwin" {
		home, err := userHome()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		return filepath.Join(home, ".config", appDir), nil
	}
	dir, err := userConfig()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, appDir), nil
}

func legacyHome(goos string, userHome, userConfig func() (string, error)) string {
	current, err := defaultHome(goos, userHome, userConfig)
	if err != nil {
		return ""
	}
	dir, err := userConfig()
	if err != nil {
		return ""
	}
	old := filepath.Join(dir, appDir)
	if old == current {
		return ""
	}
	return old
}

// DefaultConfigDir returns the directory of the configuration file,
// ~/.config/tg-cli on every system: the path of the file does not follow
// XDG_CONFIG_HOME.
func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", appDir), nil
}
