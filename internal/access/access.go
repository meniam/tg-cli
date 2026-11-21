package access

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Policy is an immutable snapshot of one access file.
type Policy struct {
	allow     map[int64]struct{}
	block     map[int64]struct{}
	write     map[int64]struct{}
	admin     map[int64]struct{}
	create    bool
	profile   bool
	dialogs   bool
	sessions  bool
	files     []string
	downloads []string
	denied    bool
}

// Allowed reports whether chatID is visible under this snapshot.
func (p Policy) Allowed(chatID int64) bool {
	if p.denied {
		return false
	}
	if _, blocked := p.block[chatID]; blocked {
		return false
	}
	if len(p.allow) == 0 {
		return true
	}
	_, allowed := p.allow[chatID]
	return allowed
}

// WriteAllowed requires an explicit writable and visible chat.
func (p Policy) WriteAllowed(chatID int64) bool {
	_, listed := p.write[chatID]
	return listed && p.Allowed(chatID)
}

// AdminAllowed requires an explicitly listed and visible chat.
func (p Policy) AdminAllowed(chatID int64) bool {
	_, listed := p.admin[chatID]
	return listed && p.Allowed(chatID)
}

// CreateAllowed reports whether creating chats is enabled.
func (p Policy) CreateAllowed() bool { return !p.denied && p.create }

func (p Policy) DialogsAllowed() bool  { return !p.denied && p.dialogs }
func (p Policy) SessionsAllowed() bool { return !p.denied && p.sessions }

// ProfileAllowed reports whether own-profile changes are enabled.
func (p Policy) ProfileAllowed() bool { return !p.denied && p.profile }

// AdminCount returns the number of explicitly listed chats.
func (p Policy) AdminCount() int { return len(p.admin) }

// InviteAllowed reports whether an invitation may be inspected. An invitation
// has no known chat ID before it is resolved, so a nonempty allow list closes it.
func (p Policy) InviteAllowed() bool { return !p.denied && len(p.allow) == 0 }

// Enabled reports whether either list restricts chats or loading failed.
func (p Policy) Enabled() bool { return p.denied || len(p.allow) > 0 || len(p.block) > 0 }

// Closed reports that a policy error denied access to every chat.
func (p Policy) Closed() bool { return p.denied }

// AllowCount and BlockCount expose list sizes without exposing identifiers.
func (p Policy) AllowCount() int { return len(p.allow) }
func (p Policy) BlockCount() int { return len(p.block) }
func (p Policy) WriteCount() int { return len(p.write) }

// FileRoots returns a detached copy of the directories allowed for file sending.
func (p Policy) FileRoots() []string {
	if p.denied {
		return nil
	}
	return append([]string(nil), p.files...)
}

// FilesAllowed reports whether file sending has at least one allowed root.
func (p Policy) FilesAllowed() bool { return !p.denied && len(p.files) > 0 }

// DownloadRoots returns a detached copy of directories allowed for downloads.
func (p Policy) DownloadRoots() []string {
	if p.denied {
		return nil
	}
	return append([]string(nil), p.downloads...)
}

// DownloadsAllowed reports whether downloads have an allowed root.
func (p Policy) DownloadsAllowed() bool { return !p.denied && len(p.downloads) > 0 }

// Filter returns a detached snapshot for database queries. A failed policy
// hides all chats; callers may still show records without a chat ID.
func (p Policy) Filter() (hideAll bool, allow, block []int64) {
	if p.denied {
		return true, nil, nil
	}
	for id := range p.allow {
		allow = append(allow, id)
	}
	for id := range p.block {
		block = append(block, id)
	}
	sort.Slice(allow, func(i, j int) bool { return allow[i] < allow[j] })
	sort.Slice(block, func(i, j int) bool { return block[i] < block[j] })
	return false, allow, block
}

var denyAll = Policy{denied: true}

// Loader caches a policy while the file's modification time, size and mode
// remain unchanged. The mode is checked on every call so permission changes
// take effect even when they leave the file's contents untouched.
type Loader struct {
	mu       sync.Mutex
	path     string
	readFile func(string) ([]byte, error)
	cached   bool
	modified time.Time
	size     int64
	mode     os.FileMode
	policy   Policy
	err      error
}

// Open prepares a loader. It does not read the file until Policy is called.
func Open(path string) *Loader {
	return &Loader{path: path, readFile: os.ReadFile}
}

