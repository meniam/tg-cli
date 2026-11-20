package gigaam

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEngine(t *testing.T, p Paths) {
	t.Helper()
	for _, f := range []string{p.Binary, p.Encoder, p.Decoder, p.Joiner, p.Tokens, p.VAD, filepath.Join(p.LibDir, "libx.dylib")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if f == p.Binary {
			mode = 0o700
		}
		if err := os.WriteFile(f, []byte("data"), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewPathsLayout(t *testing.T) {
	p := NewPaths("/h")
	if p.Root != "/h/models/gigaam" {
		t.Errorf("Root = %q", p.Root)
	}
	if p.Binary != "/h/models/gigaam/runtime/bin/sherpa-onnx-vad-with-offline-asr" {
		t.Errorf("Binary = %q", p.Binary)
	}
	if p.LibDir != "/h/models/gigaam/runtime/lib" || p.Encoder != "/h/models/gigaam/model/encoder.int8.onnx" ||
		p.VAD != "/h/models/gigaam/silero_vad.onnx" {
		t.Errorf("unexpected layout: %+v", p)
	}
}

func TestCheckEmptyRootReportsEverything(t *testing.T) {
	p := NewPaths(t.TempDir())
	problems := Check(p)
	// binary, lib, four model files, vad
	if len(problems) != 7 {
		t.Fatalf("problems = %v, want 7", problems)
	}
	for _, pr := range problems {
		if pr.Reason != "missing" {
			t.Errorf("%v: reason %q, want missing", pr, pr.Reason)
		}
	}
	if Ready(p) {
		t.Error("Ready on an empty root")
	}
}

func TestCheckReadyInstallation(t *testing.T) {
	p := NewPaths(t.TempDir())
	writeEngine(t, p)
	if problems := Check(p); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if !Ready(p) {
		t.Error("Ready = false")
	}
}

func TestCheckReportsEmptyAndNotExecutable(t *testing.T) {
	p := NewPaths(t.TempDir())
	writeEngine(t, p)
	if err := os.WriteFile(p.Joiner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Binary, 0o600); err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, pr := range Check(p) {
		reasons[pr.Path] = pr.Reason
	}
	if reasons[p.Joiner] != "empty" || reasons[p.Binary] != "not_executable" || len(reasons) != 2 {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestCheckDirectoryInsteadOfFile(t *testing.T) {
	p := NewPaths(t.TempDir())
	writeEngine(t, p)
	if err := os.Remove(p.VAD); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p.VAD, 0o700); err != nil {
		t.Fatal(err)
	}
	problems := Check(p)
	if len(problems) != 1 || problems[0].Reason != "not_a_file" {
		t.Fatalf("problems = %v", problems)
	}
}

func TestCheckEmptyLibDir(t *testing.T) {
	p := NewPaths(t.TempDir())
	writeEngine(t, p)
	if err := os.Remove(filepath.Join(p.LibDir, "libx.dylib")); err != nil {
		t.Fatal(err)
	}
	problems := Check(p)
	if len(problems) != 1 || problems[0].Path != p.LibDir || problems[0].Reason != "empty" {
		t.Fatalf("problems = %v", problems)
	}
}
