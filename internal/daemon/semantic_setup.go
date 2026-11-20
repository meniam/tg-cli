package daemon

import (
	"path/filepath"

	"tg-cli/internal/embed"
	"tg-cli/internal/redact"
	"tg-cli/internal/vectors"
)

// vectorsFileName is the file of the semantic index, next to the profile database.
const vectorsFileName = "vectors.db"

// vectorsPath is where vectors.db lives: in the database directory of the
// profile, next to store.db. The file is derived data and can be deleted.
func vectorsPath(p Paths) string { return filepath.Join(p.DatabaseDir, vectorsFileName) }

// openSemantic prepares the pieces of semantic search when the setting is on:
// the vector file and the Ollama client. Without the setting nothing is
// created: no file, no client, no request. A failure is not fatal: the daemon
// keeps serving, search falls back to words, and semanticFailure says why. The
// returned function closes what was opened.
func (d *daemon) openSemantic() func() {
	cfg := d.opts.SemanticSearch
	if !cfg.Enabled {
		return func() {}
	}
	client, err := embed.New(embed.Options{BaseURL: cfg.OllamaURL, Model: cfg.Model, Batch: cfg.Batch})
	if err != nil {
		d.semanticFailure = "ollama client: " + redact.Text(err.Error())
		d.log.Warn("semantic search is on but the Ollama client could not be built", "err", d.semanticFailure)
		return func() {}
	}
	store, err := vectors.Open(vectorsPath(d.opts.Paths))
	if err != nil {
		d.semanticFailure = "vectors file: " + redact.Text(err.Error())
		d.log.Warn("semantic search is on but vectors.db could not be opened", "err", d.semanticFailure)
		return func() {}
	}
	d.embedder = client
	d.vectors = store
	return func() { _ = store.Close() }
}
