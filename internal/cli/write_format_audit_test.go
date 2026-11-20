package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
)

// Every refusal of the entities input and of oversized stdin text is a usage
// error before the daemon is called, with its own message.
func TestAudit03WriteInputRefusals(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	object := write("object.json", `{"type":"bold"}`)
	empty := write("empty-array.json", ` [ ] `)
	huge := write("huge.json", "["+strings.Repeat(" ", maxEntitiesInputBytes)+"]")
	missing := filepath.Join(dir, "missing.json")
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"empty path", []string{"message", "send", "42", "--text", "hi", "--entities", ""}, "", "--entities needs a file path or -"},
		{"missing file", []string{"message", "send", "42", "--text", "hi", "--entities", missing}, "", "open entities file"},
		{"too large", []string{"message", "send", "42", "--text", "hi", "--entities", huge}, "", "entities JSON exceeds 256 KiB"},
		{"not an array", []string{"message", "send", "42", "--text", "hi", "--entities", object}, "", "entities must be a JSON array"},
		{"empty array", []string{"message", "send", "42", "--text", "hi", "--entities", empty}, "", "entities array is empty"},
		{"stdin text too long", []string{"message", "send", "42"}, strings.Repeat("x", maxTextInputBytes+1), "message text is too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, tc.args, tc.stdin, nil, nil)
			if r.code != exitcode.Usage || r.method != "" || !strings.Contains(r.stdout+r.stderr, tc.want) {
				t.Fatalf("want a usage error %q: %+v", tc.want, r)
			}
		})
	}
}

// Text exactly at the stdin limit is accepted.
func TestAudit03WriteStdinTextAtTheLimit(t *testing.T) {
	r := runWriteCLI(t, []string{"message", "send", "42", "--parse", "html"}, strings.Repeat("x", maxTextInputBytes), nil, nil)
	if r.code != 0 || r.method == "" {
		t.Fatalf("text at the limit was refused: %+v", r)
	}
}
