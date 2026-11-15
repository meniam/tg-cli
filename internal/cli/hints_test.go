package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
)

func TestCommandHintsAndSchemaFilter(t *testing.T) {
	e := newEnv(t)
	group := e.run("chat")
	if group.code != exitcode.Usage || !strings.Contains(group.stdout, "choose a subcommand:") {
		t.Fatalf("group: %+v", group)
	}
	help := e.run("chat", "--help")
	if help.code != exitcode.OK || !strings.Contains(help.stdout, "Usage:") {
		t.Fatalf("help: %+v", help)
	}
	usage := e.run("message", "send")
	if usage.code != exitcode.Usage || !strings.Contains(usage.stdout, "usage: tg message send") {
		t.Fatalf("usage: %+v", usage)
	}
	result := e.run("schema", "chat")
	if result.code != exitcode.OK {
		t.Fatalf("schema: %+v", result)
	}
	var doc schemaDoc
	if err := json.Unmarshal(parse(t, result).Data, &doc); err != nil || len(doc.Commands) == 0 {
		t.Fatalf("schema: %+v %v", doc, err)
	}
	for _, c := range doc.Commands {
		if !strings.HasPrefix(c.Path, "chat ") {
			t.Fatalf("unexpected path: %s", c.Path)
		}
	}
	if r := e.run("schema", "nonexistent"); r.code != exitcode.Usage {
		t.Fatalf("unknown: %+v", r)
	}
}
