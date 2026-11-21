package config

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// LockedLegacyConfig is valid only during WithLegacyConfigLock.
type LockedLegacyConfig struct {
	Config  Config
	Content []byte
	Exists  bool
	path    string
}

// WithLegacyConfigLock holds the configuration lock for the complete operation.
func WithLegacyConfigLock(path string, action func(*LockedLegacyConfig) error) error {
	return withConfigLock(path, func() error {
		read := func() ([]byte, bool, error) {
			entry, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			if err != nil || !entry.Mode().IsRegular() {
				return nil, false, invalid("config must be a regular file")
			}
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			if err != nil {
				return nil, false, invalid("cannot read config file")
			}
			return data, true, nil
		}
		before, existsBefore, err := read()
		if err != nil {
			return err
		}
		cfg, err := Load(path)
		if err != nil {
			return err
		}
		after, existsAfter, err := read()
		if err != nil {
			return err
		}
		if existsBefore != existsAfter || !bytes.Equal(before, after) {
			return invalid("config file changed during inspection")
		}
		return action(&LockedLegacyConfig{Config: cfg, Content: bytes.Clone(before), Exists: existsBefore, path: path})
	})
}

// CheckUnchanged checks an unmodified configuration before finishing migration.
func (c *LockedLegacyConfig) CheckUnchanged() error {
	entry, err := os.Lstat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		if c.Exists {
			return invalid("config file changed during migration")
		}
		return nil
	}
	if err != nil || !entry.Mode().IsRegular() || !c.Exists {
		return invalid("config file changed during migration")
	}
	f, err := os.Open(c.path)
	if err != nil {
		return invalid("config file changed during migration")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(entry, info) {
		return invalid("config file changed during migration")
	}
	current, readErr := io.ReadAll(f)
	after, statErr := os.Lstat(c.path)
	if statErr != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) {
		return invalid("config file changed during migration")
	}
	if errors.Is(readErr, os.ErrNotExist) {
		if c.Exists {
			return invalid("config file changed during migration")
		}
		current = nil
	} else if readErr != nil || !c.Exists || !bytes.Equal(current, c.Content) {
		return invalid("config file changed during migration")
	}
	return nil
}

// StripLegacy removes the old settings expressions from a TOML document.
func StripLegacy(data []byte) ([]byte, error) {
	type span struct{ start, end int }
	var spans []span
	var parser unstable.Parser
	parser.Reset(data)
	var current []string
	for parser.NextExpression() {
		node := parser.Expression()
		var start, end int
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			current = nodeKeys(node)
			if len(current) == 0 || !legacySection(current[0]) {
				continue
			}
			it := node.Key()
			if !it.Next() {
				return nil, invalid("invalid legacy section")
			}
			offset := int(it.Node().Raw.Offset)
			start = bytes.LastIndexByte(data[:offset], '\n') + 1
			end = lineEnd(data, offset)
		case unstable.KeyValue:
			keys := nodeKeys(node)
			isLegacy := len(current) > 0 && legacySection(current[0])
			if len(current) == 0 && len(keys) > 0 {
				isLegacy = legacySection(keys[0])
			}
			if !isLegacy {
				continue
			}
			offset := int(node.Raw.Offset)
			stop := int(node.Raw.Offset + node.Raw.Length)
			if offset < 0 || stop > len(data) || stop < offset {
				return nil, invalid("invalid legacy configuration")
			}
			start = bytes.LastIndexByte(data[:offset], '\n') + 1
			end = lineEnd(data, stop)
		default:
			continue
		}
		if start < 0 || end < start || end > len(data) {
			return nil, invalid("invalid legacy configuration")
		}
		spans = append(spans, span{start, end})
	}
	if parser.Error() != nil {
		return nil, invalid("invalid TOML configuration")
	}
	output := make([]byte, 0, len(data))
	last := 0
	for _, item := range spans {
		if item.start < last {
			return nil, invalid("invalid legacy configuration")
		}
		output = append(output, data[last:item.start]...)
		last = item.end
	}
	output = append(output, data[last:]...)
	var probe struct {
		Web                map[string]any            `toml:"web"`
		Behavior           map[string]any            `toml:"behavior"`
		BackgroundSync     map[string]any            `toml:"background_sync"`
		SemanticSearch     map[string]any            `toml:"semantic_search"`
		VoiceTranscription map[string]any            `toml:"voice_transcription"`
		Profiles           map[string]map[string]any `toml:"profiles"`
	}
	if err := toml.Unmarshal(output, &probe); err != nil || probe.Web != nil || probe.Behavior != nil || probe.BackgroundSync != nil || probe.SemanticSearch != nil || probe.VoiceTranscription != nil || probe.Profiles != nil {
		return nil, invalid("cannot remove legacy sections")
	}
	return output, nil
}
func legacySection(name string) bool {
	return name == "web" || name == "behavior" || name == "background_sync" || name == "semantic_search" || name == "voice_transcription" || name == "profiles"
}
