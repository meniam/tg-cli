package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/schema"
)

// annotatedApp builds the command tree and returns the app that holds the
// annotations of its commands.
func annotatedApp() (*app, *cobra.Command) {
	a := &app{deps: Deps{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, specs: map[*cobra.Command]commandSpec{}}
	return a, a.rootCommand()
}

// TestEveryCommandIsAnnotated fails when a runnable command has no spec or a
// spec that an agent cannot use: an unknown safety class, no declared result
// type, no explicit risk choice, or no annotation at all. It is the guard that
// keeps `schema` true.
func TestEveryCommandIsAnnotated(t *testing.T) {
	a, root := annotatedApp()

	known := map[safetyClass]bool{safetyRead: true, safetyLocalWrite: true, safetyAccountWrite: true}
	seen := 0
	walkCommands(root, func(cmd *cobra.Command, path string) {
		seen++
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
		// Every result type has to produce a field list, otherwise an agent
		// cannot ask for its fields.
		if len(schema.Fields(spec.Result)) == 0 {
			t.Errorf("%s: the field list of %v is empty", path, spec.Result)
		}
		// A command with JSONL says where the items live, and one without it
		// does not pretend to have them.
		hasJSONL := slices.Contains(commandFormats(spec), string(output.FormatJSONL))
		if hasJSONL != (spec.ItemPath != "") {
			t.Errorf("%s: format jsonl and item path %q disagree", path, spec.ItemPath)
		}
		if !spec.Risk.known() {
			t.Errorf("%s: risk %q was not chosen, want noRisk or dangerous", path, spec.Risk)
		}
		if spec.Risk == dangerous && spec.Safety == safetyRead {
			t.Errorf("%s: a read-only command cannot be dangerous", path)
		}
	})
	if seen == 0 {
		t.Fatal("the command tree is empty")
	}
}

// TestNoteDeleteIsDangerous keeps the one classification the decision names: a
// command that removes data in bulk must say so, whatever its safety class is.
func TestNoteDeleteIsDangerous(t *testing.T) {
	a, root := annotatedApp()
	found := false
	walkCommands(root, func(cmd *cobra.Command, path string) {
		if path == "note delete" {
			found = true
			if a.specs[cmd].Risk != dangerous {
				t.Error("note delete must be marked dangerous")
			}
		}
	})
	if !found {
		t.Fatal("note delete is not in the command tree")
	}
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
	if len(doc.Commands) == 0 || len(doc.ExitCodes) == 0 {
		t.Errorf("schema is incomplete: %d commands, %d exit codes", len(doc.Commands), len(doc.ExitCodes))
	}
	// Entries are sorted, so the order does not depend on map iteration.
	for i := 1; i < len(doc.Commands); i++ {
		if doc.Commands[i-1].Path >= doc.Commands[i].Path {
			t.Fatalf("commands are not sorted: %q before %q", doc.Commands[i-1].Path, doc.Commands[i].Path)
		}
	}
}
