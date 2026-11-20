package gigaam

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// entry describes one tar member of a test archive.
type entry struct {
	name string
	typ  byte
	body string
	link string
	mode int64
}

func file(name, body string) entry {
	return entry{name: name, typ: tar.TypeReg, body: body, mode: 0o644}
}
func dir(name string) entry { return entry{name: name, typ: tar.TypeDir, mode: 0o755} }

// makeArchive builds a tar.bz2. The standard library has no bzip2 encoder, so
// the test runs the system bzip2 over a tar stream and skips when it is absent.
func makeArchive(t *testing.T, entries []entry) []byte {
	t.Helper()
	bzip2, err := exec.LookPath("bzip2")
	if err != nil {
		t.Skip("bzip2 is not installed; cannot build test archives")
	}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link}
		if e.typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bzip2, "-c")
	cmd.Stdin = &raw
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func runtimeEntries() []entry {
	return []entry{
		dir("rt"), dir("rt/bin"), dir("rt/lib"), dir("rt/include"),
		file("rt/lib/libx.dylib", "lib"),
		file("rt/include/x.h", "header"),
		file("rt/bin/"+BinaryName, "#!/bin/sh\n"),
		file("rt/bin/other-tool", "other"),
	}
}

func modelEntries() []entry {
	return []entry{
		dir("md"),
		file("md/encoder.int8.onnx", "enc"),
		file("md/decoder.onnx", "dec"),
		file("md/joiner.onnx", "join"),
		file("md/tokens.txt", "tok"),
		file("md/README.md", "readme"),
		dir("md/test_wavs"),
		file("md/test_wavs/example.wav", "wav"),
	}
}

// fixture serves test artifacts and counts requests per path.
type fixture struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	hits      map[string]int
	blobs     map[string][]byte
	artifacts []Artifact
	paths     Paths
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, hits: map[string]int{}, blobs: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		blob, ok := f.blobs[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(blob)
	}))
	t.Cleanup(f.srv.Close)
	f.paths = NewPaths(filepath.Join(t.TempDir(), "home"))
	return f
}

// add registers a blob and returns its artifact.
func (f *fixture) add(component, urlPath string, data []byte, top string, keep []string) Artifact {
	f.mu.Lock()
	f.blobs[urlPath] = data
	f.mu.Unlock()
	a := Artifact{
		Component:   component,
		URL:         f.srv.URL + urlPath,
		Size:        int64(len(data)),
		SHA256:      sum(data),
		TopDir:      top,
		Keep:        keep,
		MaxUnpacked: 1 << 20,
	}
	f.artifacts = append(f.artifacts, a)
	return a
}

// addAll registers a valid runtime, model and VAD.
func (f *fixture) addAll() {
	f.add(ComponentRuntime, "/rt.tar.bz2", makeArchive(f.t, runtimeEntries()), "rt", []string{"bin/", "lib/"})
	f.add(ComponentModel, "/md.tar.bz2", makeArchive(f.t, modelEntries()), "md",
		[]string{"encoder.int8.onnx", "decoder.onnx", "joiner.onnx", "tokens.txt"})
	f.add(ComponentVAD, "/vad.onnx", []byte("vad-model"), "", nil)
}

func (f *fixture) install(ctx context.Context, progress ProgressFunc) error {
	return Install(ctx, Options{Paths: f.paths, Artifacts: f.artifacts, Progress: progress})
}

