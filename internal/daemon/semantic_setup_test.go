package daemon

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestOpenSemanticDoesNothingWhenOff(t *testing.T) {
	dir := t.TempDir()
	d := &daemon{opts: Options{Paths: Paths{DatabaseDir: dir}}, log: discardLogger()}
	closeFn := d.openSemantic()
	defer closeFn()
	if d.vectors != nil || d.embedder != nil || d.semanticFailure != "" {
		t.Fatalf("off: vectors=%v embedder=%v failure=%q", d.vectors, d.embedder, d.semanticFailure)
	}
	if _, err := os.Stat(vectorsPath(d.opts.Paths)); !os.IsNotExist(err) {
		t.Fatalf("vectors.db must not exist when the feature is off: %v", err)
	}
}

func TestOpenSemanticCreatesTheFileWhenOn(t *testing.T) {
	dir := t.TempDir()
	d := &daemon{opts: Options{Paths: Paths{DatabaseDir: dir}, SemanticSearch: SemanticSearchConfig{Enabled: true, OllamaURL: "http://127.0.0.1:11434", Model: "bge-m3", Batch: 16, Interval: 10 * time.Minute}}, log: discardLogger()}
	closeFn := d.openSemantic()
	defer closeFn()
	if d.vectors == nil || d.embedder == nil || d.semanticFailure != "" {
		t.Fatalf("on: vectors=%v embedder=%v failure=%q", d.vectors, d.embedder, d.semanticFailure)
	}
	info, err := os.Stat(vectorsPath(d.opts.Paths))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("vectors.db mode %v", info.Mode().Perm())
	}
}

func TestOpenSemanticReportsABadAddress(t *testing.T) {
	d := &daemon{opts: Options{Paths: Paths{DatabaseDir: t.TempDir()}, SemanticSearch: SemanticSearchConfig{Enabled: true, OllamaURL: "http://example.com:11434", Model: "bge-m3", Batch: 16}}, log: discardLogger()}
	closeFn := d.openSemantic()
	defer closeFn()
	if d.vectors != nil || d.embedder != nil || d.semanticFailure == "" {
		t.Fatalf("a non-loopback address must be refused: vectors=%v embedder=%v failure=%q", d.vectors, d.embedder, d.semanticFailure)
	}
}
