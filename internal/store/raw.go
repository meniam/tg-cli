package store

import (
	"encoding/json"
	"strings"
)

// mergeRawKeys replaces whole method values while preserving other methods.
func mergeRawKeys(old, incoming []byte) []byte {
	if len(incoming) == 0 {
		return old
	}
	var previous, next map[string]json.RawMessage
	if json.Unmarshal(incoming, &next) != nil || next == nil {
		return incoming
	}
	if json.Unmarshal(old, &previous) != nil || previous == nil {
		return incoming
	}
	methods := false
	for key := range previous {
		if strings.Contains(key, ".") || strings.HasPrefix(key, "update") {
			methods = true
			break
		}
	}
	if !methods {
		return incoming
	}
	for key, value := range next {
		previous[key] = value
	}
	merged, err := json.Marshal(previous)
	if err != nil {
		return incoming
	}
	return merged
}

// oneRawKey returns a JSON path and value for a single method response.
func oneRawKey(raw []byte) (string, any) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != 1 {
		return "", nil
	}
	for key, value := range m {
		if strings.ContainsAny(key, `"\\`) {
			return "", nil
		}
		return `$."` + key + `"`, string(value)
	}
	return "", nil
}