func (f *fixture) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func rootNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func wantNames(t *testing.T, root string, want ...string) {
	t.Helper()
	got := rootNames(t, root)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("root entries = %v, want %v", got, want)
	}
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestInstallFreshInstallPermissionsAndLayout(t *testing.T) {
	f := newFixture(t)
	f.addAll()
	var steps []string
	err := f.install(context.Background(), func(step string, _ int64) { steps = append(steps, step) })
	if err != nil {
		t.Fatal(err)
	}
	if problems := Check(f.paths); len(problems) != 0 {
		t.Fatalf("Check after install: %v", problems)
	}

	for path, want := range map[string]os.FileMode{
		f.paths.Root:                             0o700,
		f.paths.RuntimeDir:                       0o700,
		f.paths.LibDir:                           0o700,
		filepath.Join(f.paths.RuntimeDir, "bin"): 0o700,
		f.paths.ModelDir:                         0o700,
		f.paths.Binary:                           0o700,
		filepath.Join(f.paths.RuntimeDir, "bin", "other-tool"): 0o700,
		filepath.Join(f.paths.LibDir, "libx.dylib"):            0o600,
		f.paths.Encoder: 0o600,
		f.paths.Tokens:  0o600,
		f.paths.VAD:     0o600,
	} {
		if got := perm(t, path); got != want {
			t.Errorf("mode of %s = %o, want %o", path, got, want)
		}
	}

	// Entries outside Keep are not written.
	for _, absent := range []string{
		filepath.Join(f.paths.RuntimeDir, "include"),
		filepath.Join(f.paths.ModelDir, "README.md"),
		filepath.Join(f.paths.ModelDir, "test_wavs"),
	} {
		if _, err := os.Stat(absent); err == nil {
			t.Errorf("%s must not be unpacked", absent)
		}
	}
	// No temporary leftovers in the root.
	wantNames(t, f.paths.Root, "model", "runtime", "silero_vad.onnx")

	if !contains(steps, "runtime:download") || !contains(steps, "model:extract") || !contains(steps, "vad:install") {
		t.Errorf("progress steps = %v", steps)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestInstallRejectsBadChecksumBeforeUnpacking(t *testing.T) {
	f := newFixture(t)
	// The body is not even a valid archive: if unpacking started first, the
	// error would be ErrUnsafeArchive, not ErrChecksum.
	f.add(ComponentRuntime, "/rt.tar.bz2", []byte("definitely not bzip2 data"), "rt", []string{"bin/"})
	f.artifacts[0].SHA256 = sum([]byte("something else"))

	err := f.install(context.Background(), nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("error = %v, want ErrChecksum", err)
	}
	wantNames(t, f.paths.Root)
}

func TestInstallRejectsWrongSize(t *testing.T) {
	f := newFixture(t)
	data := makeArchive(t, runtimeEntries())
	f.add(ComponentRuntime, "/rt.tar.bz2", data, "rt", []string{"bin/", "lib/"})

	f.artifacts[0].Size = int64(len(data)) + 10 // server announces fewer bytes than pinned
	err := f.install(context.Background(), nil)
	if !errors.Is(err, ErrSize) {
		t.Fatalf("error = %v, want ErrSize", err)
	}
	wantNames(t, f.paths.Root)
}

func TestInstallInterruptedDownload(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Announce the full length, send half and cut the connection.
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 4096\r\n\r\n")
		_, _ = buf.Write(data[:2048])
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)

	paths := NewPaths(filepath.Join(t.TempDir(), "home"))
	err := Install(context.Background(), Options{Paths: paths, Artifacts: []Artifact{{
		Component: ComponentVAD, URL: srv.URL + "/vad.onnx", Size: 4096, SHA256: sum(data),
	}}})
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("error = %v, want ErrDownload", err)
	}
	wantNames(t, paths.Root)
}

func TestInstallNetworkUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/vad.onnx"
	srv.Close() // nothing listens any more

	paths := NewPaths(filepath.Join(t.TempDir(), "home"))
	err := Install(context.Background(), Options{Paths: paths, Artifacts: []Artifact{{
		Component: ComponentVAD, URL: url, Size: 3, SHA256: sum([]byte("abc")),
	}}})
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("error = %v, want ErrDownload", err)
	}
	if !strings.Contains(err.Error(), "install vad") {
		t.Errorf("error does not name the component: %v", err)
	}
	wantNames(t, paths.Root)
}

func TestInstallRejectsBadStatus(t *testing.T) {
	f := newFixture(t) // no blobs: every path answers 404
	f.artifacts = []Artifact{{Component: ComponentVAD, URL: f.srv.URL + "/missing", Size: 3, SHA256: sum([]byte("abc"))}}
	err := f.install(context.Background(), nil)
	if !errors.Is(err, ErrDownload) || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v, want ErrDownload with status 404", err)
	}
}

