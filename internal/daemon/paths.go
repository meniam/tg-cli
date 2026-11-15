package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// MaxSocketPath is the longest socket path accepted. Unix socket paths are
// limited by sun_path: 104 bytes on macOS and 108 on Linux, both with the
// terminating NUL, so 103 is safe everywhere.
const MaxSocketPath = 103

// DefaultProfile is the profile used when none is given.
const DefaultProfile = "default"

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Paths are the files of one profile.
type Paths struct {
	Transport string
	Proxy     string
	// Dir holds everything of the profile, mode 0700.
	Dir string
	// Socket is the JSON-RPC Unix socket, mode 0600.
	Socket string
	// Lock is held for as long as a daemon runs on the profile.
	Lock string
	// Session is the Telegram session file, mode 0600. It is a secret.
	Session string
	// Client holds the name of the official client preset chosen for the
	// profile. It is a name, not a credential.
	Client string
	// Device holds the device name the profile shows to Telegram, for example
	// in the list of devices. It is not a secret.
	Device string
	// Database is the local SQLite database, mode 0600.
	Database string
	// Access is the human-managed chat access list.
	Access string
	// Deletions records intent and outcome for each Telegram deletion.
	Deletions string
	// Log is the journal of a daemon started in the background, mode 0600.
	// Request entries contain only a registered method, duration and code.
	Log string
}

const maxLogBytes = 10 << 20 // 10 MiB

// ResolvePaths returns the paths of a profile. home is the base directory
// (the TG_CLI_HOME value); when empty, the user config directory is used.
func ResolvePaths(home, profile string) (Paths, error) {
	if !profileName.MatchString(profile) {
		return Paths{}, fmt.Errorf("invalid profile name %q: use 1-32 chars of a-z, 0-9, _ and -, starting with a letter or digit", profile)
	}
	if home == "" {
		cfg, err := os.UserConfigDir()
		if err != nil {
			return Paths{}, fmt.Errorf("find the config directory: %w", err)
		}
		home = filepath.Join(cfg, "tg-cli")
	}
	dir := filepath.Join(home, profile)
	p := Paths{
		Dir:       dir,
		Transport: filepath.Join(dir, "transport"),
		Proxy:     filepath.Join(dir, "proxy"),
		Socket:    filepath.Join(dir, "daemon.sock"),
		Lock:      filepath.Join(dir, "daemon.lock"),
		Session:   filepath.Join(dir, "session.json"),
		Client:    filepath.Join(dir, "client"),
		Device:    filepath.Join(dir, "device"),
		Database:  filepath.Join(dir, "store.db"),
		Access:    filepath.Join(dir, "access.json"),
		Deletions: filepath.Join(dir, "deletions.jsonl"),
		Log:       filepath.Join(dir, "daemon.log"),
	}
	if len(p.Socket) > MaxSocketPath {
		return Paths{}, fmt.Errorf("socket path %q is %d bytes, the limit is %d: set TG_CLI_HOME to a shorter directory", p.Socket, len(p.Socket), MaxSocketPath)
	}
	return p, nil
}
