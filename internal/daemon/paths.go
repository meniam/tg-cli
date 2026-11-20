package daemon

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"tg-cli/internal/config"
)

// MaxSocketPath is the longest socket path accepted. Unix socket paths are
// limited by sun_path: 104 bytes on macOS and 108 on Linux, both with the
// terminating NUL, so 103 is safe everywhere.
const MaxSocketPath = 103

// DefaultProfile is the profile used when none is given.
const DefaultProfile = "default"

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ReservedProfile is never a profile: its directory next to the configuration
// file holds the databases of all profiles.
const ReservedProfile = "db"

// FileNames contains configurable profile file names.
type FileNames struct {
	Database, Session, Log, Deletions, Access string
}

// DefaultFileNames returns the original profile file names.
func DefaultFileNames() FileNames {
	return FileNames{Database: "store.db", Session: "session.json", Log: "daemon.log", Deletions: "deletions.jsonl", Access: "access.json"}
}

// Paths are the files of one profile.
type Paths struct {
	// Dir holds everything of the profile, mode 0700.
	Dir string
	// Socket is the JSON-RPC Unix socket, mode 0600.
	Socket string
	// Lock is held for as long as a daemon runs on the profile.
	Lock string
	// Session is the Telegram session file, mode 0600. It is a secret.
	Session string
	// DatabaseDir holds the profile database and its journals, mode 0700. It
	// sits next to the configuration and not in Dir, so that a change of home
	// or TG_CLI_HOME does not move the history.
	DatabaseDir string
	// Database is the local SQLite database, mode 0600.
	Database string
	// LegacyDatabase is where the database lived before it moved next to the
	// configuration. The daemon moves it on start and leaves no file there.
	LegacyDatabase string
	// Access is the human-managed chat access list.
	Access string
	// Deletions records intent and outcome for each Telegram deletion.
	Deletions string
	// Log is the journal of a daemon started in the background, mode 0600.
	// Request entries contain only a registered method, duration and code.
	Log string
}

const maxLogBytes = 10 << 20 // 10 MiB

// ResolvePaths returns the paths of a profile. home is the base directory of
// the profile data (the TG_CLI_HOME value); when empty, config.DefaultHome is
// used. dbRoot is the base directory of the profile databases, the
// db directory next to the configuration file; it is required.
func ResolvePaths(home, dbRoot, profile string, files FileNames) (Paths, error) {
	if !profileName.MatchString(profile) {
		return Paths{}, fmt.Errorf("invalid profile name %q: use 1-32 chars of a-z, 0-9, _ and -, starting with a letter or digit", profile)
	}
	if profile == ReservedProfile {
		return Paths{}, fmt.Errorf("profile name %q is reserved: the %s directory next to the configuration file holds the profile databases", profile, ReservedProfile)
	}
	if dbRoot == "" {
		return Paths{}, fmt.Errorf("the database directory is empty: it is the db directory next to the configuration file")
	}
	if home == "" {
		var err error
		if home, err = config.DefaultHome(); err != nil {
			return Paths{}, err
		}
	}
	dir := filepath.Join(home, profile)
	dbDir := filepath.Join(dbRoot, profile)
	p := Paths{
		Dir:            dir,
		Socket:         filepath.Join(dir, "daemon.sock"),
		Lock:           filepath.Join(dir, "daemon.lock"),
		Session:        filepath.Join(dir, files.Session),
		DatabaseDir:    dbDir,
		Database:       filepath.Join(dbDir, files.Database),
		LegacyDatabase: filepath.Join(dir, files.Database),
		Access:         filepath.Join(dir, files.Access),
		Deletions:      filepath.Join(dir, files.Deletions),
		Log:            filepath.Join(dir, files.Log),
	}
	if len(p.Socket) > MaxSocketPath {
		return Paths{}, fmt.Errorf("socket path %q is %d bytes, the limit is %d: set home in config.toml or TG_CLI_HOME to a shorter directory", p.Socket, len(p.Socket), MaxSocketPath)
	}
	return p, nil
}

// accessMessage substitutes the configured access file name in diagnostics.
func (d *daemon) accessMessage(message string) string {
	name := filepath.Base(d.opts.Paths.Access)
	if name == "." || name == "" {
		name = "access.json"
	}
	return strings.ReplaceAll(message, "access.json", name)
}
