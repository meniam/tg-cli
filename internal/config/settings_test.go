package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSettingDefaults(t *testing.T) {
	c := defaults("/tmp")
	expected := map[string]any{
		"behavior.throttle_percent": c.Behavior.ThrottlePercent, "behavior.flood_autowait": c.Behavior.FloodAutoWait, "behavior.expect_user": c.Behavior.ExpectUser,
		"background_sync.enabled": c.BackgroundSync.Enabled, "background_sync.window_start": c.BackgroundSync.WindowStart, "background_sync.window_end": c.BackgroundSync.WindowEnd, "background_sync.interval_min": c.BackgroundSync.IntervalMin, "background_sync.interval_max": c.BackgroundSync.IntervalMax, "background_sync.page_pause_min": c.BackgroundSync.PagePauseMin, "background_sync.page_pause_max": c.BackgroundSync.PagePauseMax,
		"semantic_search.enabled": c.SemanticSearch.Enabled, "semantic_search.ollama_url": c.SemanticSearch.OllamaURL, "semantic_search.model": c.SemanticSearch.Model, "semantic_search.batch": c.SemanticSearch.Batch, "semantic_search.interval": c.SemanticSearch.Interval, "semantic_search.min_similarity": c.SemanticSearch.MinSimilarity,
		"profile.client": "", "profile.device": "", "profile.transport": "tcp", "profile.proxy": "none",
		"access.allow": []int64{}, "access.block": []int64{}, "access.write": []int64{}, "access.admin": []int64{}, "access.files": []string{}, "access.downloads": []string{}, "access.dialogs": false, "access.sessions": false, "access.create": false, "access.profile": false,
	}
	keys := SettingKeys()
	if len(keys) != len(expected) {
		t.Fatalf("got %d keys, want %d", len(keys), len(expected))
	}
	for _, key := range keys {
		t.Run(key.Name, func(t *testing.T) {
			want, ok := expected[key.Name]
			if !ok {
				t.Fatal("unexpected key")
			}
			delete(expected, key.Name)
			raw, _ := json.Marshal(want)
			if string(key.Default) != string(raw) {
				t.Fatalf("default %s, want %s", key.Default, raw)
			}
			if err := key.Validate(key.Default); err != nil {
				t.Fatal(err)
			}
			if key.Protected != strings.HasPrefix(key.Name, "access.") || key.Secret != (key.Name == "profile.proxy") {
				t.Fatal("incorrect protection")
			}
			if key.Apply != "live" && key.Apply != "restart" {
				t.Fatal("invalid application mode")
			}
		})
	}
	if err := ValidateSettings(nil); err != nil {
		t.Fatal(err)
	}
	// The template is loaded as TOML with the same defaulting as a real file.
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(Template()), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Behavior, c.Behavior) || !reflect.DeepEqual(loaded.BackgroundSync, c.BackgroundSync) || !reflect.DeepEqual(loaded.SemanticSearch, c.SemanticSearch) {
		t.Fatal("template and defaults differ")
	}
}

