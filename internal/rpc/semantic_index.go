package rpc

// The semantic indexer is a background job of the Daemon. It cuts the local
// messages into fragments and stores one embedding per fragment in vectors.db.
// It reads the message database, writes only vectors.db, never calls Telegram,
// and sends fragment text only to the local Ollama service.

const (
	// MethodSearchIndexStart queues an indexing run now. A run in progress is
	// reported unchanged.
	MethodSearchIndexStart = "search.index.start"
	// MethodSearchIndexStatus reports the state of the indexer.
	MethodSearchIndexStatus = "search.index.status"
	// MethodSearchIndexCancel stops the run after the batch it is embedding.
	MethodSearchIndexCancel = "search.index.cancel"
)

// SearchIndexParams are the parameters of the three index methods. They carry no
// field today: the indexer is one per daemon run. The type keeps the shape of
// the call when a filter appears.
type SearchIndexParams struct{}

// The states of the semantic indexer.
const (
	// SemanticIndexDisabled: [semantic_search] enabled is off. Nothing runs.
	SemanticIndexDisabled = "disabled"
	// SemanticIndexIdle: waiting for the next run.
	SemanticIndexIdle = "idle"
	// SemanticIndexIndexing: a run is in progress.
	SemanticIndexIndexing = "indexing"
	// SemanticIndexUnavailable: Ollama did not answer; the next run retries.
	SemanticIndexUnavailable = "unavailable"
	// SemanticIndexModelMissing: Ollama answered but has no such model.
	SemanticIndexModelMissing = "model_missing"
	// SemanticIndexError: the run failed for another reason.
	SemanticIndexError = "error"
)

// SemanticIndexStatus is the state of the semantic indexer. Reason is a short
// cause for the unavailable, model_missing and error states, and "cancelled"
// for an idle indexer after a cancelled run. It never carries message text. The counters describe the last run, or the run in progress:
// Indexed is the number of fragments stored for the current model, Total the
// number of fragments long enough for the index, Embedded the number computed
// in the run, Removed the number dropped in the run. StartedAt, FinishedAt and
// NextRunAt are Unix seconds.
type SemanticIndexStatus struct {
	Enabled     bool   `json:"enabled"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Model       string `json:"model,omitempty"`
	ModelDigest string `json:"model_digest,omitempty"`
	Indexed     int    `json:"indexed"`
	Total       int    `json:"total"`
	Embedded    int    `json:"embedded"`
	Removed     int    `json:"removed"`
	StartedAt   *int64 `json:"started_at,omitempty"`
	FinishedAt  *int64 `json:"finished_at,omitempty"`
	NextRunAt   *int64 `json:"next_run_at,omitempty"`
}
