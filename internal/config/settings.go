package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"tg-cli/internal/access"
	"tg-cli/internal/netconf"
)

// SettingKey describes one profile setting. Default is canonical JSON.
type SettingKey struct {
	Name, Type        string
	Default           json.RawMessage
	Env, Flag, Apply  string
	Protected, Secret bool
	// Validate checks a canonical JSON value independently of other settings.
	Validate func(json.RawMessage) error
}

type settingSpec struct {
	name, typ, def, env, flag, apply string
	protected, secret                bool
}

var settingSpecs = []settingSpec{
	{"web.listen", "string", `""`, "TG_CLI_WEB_LISTEN", "web --listen", "live", false, false},
	{"web.open", "bool", `false`, "TG_CLI_WEB_OPEN", "web --open", "live", false, false},
	{"web.cache_bytes", "int", `33554432`, "", "", "restart", false, false},
	{"web.cache_ttl", "duration", `"168h"`, "", "", "restart", false, false},
	{"behavior.throttle_percent", "number", `60`, "TG_CLI_THROTTLE_PERCENT", "", "live", false, false},
	{"behavior.flood_autowait", "duration", `"10s"`, "TG_CLI_FLOOD_AUTOWAIT", "", "live", false, false},
	{"behavior.expect_user", "string", `""`, "TG_CLI_EXPECT_USER", "--expect-user", "live", false, false},
	{"background_sync.enabled", "bool", `true`, "TG_CLI_BACKGROUND_SYNC_ENABLED", "", "restart", false, false},
	{"background_sync.window_start", "clock", `"08:00"`, "TG_CLI_BACKGROUND_SYNC_WINDOW_START", "", "live", false, false},
	{"background_sync.window_end", "clock", `"20:00"`, "TG_CLI_BACKGROUND_SYNC_WINDOW_END", "", "live", false, false},
	{"background_sync.interval_min", "duration", `"40m"`, "TG_CLI_BACKGROUND_SYNC_INTERVAL_MIN", "", "live", false, false},
	{"background_sync.interval_max", "duration", `"3h"`, "TG_CLI_BACKGROUND_SYNC_INTERVAL_MAX", "", "live", false, false},
	{"background_sync.page_pause_min", "duration", `"2s"`, "TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MIN", "", "live", false, false},
	{"background_sync.page_pause_max", "duration", `"8s"`, "TG_CLI_BACKGROUND_SYNC_PAGE_PAUSE_MAX", "", "live", false, false},
	{"semantic_search.enabled", "bool", `true`, "TG_CLI_SEMANTIC_SEARCH_ENABLED", "", "restart", false, false},
	{"semantic_search.ollama_url", "string", `"http://127.0.0.1:11434"`, "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL", "", "restart", false, false},
	{"semantic_search.model", "string", `"bge-m3"`, "TG_CLI_SEMANTIC_SEARCH_MODEL", "", "restart", false, false},
	{"semantic_search.batch", "int", `16`, "TG_CLI_SEMANTIC_SEARCH_BATCH", "", "restart", false, false},
	{"semantic_search.interval", "duration", `"10m"`, "TG_CLI_SEMANTIC_SEARCH_INTERVAL", "", "restart", false, false},
	{"semantic_search.min_similarity", "number", `0.5`, "TG_CLI_SEMANTIC_SEARCH_MIN_SIMILARITY", "search --min-similarity", "live", false, false},
	{"voice_transcription.enabled", "bool", `true`, "TG_CLI_VOICE_TRANSCRIPTION_ENABLED", "", "restart", false, false},
	{"voice_transcription.ffmpeg", "string", `"ffmpeg"`, "TG_CLI_VOICE_TRANSCRIPTION_FFMPEG", "", "restart", false, false},
	{"voice_transcription.max_voice_mb", "int", `30`, "TG_CLI_VOICE_TRANSCRIPTION_MAX_VOICE_MB", "", "restart", false, false},
	{"voice_transcription.max_video_note_mb", "int", `50`, "TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_NOTE_MB", "", "restart", false, false},
	{"voice_transcription.max_video_mb", "int", `500`, "TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MB", "", "restart", false, false},
	{"voice_transcription.max_video_minutes", "int", `120`, "TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MINUTES", "", "restart", false, false},
	{"voice_transcription.daily_download_mb", "int", `5000`, "TG_CLI_VOICE_TRANSCRIPTION_DAILY_DOWNLOAD_MB", "", "restart", false, false},
	{"voice_transcription.daily_files", "int", `500`, "TG_CLI_VOICE_TRANSCRIPTION_DAILY_FILES", "", "restart", false, false},
	{"voice_transcription.pause", "duration", `"3s"`, "TG_CLI_VOICE_TRANSCRIPTION_PAUSE", "", "restart", false, false},
	{"voice_transcription.threads", "int", `4`, "TG_CLI_VOICE_TRANSCRIPTION_THREADS", "", "restart", false, false},
	{"profile.client", "string", `"desktop"`, "", "daemon start --client", "restart", false, false},
	{"profile.device", "string", `""`, "", "daemon start --device", "restart", false, false},
	{"profile.transport", "string", `"tcp"`, "", "daemon start --transport", "restart", false, false},
	{"profile.proxy", "string", `"none"`, "TG_CLI_PROXY", "daemon start --proxy", "restart", false, true},
	{"access.allow", "ids", `[]`, "", "", "live", true, false},
	{"access.block", "ids", `[]`, "", "", "live", true, false},
	{"access.write", "ids", `[]`, "", "", "live", true, false},
	{"access.admin", "ids", `[]`, "", "", "live", true, false},
	{"access.files", "paths", `[]`, "", "", "live", true, false},
	{"access.downloads", "paths", `[]`, "", "", "live", true, false},
	{"access.dialogs", "bool", `false`, "", "", "live", true, false},
	{"access.sessions", "bool", `false`, "", "", "live", true, false},
	{"access.create", "bool", `false`, "", "", "live", true, false},
	{"access.profile", "bool", `false`, "", "", "live", true, false},
}

