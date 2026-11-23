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

// rawDeleted tells whether a stored user object says the account is deleted.
// The object is plain or keyed by the method that returned it
// (`{"messages.getDialogs":{"_":"user","deleted":true}}`).
func rawDeleted(raw []byte) bool {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return false
	}
	isDeleted := func(object json.RawMessage) bool {
		var flag struct {
			Deleted bool `json:"deleted"`
		}
		return json.Unmarshal(object, &flag) == nil && flag.Deleted
	}
	if value, ok := fields["deleted"]; ok {
		return string(value) == "true"
	}
	for _, value := range fields {
		if isDeleted(value) {
			return true
		}
	}
	return false
}
