package rpc

import "encoding/json"

const (
	MethodSettingsList        = "settings.list"
	MethodSettingsGet         = "settings.get"
	MethodSettingsSet         = "settings.set"
	MethodSettingsUnset       = "settings.unset"
	MethodSettingsAdd         = "settings.add"
	MethodSettingsRemove      = "settings.remove"
	MethodSettingsTokenInit   = "settings.token_init"
	MethodSettingsTokenChange = "settings.token_change"
)

// SettingValue describes the effective and stored values of a setting.
type SettingValue struct {
	Key             string          `json:"key"`
	Value           json.RawMessage `json:"value"`
	Source          string          `json:"source"`
	Stored          json.RawMessage `json:"stored"`
	OverriddenBy    string          `json:"overridden_by,omitempty"`
	RestartRequired bool            `json:"restart_required"`
}

type SettingsListParams struct {
	Prefix string `json:"prefix,omitempty"`
}
type SettingsListResult struct {
	Settings []SettingValue `json:"settings"`
}
type SettingsGetParams struct {
	Key        string `json:"key"`
	Passphrase string `json:"passphrase,omitempty"`
}
type SettingsSetParams struct {
	Key        string                     `json:"key,omitempty"`
	Value      json.RawMessage            `json:"value,omitempty"`
	Changes    map[string]json.RawMessage `json:"changes,omitempty"`
	Passphrase string                     `json:"passphrase,omitempty"`
}
type SettingsUnsetParams struct {
	Key        string `json:"key"`
	Passphrase string `json:"passphrase,omitempty"`
}
type SettingsListChangeParams struct {
	Key        string            `json:"key"`
	Values     []json.RawMessage `json:"values"`
	Passphrase string            `json:"passphrase,omitempty"`
}
type SettingsWriteResult struct {
	SettingValue
	Previous json.RawMessage `json:"previous"`
	Applied  string          `json:"applied"`
}
type SettingsTokenInitParams struct {
	Passphrase string `json:"passphrase"`
}
type SettingsTokenChangeParams struct {
	OldPassphrase string `json:"old_passphrase"`
	NewPassphrase string `json:"new_passphrase"`
}
type SettingsTokenResult struct {
	Updated bool `json:"updated"`
}

// SettingsBatchWriteResult describes an atomic multi-key update.
type SettingsBatchWriteResult struct {
	Settings []SettingsWriteResult `json:"settings"`
}