// SettingKeys returns detached descriptions of all known settings.
func SettingKeys() []SettingKey {
	keys := make([]SettingKey, 0, len(settingSpecs))
	for _, s := range settingSpecs {
		name := s.name
		keys = append(keys, SettingKey{Name: name, Type: s.typ, Default: json.RawMessage(s.def), Env: s.env, Flag: s.flag, Apply: s.apply, Protected: s.protected, Secret: s.secret, Validate: func(v json.RawMessage) error { _, err := ParseSettingJSON(name, v); return err }})
	}
	return keys
}

// LookupSetting finds a setting by its complete name.
func LookupSetting(name string) (SettingKey, bool) {
	for _, key := range SettingKeys() {
		if key.Name == name {
			return key, true
		}
	}
	return SettingKey{}, false
}

// ParseSettingString parses a CLI value. Lists accept a JSON array or one item.
func ParseSettingString(name, value string) (json.RawMessage, error) {
	key, ok := LookupSetting(name)
	if !ok {
		return nil, invalid("unknown setting %q", name)
	}
	var raw []byte
	switch key.Type {
	case "string", "duration", "clock":
		raw, _ = json.Marshal(value)
	case "ids", "paths":
		if strings.HasPrefix(strings.TrimSpace(value), "[") {
			raw = []byte(value)
		} else if key.Type == "ids" {
			raw = []byte("[" + value + "]")
		} else {
			raw, _ = json.Marshal([]string{value})
		}
	default:
		raw = []byte(value)
	}
	return ParseSettingJSON(name, raw)
}

// ParseSettingJSON validates and canonicalizes one value read from the database.
func ParseSettingJSON(name string, raw json.RawMessage) (json.RawMessage, error) {
	key, ok := LookupSetting(name)
	if !ok {
		return nil, invalid("unknown setting %q", name)
	}
	fail := func() (json.RawMessage, error) { return nil, invalid("invalid value for %s", name) }
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return fail()
	}
	var value any
	switch key.Type {
	case "bool":
		var v bool
		if json.Unmarshal(raw, &v) != nil {
			return fail()
		}
		value = v
	case "number":
		var v float64
		if json.Unmarshal(raw, &v) != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return fail()
		}
		value = v
	case "int":
		var v int
		if json.Unmarshal(raw, &v) != nil {
			return fail()
		}
		value = v
	case "string", "clock", "duration":
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return fail()
		}
		value = v
	case "ids":
		if raw[0] != '[' {
			return fail()
		}
		var v []int64
		if json.Unmarshal(raw, &v) != nil {
			return fail()
		}
		slices.Sort(v)
		value = slices.Compact(v)
	case "paths":
		if raw[0] != '[' {
			return fail()
		}
		var v []string
		if json.Unmarshal(raw, &v) != nil {
			return fail()
		}
		for i := range v {
			if v[i] == "" || !filepath.IsAbs(v[i]) {
				return fail()
			}
			v[i] = filepath.Clean(v[i])
		}
		slices.Sort(v)
		value = slices.Compact(v)
	default:
		return fail()
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return fail()
	}
	if err := validateSettingValue(name, value, canonical); err != nil {
		return nil, invalid("invalid value for %s: %v", name, err)
	}
	return canonical, nil
}

