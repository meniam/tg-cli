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
		"web.listen": c.Web.Listen, "web.open": c.Web.Open, "web.cache_bytes": c.Web.CacheBytes, "web.cache_ttl": c.Web.CacheTTL,
		"behavior.throttle_percent": c.Behavior.ThrottlePercent, "behavior.flood_autowait": c.Behavior.FloodAutoWait, "behavior.expect_user": c.Behavior.ExpectUser,
		"background_sync.enabled": c.BackgroundSync.Enabled, "background_sync.window_start": c.BackgroundSync.WindowStart, "background_sync.window_end": c.BackgroundSync.WindowEnd, "background_sync.interval_min": c.BackgroundSync.IntervalMin, "background_sync.interval_max": c.BackgroundSync.IntervalMax, "background_sync.page_pause_min": c.BackgroundSync.PagePauseMin, "background_sync.page_pause_max": c.BackgroundSync.PagePauseMax,
		"semantic_search.enabled": c.SemanticSearch.Enabled, "semantic_search.ollama_url": c.SemanticSearch.OllamaURL, "semantic_search.model": c.SemanticSearch.Model, "semantic_search.batch": c.SemanticSearch.Batch, "semantic_search.interval": c.SemanticSearch.Interval, "semantic_search.min_similarity": c.SemanticSearch.MinSimilarity,
		"voice_transcription.enabled": c.VoiceTranscription.Enabled, "voice_transcription.ffmpeg": c.VoiceTranscription.FFmpeg, "voice_transcription.max_voice_mb": c.VoiceTranscription.MaxVoiceMB, "voice_transcription.max_video_note_mb": c.VoiceTranscription.MaxVideoNoteMB, "voice_transcription.max_video_mb": c.VoiceTranscription.MaxVideoMB, "voice_transcription.max_video_minutes": c.VoiceTranscription.MaxVideoMinutes, "voice_transcription.daily_download_mb": c.VoiceTranscription.DailyDownloadMB, "voice_transcription.daily_files": c.VoiceTranscription.DailyFiles, "voice_transcription.pause": c.VoiceTranscription.Pause, "voice_transcription.threads": c.VoiceTranscription.Threads,
		"history_limit.since": "2022-02-02", "history_limit.messages": 200000,
		"new_chat_sync.messages": 100,
		"profile.client":         "desktop", "profile.device": "", "profile.transport": "tcp", "profile.proxy": "none",
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
		{"web.listen", "127.0.0.1:1313", `"127.0.0.1:1313"`, []string{"bad", "0.0.0.0:1234"}},
		{"web.open", "true", `true`, []string{"1"}},
		{"web.cache_bytes", "131072", `131072`, []string{"131071", "1073741825"}},
		{"web.cache_ttl", "6h", `"6h"`, []string{"0s", "-1h", "abc"}},
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
		{"new_chat_sync.messages", "1000", "1000", []string{"-1", "1001", "1.5"}},
		{"history_limit.since", "2021-12-31", `"2021-12-31"`, []string{"31-12-2021", "2021-13-01", "2022-2-2", "yesterday"}},
		{"history_limit.messages", "0", "0", []string{"-1", "100000001", "1.5"}},
		{"semantic_search.enabled", "false", "false", []string{"yes"}},
		{"semantic_search.ollama_url", "http://localhost:11434", `"http://localhost:11434"`, []string{"http://example.com", "http://localhost/path"}},
		{"semantic_search.model", "model", `"model"`, []string{"", "  ", "a\nb", strings.Repeat("x", 129)}},
		{"semantic_search.batch", "128", "128", []string{"0", "129", "1.2"}},
		{"semantic_search.interval", "1m", `"1m"`, []string{"59s", "25h", "bad"}},
		{"semantic_search.min_similarity", "0", "0", []string{"-0.1", "1.1", "NaN"}},
		{"voice_transcription.enabled", "false", "false", []string{"yes"}},
		{"voice_transcription.ffmpeg", "avconv", `"avconv"`, []string{"", "  "}},
		{"voice_transcription.max_voice_mb", "1", "1", []string{"0", "1000001"}},
		{"voice_transcription.max_video_note_mb", "2", "2", []string{"0", "1000001"}},
		{"voice_transcription.max_video_mb", "3", "3", []string{"0", "1000001"}},
		{"voice_transcription.max_video_minutes", "4", "4", []string{"0", "1000001"}},
		{"voice_transcription.daily_download_mb", "5", "5", []string{"0", "1000001"}},
		{"voice_transcription.daily_files", "6", "6", []string{"0", "1000001"}},
		{"voice_transcription.pause", "0s", `"0s"`, []string{"-1s", "1h1s"}},
		{"voice_transcription.threads", "16", "16", []string{"0", "17"}},
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
	expected := map[string]string{
		"web.listen": `""`, "web.open": `false`, "web.cache_bytes": `33554432`, "web.cache_ttl": `"168h"`,
		"behavior.throttle_percent": `60`, "behavior.flood_autowait": `"10s"`, "behavior.expect_user": `""`,
		"background_sync.enabled": `true`, "background_sync.window_start": `"08:00"`, "background_sync.window_end": `"20:00"`, "background_sync.interval_min": `"40m"`, "background_sync.interval_max": `"3h"`, "background_sync.page_pause_min": `"2s"`, "background_sync.page_pause_max": `"8s"`,
		"history_limit.since": `"2022-02-02"`, "history_limit.messages": `200000`,
		"new_chat_sync.messages":  `100`,
		"semantic_search.enabled": `true`, "semantic_search.ollama_url": `"http://127.0.0.1:11434"`, "semantic_search.model": `"bge-m3"`, "semantic_search.batch": `16`, "semantic_search.interval": `"10m"`, "semantic_search.min_similarity": `0.5`,
		"voice_transcription.enabled": `true`, "voice_transcription.ffmpeg": `"ffmpeg"`, "voice_transcription.max_voice_mb": `30`, "voice_transcription.max_video_note_mb": `50`, "voice_transcription.max_video_mb": `500`, "voice_transcription.max_video_minutes": `120`, "voice_transcription.daily_download_mb": `5000`, "voice_transcription.daily_files": `500`, "voice_transcription.pause": `"3s"`, "voice_transcription.threads": `4`,
		"profile.client": `"desktop"`, "profile.device": `""`, "profile.transport": `"tcp"`, "profile.proxy": `"none"`,
	}
	for _, key := range SettingKeys() {
		if strings.HasPrefix(key.Name, "access.") {
			continue
		}
		if _, ok := expected[key.Name]; !ok {
			t.Errorf("missing expected default for %s", key.Name)
		}
	}
	for name, want := range expected {
		key, ok := LookupSetting(name)
		if !ok || string(key.Default) != want {
			t.Errorf("%s default = %s, want %s", name, key.Default, want)
		}
		if ok && key.Validate(key.Default) != nil {
			t.Errorf("invalid default for %s", name)
		}
	}
	for _, section := range []string{"[web]", "[behavior]", "[background_sync]", "[semantic_search]", "[voice_transcription]", "[profiles.default]"} {
		if strings.Contains(Template(), section) {
			t.Errorf("template contains legacy section %s", section)
		}
	}
}
