package gigaam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Errors returned by Install. They wrap the underlying cause, so callers
// classify with errors.Is.
var (
	// ErrDownload means the file could not be fetched (no network, bad
	// status, interrupted transfer).
	ErrDownload = errors.New("download failed")
	// ErrSize means the downloaded size differs from the pinned size.
	ErrSize = errors.New("size mismatch")
	// ErrChecksum means the SHA-256 differs from the pinned value.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrUnsafeArchive means an archive entry is not allowed.
	ErrUnsafeArchive = errors.New("unsafe archive")
	// ErrIncomplete means the unpacked component misses required files.
	ErrIncomplete = errors.New("component incomplete after unpacking")
)

// ProgressFunc receives the step name ("runtime:download", "model:extract",
// "vad:skip", ...) and the number of bytes handled so far in that step. It is
// called from the installing goroutine and must not block.
type ProgressFunc func(step string, bytes int64)

// Options configures Install.
type Options struct {
	// Paths is the target layout; Paths.Root must be set.
	Paths Paths
	// Artifacts overrides the download table. Nil means Pinned().
	Artifacts []Artifact
	// HTTPClient overrides the client. Nil means a client with connection
	// timeouts and no overall deadline; the context bounds the transfer.
	HTTPClient *http.Client
	// Progress receives progress events. Nil disables them.
	Progress ProgressFunc
}

// Install downloads and unpacks whatever Check reports missing. Ready
// components are skipped. Each component is verified (size, SHA-256) before it
// is unpacked and goes live by a rename, so a failure keeps the previous
// installation intact and leaves no temporary files behind.
func Install(ctx context.Context, opts Options) error {
	root := opts.Paths.Root
	if root == "" {
		return errors.New("gigaam: install root is empty")
	}
	artifacts := opts.Artifacts
	if artifacts == nil {
		artifacts = Pinned()
	}
	client := opts.HTTPClient
	if client == nil {
		client = defaultClient()
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string, int64) {}
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("gigaam: create %s: %w", root, err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return fmt.Errorf("gigaam: chmod %s: %w", root, err)
	}
	cleanStale(root)

	for _, a := range artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(checkComponent(a.Component, opts.Paths)) == 0 {
			progress(a.Component+":skip", 0)
			continue
		}
		if err := installComponent(ctx, client, progress, opts.Paths, a); err != nil {
			return fmt.Errorf("install %s: %w", a.Component, err)
		}
	}
	return nil
}

func defaultClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: tr}
}

// Names of temporary entries inside the root; cleanStale removes leftovers of
// a killed run.
const (
	downloadPrefix = ".download-"
	stagePrefix    = ".stage-"
	oldPrefix      = ".old-"
)

// cleanStale removes temporary leftovers and restores a component whose swap
// was interrupted between the two renames.
func cleanStale(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(root, name)
		switch {
		case strings.HasPrefix(name, downloadPrefix), strings.HasPrefix(name, stagePrefix):
			_ = os.RemoveAll(full)
		case strings.HasPrefix(name, oldPrefix):
			target := filepath.Join(root, strings.TrimPrefix(name, oldPrefix))
			if _, err := os.Lstat(target); err != nil {
				_ = os.Rename(full, target)
			} else {
				_ = os.RemoveAll(full)
			}
		}
	}
}

func installComponent(ctx context.Context, client *http.Client, progress ProgressFunc, p Paths, a Artifact) error {
	root := p.Root
	file, err := downloadVerified(ctx, client, progress, root, a)
	if err != nil {
		return err
	}
	defer os.Remove(file)

	if a.Component == ComponentVAD {
		if err := os.Chmod(file, 0o600); err != nil {
			return err
		}
		progress(a.Component+":install", 0)
		return os.Rename(file, p.VAD)
	}

	targetName, err := componentDirName(a.Component)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, stagePrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	progress(a.Component+":extract", 0)
	if err := extractArchive(ctx, file, filepath.Join(stage, targetName), a); err != nil {
		return err
	}
	if problems := checkComponent(a.Component, pathsFromRoot(stage)); len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrIncomplete, problems[0])
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	progress(a.Component+":install", 0)
	return replaceDir(filepath.Join(stage, targetName), filepath.Join(root, targetName))
}

func componentDirName(component string) (string, error) {
	switch component {
	case ComponentRuntime:
		return runtimeDirName, nil
	case ComponentModel:
		return modelDirName, nil
	}
	return "", fmt.Errorf("gigaam: unknown archive component %q", component)
}

// replaceDir swaps staged into target. The previous target is moved aside
// first and put back if the second rename fails.
func replaceDir(staged, target string) error {
	old := filepath.Join(filepath.Dir(target), oldPrefix+filepath.Base(target))
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	hadOld := true
	if err := os.Rename(target, old); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		hadOld = false
	}
	if err := os.Rename(staged, target); err != nil {
		if hadOld {
			_ = os.Rename(old, target)
		}
		return err
	}
	if hadOld {
		_ = os.RemoveAll(old)
	}
	return nil
}

// downloadVerified fetches a.URL into a temporary file inside root and checks
// size and SHA-256. The file is deleted on any failure; on success the caller
// owns it.
func downloadVerified(ctx context.Context, client *http.Client, progress ProgressFunc, root string, a Artifact) (_ string, err error) {
	step := a.Component + ":download"
	progress(step, 0)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDownload, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", downloadError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: server answered %s", ErrDownload, resp.Status)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != a.Size {
		return "", fmt.Errorf("%w: server announces %d bytes, expected %d", ErrSize, resp.ContentLength, a.Size)
	}

	f, err := os.CreateTemp(root, downloadPrefix)
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	h := sha256.New()
	pr := &progressWriter{step: step, fn: progress}
	// One byte above the pinned size shows an oversized body without reading it all.
	n, copyErr := io.Copy(io.MultiWriter(f, h, pr), io.LimitReader(resp.Body, a.Size+1))
	if copyErr != nil {
		return "", downloadError(ctx, copyErr)
	}
	pr.flush()
	if n != a.Size {
		return "", fmt.Errorf("%w: got %d bytes, expected %d", ErrSize, n, a.Size)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	progress(a.Component+":verify", n)
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return "", fmt.Errorf("%w: got %s, expected %s", ErrChecksum, got, a.SHA256)
	}
	return path, nil
}

func downloadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("%w: %v", ErrDownload, err)
}

// progressWriter reports the running byte count at most once per 512 KiB.
type progressWriter struct {
	step     string
	fn       ProgressFunc
	total    int64
	reported int64
}

const progressStep = 512 << 10

func (w *progressWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if w.total-w.reported >= progressStep {
		w.flush()
	}
	return len(p), nil
}

func (w *progressWriter) flush() {
	w.reported = w.total
	w.fn(w.step, w.total)
}