func validateSettingValue(name string, value any, raw []byte) error {
	if strings.HasPrefix(name, "access.") {
		field := strings.TrimPrefix(name, "access.")
		key, _ := json.Marshal(field)
		object := append(append(append([]byte{'{'}, key...), ':'), raw...)
		object = append(object, '}')
		return access.ValidateJSON(object)
	}
	switch name {
	case "web.listen":
		_, err := ValidateWebListen(value.(string), true)
		return err
	case "web.cache_bytes", "web.cache_ttl":
		w := defaultWeb()
		if name == "web.cache_bytes" {
			w.CacheBytes = int64(value.(int))
		} else {
			w.CacheTTL = value.(string)
		}
		_, err := w.TTL()
		return err
	case "behavior.throttle_percent":
		b := defaults("").Behavior
		b.ThrottlePercent = value.(float64)
		return validateBehavior(b)
	case "behavior.flood_autowait":
		b := defaults("").Behavior
		b.FloodAutoWait = value.(string)
		return validateBehavior(b)
	case "background_sync.window_start", "background_sync.window_end":
		_, err := ParseClock(value.(string))
		return err
	case "background_sync.interval_min", "background_sync.interval_max", "background_sync.page_pause_min", "background_sync.page_pause_max":
		d, err := ParsePositiveDuration(value.(string))
		if err == nil && strings.Contains(name, "page_pause_") && d < MinBackgroundPagePause {
			return fmt.Errorf("must be at least %s", MinBackgroundPagePause)
		}
		return err
	case "semantic_search.ollama_url":
		return CheckOllamaURL(value.(string))
	case "semantic_search.model":
		return CheckSemanticModel(value.(string))
	case "semantic_search.batch":
		_, err := ParseSemanticBatch(strconv.Itoa(value.(int)))
		return err
	case "semantic_search.interval":
		_, err := ParseSemanticInterval(value.(string))
		return err
	case "semantic_search.min_similarity":
		return CheckSemanticMinSimilarity(value.(float64))
	case "voice_transcription.ffmpeg":
		return CheckVoiceFFmpeg(value.(string))
	case "voice_transcription.max_voice_mb", "voice_transcription.max_video_note_mb", "voice_transcription.max_video_mb", "voice_transcription.max_video_minutes", "voice_transcription.daily_download_mb", "voice_transcription.daily_files":
		_, err := ParseVoiceLimit(strconv.Itoa(value.(int)))
		return err
	case "voice_transcription.pause":
		_, err := ParseVoicePause(value.(string))
		return err
	case "voice_transcription.threads":
		_, err := ParseVoiceThreads(strconv.Itoa(value.(int)))
		return err
	case "profile.client":
		_, err := validateProfile("default", Profile{Client: value.(string)})
		return err
	case "profile.device":
		_, err := validateProfile("default", Profile{Device: value.(string)})
		return err
	case "profile.transport":
		_, err := netconf.ParseTransport(value.(string))
		return err
	case "profile.proxy":
		_, err := netconf.ParseProxy(value.(string))
		return err
	}
	return nil
}

// ValidateSettings checks a set of stored JSON values, filling absent keys with
// defaults before checking relationships. Unknown keys and invalid values fail.
func ValidateSettings(values map[string]json.RawMessage) error {
	all := make(map[string]json.RawMessage, len(settingSpecs))
	for _, key := range SettingKeys() {
		all[key.Name] = key.Default
	}
	for name, raw := range values {
		v, err := ParseSettingJSON(name, raw)
		if err != nil {
			return err
		}
		all[name] = v
	}
	str := func(name string) string { var v string; _ = json.Unmarshal(all[name], &v); return v }
	boolean := func(name string) bool { var v bool; _ = json.Unmarshal(all[name], &v); return v }
	b := BackgroundSync{Enabled: boolean("background_sync.enabled"), WindowStart: str("background_sync.window_start"), WindowEnd: str("background_sync.window_end"), IntervalMin: str("background_sync.interval_min"), IntervalMax: str("background_sync.interval_max"), PagePauseMin: str("background_sync.page_pause_min"), PagePauseMax: str("background_sync.page_pause_max")}
	if _, err := b.Resolve(); err != nil {
		return err
	}
	var batch int
	var similarity float64
	_ = json.Unmarshal(all["semantic_search.batch"], &batch)
	_ = json.Unmarshal(all["semantic_search.min_similarity"], &similarity)
	s := SemanticSearch{Enabled: boolean("semantic_search.enabled"), OllamaURL: str("semantic_search.ollama_url"), Model: str("semantic_search.model"), Batch: batch, Interval: str("semantic_search.interval"), MinSimilarity: similarity}
	if _, err := s.Resolve(); err != nil {
		return err
	}
	_, err := validateProfile("default", Profile{Client: str("profile.client"), Device: str("profile.device"), Transport: str("profile.transport"), Proxy: str("profile.proxy")})
	return err
}
