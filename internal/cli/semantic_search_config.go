package cli

import (
	"strconv"
	"strings"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// semanticSearchEnv names, per config key, the environment variable that
// overrides it.
var semanticSearchEnv = struct{ enabled, ollamaURL, model, batch, interval string }{
	"TG_CLI_SEMANTIC_SEARCH_ENABLED",
	"TG_CLI_SEMANTIC_SEARCH_OLLAMA_URL",
	"TG_CLI_SEMANTIC_SEARCH_MODEL",
	"TG_CLI_SEMANTIC_SEARCH_BATCH",
	"TG_CLI_SEMANTIC_SEARCH_INTERVAL",
}

// daemonSemanticSearch is the only parser for the settings of the semantic
// index: config.toml first, then the TG_CLI_SEMANTIC_SEARCH_* environment over
// it. The background parent validates the result; the foreground process parses
// the inherited environment again, as it does for the background sync.
func (a *app) daemonSemanticSearch() (daemon.SemanticSearchConfig, error) {
	settings := a.config.SemanticSearch
	overridden := false
	for _, item := range []struct {
		name  string
		check func(string) error
		set   func(string)
	}{
		{semanticSearchEnv.ollamaURL, config.CheckOllamaURL, func(v string) { settings.OllamaURL = v }},
		{semanticSearchEnv.model, config.CheckSemanticModel, func(v string) { settings.Model = v }},
		{semanticSearchEnv.batch, func(v string) error { _, err := config.ParseSemanticBatch(v); return err }, func(v string) { settings.Batch, _ = strconv.Atoi(v) }},
		{semanticSearchEnv.interval, func(v string) error { _, err := config.ParseSemanticInterval(v); return err }, func(v string) { settings.Interval = v }},
	} {
		value := strings.TrimSpace(a.deps.Getenv(item.name))
		if value == "" {
			continue
		}
		if err := item.check(value); err != nil {
			return daemon.SemanticSearchConfig{}, fail(exitcode.Usage, "%s %v", item.name, err)
		}
		item.set(value)
		overridden = true
	}
	switch value := strings.TrimSpace(a.deps.Getenv(semanticSearchEnv.enabled)); value {
	case "":
	case "true", "false":
		settings.Enabled = value == "true"
		overridden = true
	default:
		return daemon.SemanticSearchConfig{}, fail(exitcode.Usage, "%s must be true or false", semanticSearchEnv.enabled)
	}
	values, err := settings.Resolve()
	if err != nil {
		if overridden {
			return daemon.SemanticSearchConfig{}, fail(exitcode.Usage, "%v (after the TG_CLI_SEMANTIC_SEARCH_* overrides)", err)
		}
		return daemon.SemanticSearchConfig{}, fail(exitcode.Usage, "%v", err)
	}
	return daemon.SemanticSearchConfig{
		Enabled:   values.Enabled,
		OllamaURL: values.OllamaURL,
		Model:     values.Model,
		Batch:     values.Batch,
		Interval:  values.Interval,
	}, nil
}
