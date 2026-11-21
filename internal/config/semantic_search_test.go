package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadSemanticSearch(t *testing.T, content string) (Config, error) {
	t.Helper()
	return loadBackgroundSync(t, content)
}

func TestSemanticSearchDefaults(t *testing.T) {
	for name, content := range map[string]string{"missing file": "", "template": Template()} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadSemanticSearch(t, content)
			if err != nil {
				t.Fatal(err)
			}
			values, err := cfg.SemanticSearch.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			want := SemanticSearchValues{Enabled: true, OllamaURL: "http://127.0.0.1:11434", Model: "bge-m3", Batch: 16, Interval: 10 * time.Minute, MinSimilarity: 0.5}
			if values != want {
				t.Fatalf("values = %+v, want %+v (enabled)", values, want)
			}
		})
	}
}

func TestSemanticSearchFromFile(t *testing.T) {
	cfg, err := loadSemanticSearch(t, `[semantic_search]
enabled = true
ollama_url = "https://localhost:8443"
model = "embeddinggemma:latest"
batch = 128
interval = "24h"
min_similarity = 0.65
`)
	if err != nil {
		t.Fatal(err)
	}
	values, err := cfg.SemanticSearch.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := SemanticSearchValues{Enabled: true, OllamaURL: "https://localhost:8443", Model: "embeddinggemma:latest", Batch: 128, Interval: 24 * time.Hour, MinSimilarity: 0.65}
	if values != want {
		t.Fatalf("values = %+v, want %+v", values, want)
	}
}

func TestSemanticSearchAcceptsTheBounds(t *testing.T) {
	for name, body := range map[string]string{
		"batch low":        "batch = 1",
		"batch high":       "batch = 128",
		"interval low":     `interval = "1m"`,
		"interval high":    `interval = "24h"`,
		"similarity zero":  "min_similarity = 0",
		"similarity one":   "min_similarity = 1",
		"similarity float": "min_similarity = 0.55",
		"model 128":        `model = "` + strings.Repeat("m", 128) + `"`,
		"ipv6 loopback":    `ollama_url = "http://[::1]:11434"`,
		"127.0.0.1 only":   `ollama_url = "http://127.0.0.1"`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSemanticSearch(t, "[semantic_search]\n"+body+"\n"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestSemanticSearchRejectsInvalidValues covers every rule of the section: each
// invalid file is a usage error (code 2) that names the key.
func TestSemanticSearchRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"url external host", `ollama_url = "http://example.com:11434"`, "semantic_search.ollama_url must be an http or https address of this machine"},
		{"url lan address", `ollama_url = "http://192.168.1.5:11434"`, "semantic_search.ollama_url"},
		{"url all interfaces", `ollama_url = "http://0.0.0.0:11434"`, "semantic_search.ollama_url"},
		{"url scheme", `ollama_url = "ftp://127.0.0.1"`, "semantic_search.ollama_url"},
		{"url credentials", `ollama_url = "http://user:pass@127.0.0.1:11434"`, "semantic_search.ollama_url"},
		{"url path", `ollama_url = "http://127.0.0.1:11434/api"`, "semantic_search.ollama_url"},
		{"url not an address", `ollama_url = "localhost"`, "semantic_search.ollama_url"},
		{"url empty", `ollama_url = ""`, "semantic_search.ollama_url"},
		{"model empty", `model = ""`, "semantic_search.model must be 1 to 128 characters"},
		{"model blank", `model = "   "`, "semantic_search.model must not be blank"},
		{"model too long", `model = "` + strings.Repeat("m", 129) + `"`, "semantic_search.model must be 1 to 128 characters"},
		{"model control character", `model = "bge\u0007m3"`, "semantic_search.model must not contain control characters"},
		{"batch zero", `batch = 0`, "semantic_search.batch must be a whole number from 1 to 128"},
		{"batch negative", `batch = -4`, "semantic_search.batch must be a whole number from 1 to 128"},
		{"batch above the limit", `batch = 129`, "semantic_search.batch must be a whole number from 1 to 128"},
		{"interval not a duration", `interval = "often"`, "semantic_search.interval must be a duration from 1m to 24h"},
		{"interval below one minute", `interval = "59s"`, "semantic_search.interval must be a duration from 1m to 24h"},
		{"interval zero", `interval = "0"`, "semantic_search.interval must be a duration from 1m to 24h"},
		{"interval negative", `interval = "-10m"`, "semantic_search.interval must be a duration from 1m to 24h"},
		{"interval above a day", `interval = "24h1s"`, "semantic_search.interval must be a duration from 1m to 24h"},
		{"similarity above one", `min_similarity = 1.01`, "semantic_search.min_similarity must be a number from 0 to 1"},
		{"similarity negative", `min_similarity = -0.1`, "semantic_search.min_similarity must be a number from 0 to 1"},
		{"similarity integer above one", `min_similarity = 2`, "semantic_search.min_similarity must be a number from 0 to 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadSemanticSearch(t, "[semantic_search]\n"+tc.body+"\n")
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code() != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want a code 2 error containing %q", err, tc.want)
			}
		})
	}
}

func TestSemanticSearchTypesAreChecked(t *testing.T) {
	for name, body := range map[string]string{
		"enabled string":    `enabled = "yes"`,
		"batch string":      `batch = "16"`,
		"batch float":       `batch = 16.5`,
		"interval int":      `interval = 600`,
		"similarity string": `min_similarity = "0.5"`,
		"unknown key":       `window = "1h"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadSemanticSearch(t, "[semantic_search]\n"+body+"\n")
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code() != 2 {
				t.Fatalf("Load error = %v, want a code 2 error", err)
			}
		})
	}
}

// TestSemanticSearchSurvivesProfileUpdate shows that writing a profile key keeps
// the section byte for byte.
func TestSemanticSearchSurvivesProfileUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[semantic_search]\nenabled = true\nmodel = \"embeddinggemma\"\nbatch = 8\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := UpdateProfile(path, "default", map[string]string{"device": "X"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.SemanticSearch
	if !s.Enabled || s.Model != "embeddinggemma" || s.Batch != 8 || s.OllamaURL != "http://127.0.0.1:11434" || s.Interval != "10m" {
		t.Fatalf("section after update = %+v", s)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), content) {
		t.Fatalf("the update changed the section:\n%s", data)
	}
}
