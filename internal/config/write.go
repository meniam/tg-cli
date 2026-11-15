package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// UpdateProfile atomically updates profile keys while preserving unrelated text.
// When onlyMissing is true, existing keys are not changed.
func UpdateProfile(path, name string, changes map[string]string, onlyMissing bool) (Config, error) {
	return updateProfile(path, name, changes, onlyMissing, nil)
}

// UpdateProfileWith runs action under the config lock, rolling back an update if it fails.
func UpdateProfileWith(path, name string, changes map[string]string, onlyMissing bool, action func() error) (Config, error) {
	return updateProfile(path, name, changes, onlyMissing, action)
}

func updateProfile(path, name string, changes map[string]string, onlyMissing bool, action func() error) (Config, error) {
	if !profileName.MatchString(name) {
		return Config{}, invalid("invalid profile name")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create config directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "config.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Config{}, fmt.Errorf("open config lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Config{}, fmt.Errorf("lock config: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	p := cfg.Profiles[name]
	for _, key := range keys {
		value := changes[key]
		var old string
		switch key {
		case "client":
			old = p.Client
		case "device":
			old = p.Device
		case "transport":
			old = p.Transport
		case "proxy":
			old = p.Proxy
		default:
			return Config{}, invalid("unknown profile key")
		}
		if onlyMissing && hasProfileKey(data, name, key) {
			if _, err := editProfile(data, name, key, value, true); err != nil {
				return Config{}, err
			}
			continue
		}
		if old == value {
			if profilePresent(cfg.Profiles, name) {
				if _, err := editProfile(data, name, key, value, true); err != nil {
					return Config{}, err
				}
			}
			continue
		}
		data, err = editProfile(data, name, key, value, profilePresent(cfg.Profiles, name))
		if err != nil {
			return Config{}, err
		}
		switch key {
		case "client":
			p.Client = value
		case "device":
			p.Device = value
		case "transport":
			p.Transport = value
		case "proxy":
			p.Proxy = value
		}
		if cfg.Profiles == nil {
			cfg.Profiles = make(map[string]Profile)
		}
		cfg.Profiles[name] = p
	}
	var decoded Config
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&decoded); err != nil {
		return Config{}, invalid("profile must use a separate table")
	}
	if _, err = validateProfile(name, p); err != nil {
		return Config{}, err
	}
	original, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if missing {
		original = nil
	} else if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	changed := !bytes.Equal(original, data)
	if changed {
		if err := writeAtomic(path, data); err != nil {
			return Config{}, err
		}
	}
	if action != nil {
		if err := action(); err != nil {
			if changed {
				var rollback error
				if missing {
					rollback = os.Remove(path)
				} else {
					rollback = writeAtomic(path, original)
				}
				if rollback != nil {
					return Config{}, errors.Join(err, fmt.Errorf("restore config: %w", rollback))
				}
			}
			return Config{}, err
		}
	}
	return cfg, nil
}
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create config: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func profilePresent(profiles map[string]Profile, name string) bool {
	_, ok := profiles[name]
	return ok
}

func editProfile(data []byte, profile, key, value string, exists bool) ([]byte, error) {
	var parser unstable.Parser
	parser.Reset(data)
	inTarget, found := false, false
	insert := -1
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			parts := nodeKeys(node)
			inTarget = len(parts) == 2 && parts[0] == "profiles" && parts[1] == profile && node.Kind == unstable.Table
			if inTarget {
				found = true
				it := node.Key()
				it.Next()
				offset := int(it.Node().Raw.Offset)
				insert = lineEnd(data, offset)
			}
			continue
		}
		if node.Kind != unstable.KeyValue || !inTarget {
			continue
		}
		keys := nodeKeys(node)
		if len(keys) != 1 {
			return nil, invalid("profile must use a separate table with simple keys")
		}
		r := node.Value().Raw
		start, end := int(r.Offset), int(r.Offset+r.Length)
		insert = lineEnd(data, end)
		if keys[0] == key {
			result := make([]byte, 0, len(data)+len(value)+2)
			result = append(result, data[:start]...)
			result = strconv.AppendQuote(result, value)
			result = append(result, data[end:]...)
			return result, nil
		}
	}
	if parser.Error() != nil {
		return nil, invalid("invalid TOML configuration")
	}
	if !found && exists {
		return nil, invalid("profile must use a separate table")
	}
	line := key + " = " + strconv.Quote(value) + "\n"
	if found {
		prefix := ""
		if insert > 0 && data[insert-1] != '\n' {
			prefix = "\n"
		}
		out := append([]byte(nil), data[:insert]...)
		out = append(out, []byte(prefix+line)...)
		return append(out, data[insert:]...), nil
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n\n") {
		prefix += "\n"
	}
	return append(append([]byte(nil), data...), []byte(prefix+"[profiles."+profile+"]\n"+line)...), nil
}
func nodeKeys(n *unstable.Node) []string {
	var keys []string
	it := n.Key()
	for it.Next() {
		keys = append(keys, string(it.Node().Data))
	}
	return keys
}
func lineEnd(data []byte, offset int) int {
	if i := bytes.IndexByte(data[offset:], '\n'); i >= 0 {
		return offset + i + 1
	}
	return len(data)
}

func hasProfileKey(data []byte, profile, key string) bool {
	var parser unstable.Parser
	parser.Reset(data)
	current := []string(nil)
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			current = nodeKeys(node)
			continue
		}
		if node.Kind != unstable.KeyValue {
			continue
		}
		full := append(append([]string(nil), current...), nodeKeys(node)...)
		if len(full) == 3 && full[0] == "profiles" && full[1] == profile && full[2] == key {
			return true
		}
	}
	return false
}
