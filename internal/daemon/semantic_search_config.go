package daemon

import "time"

// SemanticSearchConfig holds the settings of the local semantic index. The
// caller checks them (see config.SemanticSearch); the indexer trusts them.
// Without Enabled the daemon makes no request to Ollama and creates no
// vectors.db.
type SemanticSearchConfig struct {
	// Enabled turns the indexer on.
	Enabled bool
	// OllamaURL is the address of the Ollama service, a loopback address.
	OllamaURL string
	// Model is the Ollama embedding model.
	Model string
	// Batch is the number of texts per Ollama request.
	Batch int
	// Interval is the wait between two indexing runs.
	Interval time.Duration
}
