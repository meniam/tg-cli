package config

import (
	"encoding/json"
	"fmt"
)

// SettingsForProfile converts explicitly set legacy keys to registry values.
// Empty transport and proxy use the same network defaults as daemon start.
func (l Legacy) SettingsForProfile(profile string) (map[string]json.RawMessage, error) {
	if !validProfileName(profile) {
		return nil, invalidProfileName(profile, "profile name")
	}
	result := make(map[string]json.RawMessage)
	sections := []struct {
		name   string
		values map[string]any
	}{
		{"behavior", l.Behavior}, {"background_sync", l.BackgroundSync}, {"semantic_search", l.SemanticSearch}, {"voice_transcription", l.VoiceTranscription}, {"profile", l.Profiles[profile]},
	}
	for _, section := range sections {
		for key, value := range section.values {
			name := section.name + "." + key
			text, isString := value.(string)
			if name == "profile.transport" && isString && text == "" {
				value = "tcp"
			}
			if name == "profile.proxy" && isString && text == "" {
				value = "none"
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("invalid legacy setting %s", name)
			}
			canonical, err := ParseSettingJSON(name, raw)
			if err != nil {
				return nil, fmt.Errorf("invalid legacy setting %s", name)
			}
			result[name] = canonical
		}
	}
	if err := ValidateSettings(result); err != nil {
		return nil, invalid("invalid legacy settings combination")
	}
	return result, nil
}
