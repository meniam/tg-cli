package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestWriteFormatCLIParseAndLegacySnapshots(t *testing.T) {
	for _, mode := range writeParseModes {
		for _, edit := range []bool{false, true} {
			args := []string{"message", "send", "42", "--text", "hello", "--parse", mode}
			if edit {
				args = []string{"message", "edit", "42", "11", "--yes", "--text", "hello", "--parse", mode}
			}
			got := runWriteCLI(t, args, "", nil, nil)
			if got.code != 0 {
				t.Fatalf("%s edit=%v: %+v", mode, edit, got)
			}
			if edit {
				var p rpc.EditParams
				if err := json.Unmarshal(got.params, &p); err != nil || p.Parse != mode {
					t.Fatalf("edit params=%+v %v", p, err)
				}
			} else {
				var p rpc.SendParams
				if err := json.Unmarshal(got.params, &p); err != nil || p.Parse != mode {
					t.Fatalf("send params=%+v %v", p, err)
				}
			}
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"message", "send", "42", "--text", "plain"}, `{"chat":"42","text":"plain"}`},
		{[]string{"message", "edit", "42", "11", "--yes", "--text", "plain"}, `{"chat":"42","id":11,"text":"plain"}`},
	} {
		got := runWriteCLI(t, tc.args, "", nil, nil)
		if got.code != 0 || string(got.params) != tc.want || got.stdout != "{\"ok\":true,\"data\":{\"chat_id\":42,\"id\":11,\"date\":\"2026-11-14T10:00:00Z\"}}\n" {
			t.Fatalf("legacy snapshot: %+v", got)
		}
	}
}

func TestWriteFormatCLIEntitiesAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entities.json")
	if err := os.WriteFile(path, []byte(`[{"type":"bold","offset":0,"length":5}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []bool{false, true} {
		args := []string{"message", "send", "42", "--text", "hello", "--entities", path}
		if edit {
			args = []string{"message", "edit", "42", "11", "--yes", "--text", "hello", "--entities", path}
		}
		got := runWriteCLI(t, args, "", nil, nil)
		if got.code != 0 || !strings.Contains(string(got.params), `"entities":[{"type":"bold","offset":0,"length":5}]`) {
			t.Fatalf("entities: %+v", got)
		}
	}
	textFromStdin := runWriteCLI(t, []string{"message", "send", "42", "--entities", path}, "hello", nil, nil)
	if textFromStdin.code != 0 || !strings.Contains(string(textFromStdin.params), `"text":"hello"`) {
		t.Fatalf("text stdin with entity file: %+v", textFromStdin)
	}
	stdin := runWriteCLI(t, []string{"message", "send", "42", "--text", "hello", "--entities", "-"}, `[{"type":"bold","offset":0,"length":5}]`, nil, nil)
	if stdin.code != 0 || !strings.Contains(string(stdin.params), `"entities":[`) {
		t.Fatalf("stdin: %+v", stdin)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"mutual exclusion", []string{"message", "send", "42", "--text", "hi", "--parse", "html", "--entities", path}, "", "--parse and --entities"},
		{"stdin occupied", []string{"message", "send", "42", "--entities", "-"}, `[{"type":"bold","length":2}]`, "stdin is used for entities"},
		{"invalid mode", []string{"message", "edit", "42", "11", "--yes", "--text", "hi", "--parse", "bogus"}, "", "invalid --parse"},
		{"empty file", []string{"message", "send", "42", "--text", "hi", "--entities", path}, "", "entities JSON is empty"},
		{"bad json", []string{"message", "send", "42", "--text", "hi", "--entities", path}, "", "invalid entities JSON at byte"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "empty file" {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "bad json" {
				if err := os.WriteFile(path, []byte(`[{"type":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := runWriteCLI(t, tc.args, tc.stdin, nil, nil)
			if got.code != exitcode.Usage || got.method != "" || !strings.Contains(got.stdout, tc.want) {
				t.Fatalf("%s: %+v", tc.name, got)
			}
		})
	}
	long := strings.Repeat("a", 4097)
	formatted := runWriteCLI(t, []string{"message", "send", "42", "--parse", "html", "--text", long}, "", nil, nil)
	if formatted.code != 0 {
		t.Fatalf("classic source was rejected before parsing: %+v", formatted)
	}
	rich := runWriteCLI(t, []string{"message", "send", "42", "--parse", "rich-html", "--text", long}, "", nil, nil)
	if rich.code != exitcode.Usage || rich.method != "" {
		t.Fatalf("rich source limit: %+v", rich)
	}
}

func TestWriteFormatSchemaFlags(t *testing.T) {
	for _, name := range []string{"message send", "message edit"} {
		command := schemaCommands(t)[name]
		if command.Safety != "account_write" || command.Dangerous != (name == "message edit") {
			t.Fatalf("safety: %s %+v", name, command)
		}
		flags := map[string]flagDoc{}
		for _, flag := range command.Flags {
			flags[flag.Name] = flag
		}
		if _, ok := flags["entities"]; !ok {
			t.Fatalf("%s missing entities", name)
		}
		if !reflect.DeepEqual(flags["parse"].Values, writeParseModes) {
			t.Fatalf("%s modes=%v", name, flags["parse"].Values)
		}
	}
}
