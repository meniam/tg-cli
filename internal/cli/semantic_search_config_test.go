package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// defaultSemanticSearch is what the daemon gets when nothing is configured.
var defaultSemanticSearch = daemon.SemanticSearchConfig{
	OllamaURL: "http://127.0.0.1:11434", Model: "bge-m3", Batch: 16, Interval: 10 * time.Minute,
}

func TestDaemonSemanticSearchDefaults(t *testing.T) {
	got, err := backgroundSyncApp(t, "", nil).daemonSemanticSearch()
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultSemanticSearch {
		t.Fatalf("got %+v, want %+v (disabled)", got, defaultSemanticSearch)
	}
}

func TestDaemonSemanticSearchEnvironmentOverridesFile(t *testing.T) {
	file := "[semantic_search]\nenabled = false\nollama_url = \"http://localhost:1111\"\nmodel = \"file-model\"\nbatch = 4\ninterval = \"1h\"\n"
	fromFile := daemon.SemanticSearchConfig{OllamaURL: "http://localhost:1111", Model: "file-model", Batch: 4, Interval: time.Hour}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want daemon.SemanticSearchConfig
	}{
		{"file only", nil, fromFile},
		{"every variable", map[string]string{
			"TG_CLI_SEMANTIC_SEARCH_ENABLED":    " true ",
			"TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL": "http://[::1]:2222",
			"TG_CLI_SEMANTIC_SEARCH_MODEL":      "env-model",
			"TG_CLI_SEMANTIC_SEARCH_BATCH":      "128",
			"TG_CLI_SEMANTIC_SEARCH_INTERVAL":   "90s",
		}, daemon.SemanticSearchConfig{Enabled: true, OllamaURL: "http://[::1]:2222", Model: "env-model", Batch: 128, Interval: 90 * time.Second}},
		{"one variable keeps the rest of the file", map[string]string{"TG_CLI_SEMANTIC_SEARCH_BATCH": "8"},
			daemon.SemanticSearchConfig{OllamaURL: "http://localhost:1111", Model: "file-model", Batch: 8, Interval: time.Hour}},
		{"blank variable is ignored", map[string]string{"TG_CLI_SEMANTIC_SEARCH_ENABLED": "  ", "TG_CLI_SEMANTIC_SEARCH_MODEL": " "}, fromFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backgroundSyncApp(t, file, tc.env).daemonSemanticSearch()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDaemonSemanticSearchEnabledOverridesFileBothWays(t *testing.T) {
	on, err := backgroundSyncApp(t, "", map[string]string{"TG_CLI_SEMANTIC_SEARCH_ENABLED": "true"}).daemonSemanticSearch()
	if err != nil || !on.Enabled {
		t.Fatalf("enabled = %v, err = %v, want the variable to turn it on", on.Enabled, err)
	}
	off, err := backgroundSyncApp(t, "[semantic_search]\nenabled = true\n", map[string]string{"TG_CLI_SEMANTIC_SEARCH_ENABLED": "false"}).daemonSemanticSearch()
	if err != nil || off.Enabled {
		t.Fatalf("enabled = %v, err = %v, want the variable to turn it off", off.Enabled, err)
	}
}

// TestDaemonSemanticSearchInvalidEnvironment checks every rule through the
// environment: each wrong value is a usage error that names its variable.
func TestDaemonSemanticSearchInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, variable, value, want string
	}{
		{"enabled word", "TG_CLI_SEMANTIC_SEARCH_ENABLED", "yes", "TG_CLI_SEMANTIC_SEARCH_ENABLED must be true or false"},
		{"enabled number", "TG_CLI_SEMANTIC_SEARCH_ENABLED", "1", "TG_CLI_SEMANTIC_SEARCH_ENABLED must be true or false"},
		{"url external host", "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL", "http://example.com:11434", "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL must be an http or https address of this machine"},
		{"url scheme", "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL", "tcp://127.0.0.1:11434", "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL"},
		{"model too long", "TG_CLI_SEMANTIC_SEARCH_MODEL", strings.Repeat("m", 129), "TG_CLI_SEMANTIC_SEARCH_MODEL must be 1 to 128 characters"},
		{"model control character", "TG_CLI_SEMANTIC_SEARCH_MODEL", "bge\x07m3", "TG_CLI_SEMANTIC_SEARCH_MODEL must not contain control characters"},
		{"batch zero", "TG_CLI_SEMANTIC_SEARCH_BATCH", "0", "TG_CLI_SEMANTIC_SEARCH_BATCH must be a whole number from 1 to 128"},
		{"batch above the limit", "TG_CLI_SEMANTIC_SEARCH_BATCH", "129", "TG_CLI_SEMANTIC_SEARCH_BATCH must be a whole number from 1 to 128"},
		{"batch word", "TG_CLI_SEMANTIC_SEARCH_BATCH", "many", "TG_CLI_SEMANTIC_SEARCH_BATCH must be a whole number from 1 to 128"},
		{"interval below a minute", "TG_CLI_SEMANTIC_SEARCH_INTERVAL", "30s", "TG_CLI_SEMANTIC_SEARCH_INTERVAL must be a duration from 1m to 24h"},
		{"interval above a day", "TG_CLI_SEMANTIC_SEARCH_INTERVAL", "25h", "TG_CLI_SEMANTIC_SEARCH_INTERVAL must be a duration from 1m to 24h"},
		{"interval word", "TG_CLI_SEMANTIC_SEARCH_INTERVAL", "later", "TG_CLI_SEMANTIC_SEARCH_INTERVAL must be a duration from 1m to 24h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := backgroundSyncApp(t, "", map[string]string{tc.variable: tc.value}).daemonSemanticSearch()
			var commandErr *cmdError
			if !errors.As(err, &commandErr) || commandErr.exit != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want usage containing %q", err, tc.want)
			}
		})
	}
}

