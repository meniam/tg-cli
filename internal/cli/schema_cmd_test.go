package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
)

// TestEveryCommandIsAnnotated fails when a runnable command has no spec or a
// spec that an agent cannot use: an unknown safety class, no declared result
// type, or no annotation at all. It is the guard that keeps `schema` true.
func TestEveryCommandIsAnnotated(t *testing.T) {
	a := &app{deps: Deps{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, specs: map[*cobra.Command]commandSpec{}}
	root := a.rootCommand()

	known := map[safetyClass]bool{safetyRead: true, safetyLocalWrite: true, safetyAccountWrite: true}
	seen := map[*cobra.Command]bool{}
	walkCommands(root, func(cmd *cobra.Command, path string) {
		seen[cmd] = true
		spec, ok := a.specs[cmd]
		if !ok {
			t.Errorf("%s: runnable command without an annotation", path)
			return
		}
		if !known[spec.Safety] {
			t.Errorf("%s: unknown safety class %q, want read, local_write or account_write", path, spec.Safety)
		}
		if spec.Result == nil || spec.Result.Kind() != reflect.Struct {
			t.Errorf("%s: the result type must be a struct, got %v", path, spec.Result)
		}
		if spec.Dangerous && spec.Safety == safetyRead {
			t.Errorf("%s: a read-only command cannot be dangerous", path)
		}
		if want := len(spec.Args); want > 0 && !strings.Contains(cmd.Use, "<") {
			t.Errorf("%s: %d annotated arguments but the usage names none: %q", path, want, cmd.Use)
		}
	})
	if len(seen) == 0 {
		t.Fatal("the command tree is empty")
	}
	// A spec of a command that is no longer runnable is dead weight.
	for cmd := range a.specs {
		if !seen[cmd] {
			t.Errorf("%q is annotated but not an agent-facing command", cmd.Name())
		}
	}
}

// TestDangerousCommandsAreMarked keeps the hand-made classification honest where
// it matters most: a command that removes data must say so.
func TestDangerousCommandsAreMarked(t *testing.T) {
	dangerous := []string{"note delete"}
	if got := dangerousCommands(); !reflect.DeepEqual(got, dangerous) {
		t.Errorf("dangerous commands: got %v, want %v; review the annotation of every change", got, dangerous)
	}
}

func dangerousCommands() []string {
	a := &app{deps: Deps{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, specs: map[*cobra.Command]commandSpec{}}
	var paths []string
	walkCommands(a.rootCommand(), func(cmd *cobra.Command, path string) {
		if a.specs[cmd].Dangerous {
			paths = append(paths, path)
		}
	})
	return paths
}

// TestSchemaNeedsNoDaemonAndIsDeterministic runs `schema` with a home directory
// that holds no daemon and no profile: the command must answer from the command
// tree alone, and the same build must print the same bytes.
func TestSchemaNeedsNoDaemonAndIsDeterministic(t *testing.T) {
	e := newEnv(t) // no daemon is started, TG_CLI_HOME points at an empty directory

	first := e.run("schema")
	if first.code != exitcode.OK || !parse(t, first).OK {
		t.Fatalf("schema: exit %d, %q, stderr %q", first.code, first.stdout, first.stderr)
	}
	second := e.run("schema", "--profile", "work")
	if second.code != exitcode.OK {
		t.Fatalf("schema on another profile: exit %d, %q", second.code, second.stdout)
	}
	if first.stdout != second.stdout {
		t.Error("schema must print the same bytes on two runs")
	}
	if first.stderr != "" {
		t.Errorf("schema must not write to stderr: %q", first.stderr)
	}
	if strings.Contains(first.stdout, e.home) {
		t.Errorf("schema must not depend on the profile directory: %q", first.stdout)
	}

	var doc schemaDoc
	if err := json.Unmarshal(parse(t, first).Data, &doc); err != nil {
		t.Fatalf("schema is not the documented shape: %v", err)
	}
	if doc.SchemaVersion != schemaVersion {
		t.Errorf("schema_version = %d, want %d", doc.SchemaVersion, schemaVersion)
	}
	if doc.Tool.Name != "tg-cli" || doc.Tool.Version == "" {
		t.Errorf("tool = %+v", doc.Tool)
	}
	if len(doc.Commands) == 0 || len(doc.ExitCodes) != 8 || len(doc.Output.Formats) == 0 {
		t.Errorf("schema is incomplete: %d commands, %d exit codes, %v formats", len(doc.Commands), len(doc.ExitCodes), doc.Output.Formats)
	}
	// Entries are sorted, so the order does not depend on map iteration.
	for i := 1; i < len(doc.Commands); i++ {
		if doc.Commands[i-1].Path >= doc.Commands[i].Path {
			t.Fatalf("commands are not sorted: %q before %q", doc.Commands[i-1].Path, doc.Commands[i].Path)
		}
	}
	// Every command in the document names what an agent needs to pick it.
	for _, cmd := range doc.Commands {
		if cmd.Summary == "" || cmd.Safety == "" || cmd.Result == "" {
			t.Errorf("%q is incomplete: %+v", cmd.Path, cmd)
		}
	}
	// The fixed value lists travel with the flags, so an agent needs no docs.
	values := map[string][]string{}
	for _, cmd := range doc.Commands {
		for _, f := range cmd.Flags {
			if len(f.Values) > 0 {
				values[cmd.Path+" "+f.Name] = f.Values
			}
		}
	}
	for key, want := range map[string][]string{
		"chat list format":   outputFormats(),
		"search scope":       searchScopes(),
		"note delete author": {"owner", "ai"},
	} {
		if got := values[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("schema values of %q = %v, want %v", key, got, want)
		}
	}
}

// TestVersionDescribesTheBuild checks that `version` needs nothing but the
// binary and reports the version the build set.
func TestVersionDescribesTheBuild(t *testing.T) {
	e := newEnv(t)
	r := e.run("version")
	if r.code != exitcode.OK || !parse(t, r).OK {
		t.Fatalf("version: exit %d, %q", r.code, r.stdout)
	}
	var info struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(parse(t, r).Data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Version == "" {
		t.Error("version must be set")
	}
	if r.stderr != "" {
		t.Errorf("version must not write to stderr: %q", r.stderr)
	}
}
