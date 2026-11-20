// Package gigaam locates, checks and installs the local speech recognition
// engine: the sherpa-onnx runtime, the GigaAM v3 model and the Silero VAD model.
package gigaam

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Directory and file names inside the engine root. The install code and the
// layout of Paths both rely on them.
const (
	runtimeDirName = "runtime"
	modelDirName   = "model"
	vadFileName    = "silero_vad.onnx"

	// BinaryName is the sherpa-onnx program that runs VAD and offline ASR.
	BinaryName = "sherpa-onnx-vad-with-offline-asr"
)

// Paths holds every path the engine needs. All of them live under Root.
type Paths struct {
	// Root is <home>/models/gigaam.
	Root string
	// RuntimeDir holds bin/ and lib/ of the sherpa-onnx build.
	RuntimeDir string
	// Binary is the sherpa-onnx-vad-with-offline-asr program.
	Binary string
	// LibDir is the runtime lib directory (DYLD_LIBRARY_PATH).
	LibDir string
	// ModelDir holds the four model files.
	ModelDir string
	Encoder  string
	Decoder  string
	Joiner   string
	Tokens   string
	// VAD is silero_vad.onnx.
	VAD string
}

// NewPaths builds the engine layout under <home>/models/gigaam.
func NewPaths(home string) Paths {
	return pathsFromRoot(filepath.Join(home, "models", "gigaam"))
}

func pathsFromRoot(root string) Paths {
	runtime := filepath.Join(root, runtimeDirName)
	model := filepath.Join(root, modelDirName)
	return Paths{
		Root:       root,
		RuntimeDir: runtime,
		Binary:     filepath.Join(runtime, "bin", BinaryName),
		LibDir:     filepath.Join(runtime, "lib"),
		ModelDir:   model,
		Encoder:    filepath.Join(model, "encoder.int8.onnx"),
		Decoder:    filepath.Join(model, "decoder.onnx"),
		Joiner:     filepath.Join(model, "joiner.onnx"),
		Tokens:     filepath.Join(model, "tokens.txt"),
		VAD:        filepath.Join(root, vadFileName),
	}
}

// Problem names one thing missing from the engine installation.
type Problem struct {
	// Component is "runtime", "model" or "vad".
	Component string
	// Path is the file that failed the check.
	Path string
	// Reason is "missing", "not_executable", "empty" or "not_a_file".
	Reason string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s: %s (%s)", p.Component, p.Path, p.Reason)
}

// Check reports what is missing from the installation without running any
// program. An empty result means the engine is ready.
func Check(p Paths) []Problem {
	var problems []Problem
	problems = append(problems, checkComponent(ComponentRuntime, p)...)
	problems = append(problems, checkComponent(ComponentModel, p)...)
	problems = append(problems, checkComponent(ComponentVAD, p)...)
	return problems
}

// Ready reports whether Check finds nothing missing.
func Ready(p Paths) bool { return len(Check(p)) == 0 }

// Names of the installable components.
const (
	ComponentRuntime = "runtime"
	ComponentModel   = "model"
	ComponentVAD     = "vad"
)

func checkComponent(component string, p Paths) []Problem {
	var problems []Problem
	switch component {
	case ComponentRuntime:
		problems = appendProblem(problems, component, p.Binary, true)
		problems = appendLibProblems(problems, p.LibDir)
	case ComponentModel:
		for _, f := range []string{p.Encoder, p.Decoder, p.Joiner, p.Tokens} {
			problems = appendProblem(problems, component, f, false)
		}
	case ComponentVAD:
		problems = appendProblem(problems, component, p.VAD, false)
	}
	return problems
}

// appendLibProblems requires the lib directory to hold at least one non-empty
// file; the exact library names belong to the pinned runtime build.
func appendLibProblems(problems []Problem, libDir string) []Problem {
	entries, err := os.ReadDir(libDir)
	if err != nil {
		return append(problems, Problem{Component: ComponentRuntime, Path: libDir, Reason: "missing"})
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err == nil && info.Size() > 0 {
			found = true
			break
		}
	}
	if !found {
		problems = append(problems, Problem{Component: ComponentRuntime, Path: libDir, Reason: "empty"})
	}
	return problems
}

func appendProblem(problems []Problem, component, path string, executable bool) []Problem {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return append(problems, Problem{Component: component, Path: path, Reason: "missing"})
	case !info.Mode().IsRegular():
		return append(problems, Problem{Component: component, Path: path, Reason: "not_a_file"})
	case info.Size() == 0:
		return append(problems, Problem{Component: component, Path: path, Reason: "empty"})
	case executable && info.Mode().Perm()&0o100 == 0:
		return append(problems, Problem{Component: component, Path: path, Reason: "not_executable"})
	}
	return problems
}