// Policy returns the current policy. A malformed, unreadable or unsafe file
// closes all chats and invitations until a later call observes a correction.
func (l *Loader) Policy() (Policy, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, err := os.Lstat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.cached = false
		return Policy{}, nil
	}
	if err != nil {
		return denyAll, fmt.Errorf("stat access file %q: %w", l.path, err)
	}
	if l.cached && info.ModTime().Equal(l.modified) && info.Size() == l.size && info.Mode() == l.mode {
		return l.policy, l.err
	}
	l.cached = true
	l.modified, l.size, l.mode = info.ModTime(), info.Size(), info.Mode()
	if info.Mode()&os.ModeSymlink != 0 {
		l.policy = denyAll
		l.err = fmt.Errorf("access file %q is a symbolic link", l.path)
		return l.policy, l.err
	}
	if info.Mode().Perm()&0o022 != 0 {
		l.policy = denyAll
		l.err = fmt.Errorf("access file %q is writable by group or others", l.path)
		return l.policy, l.err
	}
	data, err := l.readFile(l.path)
	if err != nil {
		l.policy = denyAll
		l.err = fmt.Errorf("read access file %q: %w", l.path, err)
		return l.policy, l.err
	}
	l.policy, err = parse(data)
	if err != nil {
		l.policy = denyAll
		l.err = fmt.Errorf("parse access file %q: %w", l.path, err)
		return l.policy, l.err
	}
	l.err = nil
	return l.policy, nil
}

func parse(data []byte) (Policy, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return Policy{}, nil
	}
	if data[0] != '{' {
		return Policy{}, errors.New("expected a JSON object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Policy{}, errors.New("invalid JSON")
	}
	var policy Policy
	for key, value := range raw {
		value = bytes.TrimSpace(value)
		switch key {
		case "allow":
			if len(value) == 0 || value[0] != '[' {
				return Policy{}, errors.New("allow must be an array of integer chat IDs")
			}
			ids, err := parseIDs(value)
			if err != nil {
				return Policy{}, fmt.Errorf("allow: %w", err)
			}
			policy.allow = ids
		case "block":
			if len(value) == 0 || value[0] != '[' {
				return Policy{}, errors.New("block must be an array of integer chat IDs")
			}
			ids, err := parseIDs(value)
			if err != nil {
				return Policy{}, fmt.Errorf("block: %w", err)
			}
			policy.block = ids
		case "write":
			if len(value) == 0 || value[0] != '[' {
				return Policy{}, errors.New("write must be an array of integer chat IDs")
			}
			ids, err := parseIDs(value)
			if err != nil {
				return Policy{}, fmt.Errorf("write: %w", err)
			}
			policy.write = ids
		case "admin":
			if len(value) == 0 || value[0] != '[' {
				return Policy{}, errors.New("admin must be an array of integer chat IDs")
			}
			ids, err := parseIDs(value)
			if err != nil {
				return Policy{}, fmt.Errorf("admin: %w", err)
			}
			policy.admin = ids
		case "create":
			if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
				return Policy{}, errors.New("create must be a boolean")
			}
			policy.create = bytes.Equal(value, []byte("true"))
		case "dialogs", "sessions":
			if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
				return Policy{}, fmt.Errorf("%s must be a boolean", key)
			}
			if key == "dialogs" {
				policy.dialogs = bytes.Equal(value, []byte("true"))
			} else {
				policy.sessions = bytes.Equal(value, []byte("true"))
			}
		case "profile":
			if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
				return Policy{}, errors.New("profile must be a boolean")
			}
			policy.profile = bytes.Equal(value, []byte("true"))
		case "files":
			if len(value) == 0 || value[0] != '[' {
				return Policy{}, errors.New("files must be an array of absolute directory paths")
			}
			roots, err := parseFileRoots(value)
			if err != nil {
				return Policy{}, fmt.Errorf("files: %w", err)
			}
			policy.files = roots
		case "downloads":
			if len(value) == 0 || value[0] != '[' {
				return Policy{}, errors.New("downloads must be an array of absolute directory paths")
			}
			roots, err := parseFileRoots(value)
			if err != nil {
				return Policy{}, fmt.Errorf("downloads: %w", err)
			}
			policy.downloads = roots
		default:
			return Policy{}, errors.New("unknown field")
		}
	}
	return policy, nil
}

func parseFileRoots(data json.RawMessage) ([]string, error) {
	var roots []string
	if err := json.Unmarshal(data, &roots); err != nil {
		return nil, errors.New("expected an array of absolute directory paths")
	}
	seen := make(map[string]struct{}, len(roots))
	for i, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			return nil, errors.New("directory paths must be absolute and nonempty")
		}
		root = filepath.Clean(root)
		if _, duplicate := seen[root]; duplicate {
			return nil, errors.New("duplicate directory path")
		}
		seen[root] = struct{}{}
		roots[i] = root
	}
	return roots, nil
}

func parseIDs(data json.RawMessage) (map[int64]struct{}, error) {
	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, errors.New("expected an array of integer chat IDs")
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, errors.New("chat ID must not be zero")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("duplicate chat ID")
		}
		seen[id] = struct{}{}
	}
	return seen, nil
}

// ValidateJSON checks an access policy with the same rules used by Loader.
func ValidateJSON(data []byte) error {
	_, err := parse(data)
	return err
}