func TestSettingValues(t *testing.T) {
	tests := []struct {
		name, input, want string
		bad               []string
	}{
		{"behavior.throttle_percent", "1.5", "1.5", []string{"0", "101", "NaN", "\"10\""}},
		{"behavior.flood_autowait", "0s", `"0s"`, []string{"-1s", "bad"}},
		{"behavior.expect_user", "@alice", `"@alice"`, nil},
		{"background_sync.enabled", "true", "true", []string{"1", "TRUE"}},
		{"background_sync.window_start", "00:00", `"00:00"`, []string{"8:00", "24:00", "-1:00"}},
		{"background_sync.window_end", "23:59", `"23:59"`, []string{"23:60"}},
		{"background_sync.interval_min", "1s", `"1s"`, []string{"0s", "bad"}},
		{"background_sync.interval_max", "2s", `"2s"`, []string{"-1s"}},
		{"background_sync.page_pause_min", "1s", `"1s"`, []string{"0s", "500ms"}},
		{"background_sync.page_pause_max", "2s", `"2s"`, []string{"0s", "500ms"}},
		{"semantic_search.enabled", "false", "false", []string{"yes"}},
		{"semantic_search.ollama_url", "http://localhost:11434", `"http://localhost:11434"`, []string{"http://example.com", "http://localhost/path"}},
		{"semantic_search.model", "model", `"model"`, []string{"", "  ", "a\nb", strings.Repeat("x", 129)}},
		{"semantic_search.batch", "128", "128", []string{"0", "129", "1.2"}},
		{"semantic_search.interval", "1m", `"1m"`, []string{"59s", "25h", "bad"}},
		{"semantic_search.min_similarity", "0", "0", []string{"-0.1", "1.1", "NaN"}},
		{"profile.client", "desktop", `"desktop"`, []string{"UPPER", "bad_name"}},
		{"profile.device", "device", `"device"`, []string{"  ", "x\ny", strings.Repeat("x", 65)}},
		{"profile.transport", "wss", `"wss"`, []string{"udp", ""}},
		{"profile.proxy", "none", `"none"`, []string{"bad", "socks5://host"}},
		{"access.allow", "[3,-1,3]", "[-1,3]", []string{"[0]", "[1.5]", "[null]"}},
		{"access.block", "1", "[1]", []string{"0", "[\"1\"]"}},
		{"access.write", "-123", "[-123]", []string{"0"}},
		{"access.admin", "[]", "[]", []string{"[0]"}},
		{"access.files", `["/z/../a","/a","/b"]`, `["/a","/b"]`, []string{"relative", "[\"\"]", "[null]"}},
		{"access.downloads", "/tmp", `["/tmp"]`, []string{"relative"}},
		{"access.dialogs", "true", "true", []string{"yes"}}, {"access.sessions", "false", "false", []string{"0"}},
		{"access.create", "true", "true", []string{"null"}}, {"access.profile", "false", "false", []string{"[]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSettingString(tt.name, tt.input)
			if err != nil || string(got) != tt.want {
				t.Fatalf("got %s, %v; want %s", got, err, tt.want)
			}
			for _, v := range tt.bad {
				if _, err := ParseSettingString(tt.name, v); err == nil {
					t.Errorf("accepted %q", v)
				}
			}
			for _, raw := range []string{"null", "{}", "[] true"} {
				if _, err := ParseSettingJSON(tt.name, json.RawMessage(raw)); err == nil {
					t.Errorf("accepted JSON %s", raw)
				}
			}
		})
	}
	if len(tests) != len(SettingKeys()) {
		t.Fatal("missing key coverage")
	}
	if _, err := ParseSettingJSON("unknown", []byte("true")); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestValidateSettingPairs(t *testing.T) {
	cases := []struct {
		values map[string]string
		valid  bool
	}{
		{map[string]string{"background_sync.window_start": `"20:00"`}, false},
		{map[string]string{"background_sync.window_start": `"21:00"`, "background_sync.window_end": `"22:00"`}, true},
		{map[string]string{"background_sync.interval_min": `"4h"`}, false},
		{map[string]string{"background_sync.interval_min": `"4h"`, "background_sync.interval_max": `"4h"`}, true},
		{map[string]string{"background_sync.page_pause_min": `"500ms"`}, false},
		{map[string]string{"background_sync.page_pause_max": `"1s"`}, false},
		{map[string]string{"background_sync.page_pause_min": `"1s"`, "background_sync.page_pause_max": `"1s"`}, true},
		{map[string]string{"profile.proxy": `"http://localhost:8080"`}, false},
		{map[string]string{"profile.transport": `"wss"`, "profile.proxy": `"http://localhost:8080"`}, true},
		{map[string]string{"profile.transport": `"auto"`, "profile.proxy": `"tg://proxy?server=localhost&port=443&secret=00000000000000000000000000000000"`}, false},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			values := map[string]json.RawMessage{}
			for k, v := range tc.values {
				values[k] = json.RawMessage(v)
			}
			err := ValidateSettings(values)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestTemplateDocumentedSettingDefaults(t *testing.T) {
	section := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(Template(), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		switch line {
		case "[behavior]", "[background_sync]", "[semantic_search]":
			section = strings.Trim(line, "[]")
			continue
		case "[profiles.default]":
			section = "profile"
			continue
		}
		name, value, ok := strings.Cut(line, " = ")
		if !ok || strings.ContainsAny(name, " \t") {
			continue
		}
		full := section + "." + name
		// These examples intentionally demonstrate non-default values.
		switch full {
		case "behavior.expect_user", "profile.client", "profile.device", "profile.proxy":
			continue
		}
		key, ok := LookupSetting(full)
		if !ok {
			continue
		}
		parsed, err := ParseSettingJSON(full, json.RawMessage(value))
		if err != nil {
			t.Fatalf("template %s: %v", full, err)
		}
		if string(parsed) != string(key.Default) {
			t.Errorf("template %s = %s, default %s", full, parsed, key.Default)
		}
		seen[full] = true
	}
	for _, key := range SettingKeys() {
		if strings.HasPrefix(key.Name, "access.") || key.Name == "behavior.expect_user" || key.Name == "profile.client" || key.Name == "profile.device" || key.Name == "profile.proxy" {
			continue
		}
		if !seen[key.Name] {
			t.Errorf("template has no default assignment for %s", key.Name)
		}
	}
	for _, comment := range []string{`# Default: "" (no check)`, `# Default: "" (not chosen:`, `# Default: "" (the Telegram client`, `# Default: "" (no proxy; "none" also means no proxy)`} {
		if !strings.Contains(Template(), comment) {
			t.Errorf("template missing %q", comment)
		}
	}
}