func TestInstallRejectsUnsafeArchives(t *testing.T) {
	big := strings.Repeat("a", 2048)
	cases := []struct {
		name    string
		entries []entry
		max     int64
	}{
		{"dotdot at start", []entry{dir("rt"), file("../evil", "x")}, 0},
		{"dotdot inside top", []entry{dir("rt"), file("rt/../../evil", "x")}, 0},
		{"absolute path", []entry{dir("rt"), file("/tmp/evil", "x")}, 0},
		{"outside top dir", []entry{dir("rt"), file("other/x", "x")}, 0},
		{"top-level file", []entry{file("rt", "x")}, 0},
		{"symlink", []entry{dir("rt"), {name: "rt/lib", typ: tar.TypeSymlink, link: "/etc", mode: 0o777}}, 0},
		{"hardlink", []entry{dir("rt"), {name: "rt/lib", typ: tar.TypeLink, link: "rt/bin/x", mode: 0o644}}, 0},
		{"char device", []entry{dir("rt"), {name: "rt/dev", typ: tar.TypeChar, mode: 0o644}}, 0},
		{"fifo", []entry{dir("rt"), {name: "rt/fifo", typ: tar.TypeFifo, mode: 0o644}}, 0},
		{"unpacked size over limit", []entry{dir("rt"), file("rt/bin/a", big)}, 1024},
		{"duplicate entry", []entry{dir("rt"), file("rt/bin/a", "1"), file("rt/bin/a", "2")}, 0},
		{"unsafe entry after valid ones", append(runtimeEntries(), file("rt/lib/../../x", "x")), 0},
		{"link outside Keep is still rejected", []entry{dir("rt"), {name: "rt/include/l", typ: tar.TypeSymlink, link: "/etc", mode: 0o777}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			data := makeArchive(t, tc.entries)
			f.add(ComponentRuntime, "/rt.tar.bz2", data, "rt", []string{"bin/", "lib/"})
			if tc.max > 0 {
				f.artifacts[0].MaxUnpacked = tc.max
			}
			err := f.install(context.Background(), nil)
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("error = %v, want ErrUnsafeArchive", err)
			}
			// Nothing leaked outside the root and no half install remains.
			wantNames(t, f.paths.Root)
			parent := filepath.Dir(f.paths.Root) // <tmp>/home/models
			if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
				t.Fatal("file written outside the install root")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(parent), "evil")); err == nil {
				t.Fatal("file written outside the install root")
			}
		})
	}
}

func TestInstallIncompleteComponentIsRejected(t *testing.T) {
	f := newFixture(t)
	// The archive is valid but has no sherpa-onnx-vad-with-offline-asr.
	data := makeArchive(t, []entry{dir("rt"), file("rt/bin/other-tool", "x"), file("rt/lib/libx.dylib", "x")})
	f.add(ComponentRuntime, "/rt.tar.bz2", data, "rt", []string{"bin/", "lib/"})
	err := f.install(context.Background(), nil)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("error = %v, want ErrIncomplete", err)
	}
	wantNames(t, f.paths.Root)
}

func TestInstallSecondRunDownloadsNothing(t *testing.T) {
	f := newFixture(t)
	f.addAll()
	if err := f.install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/rt.tar.bz2", "/md.tar.bz2", "/vad.onnx"} {
		if n := f.hitCount(p); n != 1 {
			t.Fatalf("first run: %s fetched %d times", p, n)
		}
	}
	var steps []string
	if err := f.install(context.Background(), func(s string, _ int64) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/rt.tar.bz2", "/md.tar.bz2", "/vad.onnx"} {
		if n := f.hitCount(p); n != 1 {
			t.Errorf("second run fetched %s again (%d)", p, n)
		}
	}
	if !contains(steps, "runtime:skip") || !contains(steps, "model:skip") || !contains(steps, "vad:skip") {
		t.Errorf("steps = %v, want three skips", steps)
	}
}

func TestInstallFetchesOnlyWhatIsMissing(t *testing.T) {
	f := newFixture(t)
	f.addAll()
	if err := f.install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.paths.Tokens); err != nil {
		t.Fatal(err)
	}
	if err := f.install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if n := f.hitCount("/md.tar.bz2"); n != 2 {
		t.Errorf("model fetched %d times, want 2", n)
	}
	if n := f.hitCount("/rt.tar.bz2"); n != 1 {
		t.Errorf("runtime fetched %d times, want 1", n)
	}
	if n := f.hitCount("/vad.onnx"); n != 1 {
		t.Errorf("vad fetched %d times, want 1", n)
	}
	if problems := Check(f.paths); len(problems) != 0 {
		t.Fatalf("Check: %v", problems)
	}
}

