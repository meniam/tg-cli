package access

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// FromSettings builds a policy from stored settings. A malformed access value
// always returns a closed policy. Other sections do not affect this policy.
func FromSettings(settings map[string]json.RawMessage) (Policy, error) {
	fields := make(map[string]json.RawMessage)
	for key, value := range settings {
		if strings.HasPrefix(key, "access.") {
			fields[strings.TrimPrefix(key, "access.")] = value
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return denyAll, errors.New("invalid access settings")
	}
	policy, err := parse(raw)
	if err != nil {
		return denyAll, errors.New("invalid access settings")
	}
	return policy, nil
}

// Denied returns a policy that denies all operations and hides all chats.
func Denied() Policy { return denyAll }

// LegacySettings validates an old access document and returns detached fields
// named for the settings registry. It never includes input values in errors.
func LegacySettings(data []byte) (map[string]json.RawMessage, error) {
	if _, err := parse(data); err != nil {
		return nil, errors.New("invalid access settings")
	}
	fields := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(data)) == 0 {
		return fields, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, errors.New("invalid access settings")
	}
	for key, value := range raw {
		fields["access."+key] = bytes.Clone(value)
	}
	return fields, nil
}
