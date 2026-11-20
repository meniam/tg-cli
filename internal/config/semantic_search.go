package config

import (
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"tg-cli/internal/embed"
)

// SemanticSearch holds the settings of the local semantic index. The values are
// kept as the owner wrote them; Resolve checks and converts them.
type SemanticSearch struct {
	// Enabled turns the indexer on. Off by default: the daemon then makes no
	// request to Ollama and creates no vectors.db.
	Enabled bool `toml:"enabled"`
	// OllamaURL is the address of the local Ollama service.
	OllamaURL string `toml:"ollama_url"`
	// Model is the Ollama embedding model.
	Model string `toml:"model"`
	// Batch is the number of texts sent to Ollama in one request.
	Batch int `toml:"batch"`
	// Interval is the wait between two indexing runs, as a Go duration.
	Interval string `toml:"interval"`
	// MinSimilarity is the lowest cosine similarity a vector hit of
	// `tg search --semantic` may have; 0 turns the threshold off.
	MinSimilarity float64 `toml:"min_similarity"`
}

// SemanticSearchValues is a checked SemanticSearch.
type SemanticSearchValues struct {
	Enabled       bool
	OllamaURL     string
	Model         string
	Batch         int
	Interval      time.Duration
	MinSimilarity float64
}

const (
	// MaxSemanticModelRunes is the longest accepted model name.
	MaxSemanticModelRunes = 128
	// MinSemanticBatch and MaxSemanticBatch bound the texts per Ollama request.
	MinSemanticBatch = 1
	MaxSemanticBatch = 128
	// MinSemanticInterval and MaxSemanticInterval bound the wait between runs.
	MinSemanticInterval = time.Minute
	MaxSemanticInterval = 24 * time.Hour
)

func defaultSemanticSearch() SemanticSearch {
	return SemanticSearch{OllamaURL: "http://127.0.0.1:11434", Model: "bge-m3", Batch: 16, Interval: "10m", MinSimilarity: DefaultSemanticMinSimilarity}
}

// DefaultSemanticMinSimilarity is the threshold when the owner sets none. The
// cosine of bge-m3 for unrelated fragments of the owner's chats has a median of
// 0.40 to 0.42 and a 99th percentile near 0.5, so 0.5 cuts most of the noise.
const DefaultSemanticMinSimilarity = 0.5

// CheckOllamaURL reports why an address is not usable: it must be http or https
// with a loopback host (127.0.0.1, ::1, localhost), because the indexed text is
// private. The error text is the tail of the message that names the key.
func CheckOllamaURL(value string) error {
	if _, err := embed.ValidateBaseURL(value); err != nil {
		return fmt.Errorf("must be an http or https address of this machine (127.0.0.1, ::1 or localhost) without credentials or path")
	}
	return nil
}

// CheckSemanticModel reports why a model name is not usable.
func CheckSemanticModel(value string) error {
	if len(value) == 0 || utf8.RuneCountInString(value) > MaxSemanticModelRunes {
		return fmt.Errorf("must be 1 to %d characters", MaxSemanticModelRunes)
	}
	empty := true
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
		if !unicode.IsSpace(r) {
			empty = false
		}
	}
	if empty {
		return fmt.Errorf("must not be blank")
	}
	return nil
}

// ParseSemanticBatch reads a whole number of texts per request.
func ParseSemanticBatch(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinSemanticBatch || n > MaxSemanticBatch {
		return 0, fmt.Errorf("must be a whole number from %d to %d", MinSemanticBatch, MaxSemanticBatch)
	}
	return n, nil
}

// ParseSemanticInterval reads a Go duration from one minute to 24 hours.
func ParseSemanticInterval(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < MinSemanticInterval || d > MaxSemanticInterval {
		return 0, fmt.Errorf("must be a duration from 1m to 24h such as 10m or 1h")
	}
	return d, nil
}

// CheckSemanticMinSimilarity reports why a similarity threshold is not usable:
// it is a cosine, so it lies from 0 to 1 inclusive. The error text is the tail
// of the message that names the key or the flag.
func CheckSemanticMinSimilarity(value float64) error {
	if math.IsNaN(value) || value < 0 || value > 1 {
		return fmt.Errorf("must be a number from 0 to 1 (0 turns the threshold off)")
	}
	return nil
}

// ParseSemanticMinSimilarity reads a similarity threshold such as 0.5.
func ParseSemanticMinSimilarity(value string) (float64, error) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a number from 0 to 1 (0 turns the threshold off)")
	}
	if err := CheckSemanticMinSimilarity(n); err != nil {
		return 0, err
	}
	return n, nil
}

// Resolve checks every setting and returns the converted values. An error names
// the config key; it is a usage error.
func (s SemanticSearch) Resolve() (SemanticSearchValues, error) {
	if err := CheckOllamaURL(s.OllamaURL); err != nil {
		return SemanticSearchValues{}, invalid("semantic_search.ollama_url %v", err)
	}
	if err := CheckSemanticModel(s.Model); err != nil {
		return SemanticSearchValues{}, invalid("semantic_search.model %v", err)
	}
	batch, err := ParseSemanticBatch(strconv.Itoa(s.Batch))
	if err != nil {
		return SemanticSearchValues{}, invalid("semantic_search.batch %v", err)
	}
	interval, err := ParseSemanticInterval(s.Interval)
	if err != nil {
		return SemanticSearchValues{}, invalid("semantic_search.interval %v", err)
	}
	if err := CheckSemanticMinSimilarity(s.MinSimilarity); err != nil {
		return SemanticSearchValues{}, invalid("semantic_search.min_similarity %v", err)
	}
	return SemanticSearchValues{Enabled: s.Enabled, OllamaURL: s.OllamaURL, Model: s.Model, Batch: batch, Interval: interval, MinSimilarity: s.MinSimilarity}, nil
}