func TestInstallFailureKeepsPreviousInstallation(t *testing.T) {
	f := newFixture(t)
	f.addAll()
	// A previous, incomplete model directory: encoder is empty, so Check
	// reports the model missing and Install tries to replace it.
	if err := os.MkdirAll(f.paths.ModelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.paths.ModelDir, "old.txt")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.Encoder, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for i, a := range f.artifacts {
		if a.Component == ComponentModel {
			f.artifacts[i].SHA256 = sum([]byte("tampered"))
		}
	}
	err := f.install(context.Background(), nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("error = %v, want ErrChecksum", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("previous installation lost: %v", err)
	}
	for _, n := range rootNames(t, f.paths.Root) {
		if strings.HasPrefix(n, ".") {
			t.Errorf("temporary entry %q left behind", n)
		}
	}

	// A good archive then replaces the old directory completely.
	for i, a := range f.artifacts {
		if a.Component == ComponentModel {
			f.artifacts[i].SHA256 = sum(f.blobs["/md.tar.bz2"])
		}
	}
	if err := f.install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("old file survived the replacement")
	}
	if problems := Check(f.paths); len(problems) != 0 {
		t.Fatalf("Check: %v", problems)
	}
	for _, n := range rootNames(t, f.paths.Root) {
		if strings.HasPrefix(n, ".") {
			t.Errorf("temporary entry %q left behind", n)
		}
	}
}

func TestReplaceDirRestoresOldWhenSwapFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "model")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The staged directory does not exist, so the second rename fails.
	if err := replaceDir(filepath.Join(root, "missing-stage"), target); err == nil {
		t.Fatal("replaceDir succeeded without a staged directory")
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); err != nil {
		t.Fatalf("old directory not restored: %v", err)
	}
	wantNames(t, root, "model")
}

func TestInstallCancelCleansTemporaryFiles(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done() // hold the transfer open until the client leaves
	}))
	t.Cleanup(srv.Close)

	paths := NewPaths(filepath.Join(t.TempDir(), "home"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		done <- Install(ctx, Options{Paths: paths, Artifacts: []Artifact{{
			Component: ComponentVAD, URL: srv.URL + "/vad.onnx", Size: 1 << 20, SHA256: sum([]byte("x")),
		}}})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Install did not return after cancellation")
	}
	wantNames(t, paths.Root)
}

func TestInstallCancelDuringExtraction(t *testing.T) {
	f := newFixture(t)
	f.addAll()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := f.install(ctx, func(step string, _ int64) {
		if step == "runtime:extract" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	wantNames(t, f.paths.Root)
}

func TestInstallCleansStaleEntries(t *testing.T) {
	f := newFixture(t)
	f.addAll()
	if err := os.MkdirAll(filepath.Join(f.paths.Root, ".stage-dead", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.paths.Root, ".download-dead"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A swap killed between its two renames leaves only the .old- directory.
	oldRuntime := filepath.Join(f.paths.Root, ".old-runtime")
	if err := os.MkdirAll(filepath.Join(oldRuntime, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRuntime, "bin", "marker"), []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Serve only model and vad: the restored runtime directory is incomplete,
	// so we only check that the restore happened by looking before the swap.
	cleanStale(f.paths.Root)
	if _, err := os.Stat(filepath.Join(f.paths.RuntimeDir, "bin", "marker")); err != nil {
		t.Fatalf(".old-runtime was not restored: %v", err)
	}
	wantNames(t, f.paths.Root, "runtime")
}

func TestInstallEmptyRoot(t *testing.T) {
	if err := Install(context.Background(), Options{}); err == nil {
		t.Fatal("Install with an empty root succeeded")
	}
}

func TestPinnedTable(t *testing.T) {
	arts := Pinned()
	if len(arts) != 3 {
		t.Fatalf("Pinned has %d artifacts, want 3", len(arts))
	}
	seen := map[string]bool{}
	for _, a := range arts {
		seen[a.Component] = true
		if !strings.HasPrefix(a.URL, "https://github.com/k2-fsa/sherpa-onnx/releases/download/") {
			t.Errorf("%s: URL %q is not a pinned k2-fsa release", a.Component, a.URL)
		}
		if len(a.SHA256) != 64 || a.Size <= 0 {
			t.Errorf("%s: bad size or digest", a.Component)
		}
		if a.Component != ComponentVAD && (a.TopDir == "" || a.MaxUnpacked <= 0 || len(a.Keep) == 0) {
			t.Errorf("%s: archive limits are not set", a.Component)
		}
	}
	for _, c := range []string{ComponentRuntime, ComponentModel, ComponentVAD} {
		if !seen[c] {
			t.Errorf("component %s missing", c)
		}
	}
}