// TestInvalidSemanticSearchStopsBeforeClientAndLauncher is the exit-code-2
// contract: a wrong value, from the file or from the environment, stops
// `daemon start` before a client is created or a process is launched.
func TestInvalidSemanticSearchStopsBeforeClientAndLauncher(t *testing.T) {
	for _, tc := range []struct {
		name, file, variable, value string
		want                        string
	}{
		{name: "environment value", variable: "TG_CLI_SEMANTIC_SEARCH_BATCH", value: "0", want: "TG_CLI_SEMANTIC_SEARCH_BATCH"},
		{name: "environment address", variable: "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL", value: "http://10.0.0.5:11434", want: "TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL"},
		{name: "file value", file: "[semantic_search]\ninterval = \"10s\"\n", want: "semantic_search.interval"},
		{name: "file address", file: "[semantic_search]\nollama_url = \"http://example.com\"\n", want: "semantic_search.ollama_url"},
	} {
		for _, foreground := range []bool{false, true} {
			name := tc.name + "/background"
			args := []string{"daemon", "start"}
			if foreground {
				name = tc.name + "/foreground"
				args = append(args, "--foreground")
			}
			t.Run(name, func(t *testing.T) {
				e := newEnv(t)
				if tc.file != "" {
					writeConfig(t, e, tc.file)
				}
				if tc.variable != "" {
					e.env[tc.variable] = tc.value
				}
				var stdout, stderr safeBuffer
				deps := e.deps(&stdout, &stderr)
				created, launched := false, false
				deps.NewTelegram = func(daemon.TelegramConfig) (daemon.Telegram, error) {
					created = true
					return nil, errors.New("should not create Telegram")
				}
				deps.LaunchDaemon = func([]string, string) (int, func() error, error) {
					launched = true
					return 0, nil, errors.New("should not launch daemon")
				}
				code := Execute(context.Background(), args, deps)
				if code != exitcode.Usage || created || launched {
					t.Fatalf("code=%d created=%t launched=%t", code, created, launched)
				}
				if answer := stdout.String() + stderr.String(); !strings.Contains(answer, tc.want) {
					t.Fatalf("unexpected diagnostic: %q", answer)
				}
			})
		}
	}
}

// TestSemanticSearchReachesDaemonOptions follows the settings from config.toml
// and the environment through `daemon start --foreground` into daemon.Options.
func TestSemanticSearchReachesDaemonOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  map[string]string
		want daemon.SemanticSearchConfig
	}{
		{"off by default", "", nil, defaultSemanticSearch},
		{"file and environment", "[semantic_search]\nenabled = false\nmodel = \"file-model\"\nbatch = 32\n", map[string]string{
			"TG_CLI_SEMANTIC_SEARCH_ENABLED":  "true",
			"TG_CLI_SEMANTIC_SEARCH_INTERVAL": "2h",
		}, daemon.SemanticSearchConfig{Enabled: true, OllamaURL: "http://127.0.0.1:11434", Model: "file-model", Batch: 32, Interval: 2 * time.Hour}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.file != "" {
				writeConfig(t, e, tc.file)
			}
			for key, value := range tc.env {
				e.env[key] = value
			}
			var received daemon.Options
			called := false
			previous := runDaemon
			runDaemon = func(_ context.Context, opts daemon.Options) error {
				received, called = opts, true
				return nil
			}
			t.Cleanup(func() { runDaemon = previous })
			var stdout, stderr safeBuffer
			deps := e.deps(&stdout, &stderr)
			deps.NewTelegram = func(daemon.TelegramConfig) (daemon.Telegram, error) { return e.tg, nil }
			if code := Execute(context.Background(), []string{"daemon", "start", "--foreground"}, deps); code != exitcode.OK {
				t.Fatalf("exit %d: %s %s", code, stdout.String(), stderr.String())
			}
			if !called || received.SemanticSearch != tc.want {
				t.Fatalf("called=%t options=%+v, want %+v", called, received.SemanticSearch, tc.want)
			}
		})
	}
}

// TestConfigShowListsSemanticSearch keeps `config show` complete: the section is
// printed as read from the file, with the defaults for what the file omits.
func TestConfigShowListsSemanticSearch(t *testing.T) {
	e := newEnv(t)
	for name, text := range map[string]string{
		"defaults": "",
		"file":     "[semantic_search]\nenabled = true\nmodel = \"gemma\"\nbatch = 64\n",
	} {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, e, text)
			r := e.run("config", "show")
			if r.code != exitcode.OK {
				t.Fatalf("show: %d %s %s", r.code, r.stdout, r.stderr)
			}
			var got configShowResult
			if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
				t.Fatal(err)
			}
			want := configSemanticSearchResult{OllamaURL: "http://127.0.0.1:11434", Model: "bge-m3", Batch: 16, Interval: "10m"}
			if name == "file" {
				want.Enabled, want.Model, want.Batch = true, "gemma", 64
			}
			if got.SemanticSearch != want {
				t.Fatalf("semantic_search = %+v, want %+v", got.SemanticSearch, want)
			}
		})
	}
}
