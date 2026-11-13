package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
)

func TestThrottleCLIOutputAndSchema(t *testing.T) {
	a, root := annotatedApp()
	out := a.deps.Stdout.(*bytes.Buffer)
	var list *cobra.Command
	walkCommands(root, func(cmd *cobra.Command, path string) {
		if path == "chat list" {
			list = cmd
		}
	})
	if list == nil {
		t.Fatal("chat list command is missing")
	}
	a.throttledMS = 25
	if err := root.PersistentFlags().Set("fields", "chats.id"); err != nil {
		t.Fatal(err)
	}
	items := []struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}{{7, "Ann"}}
	result := struct {
		Chats any `json:"chats"`
	}{items}
	if err := a.success(list, result); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), `{"ok":true,"data":{"chats":[{"id":7}]},"throttled_ms":25}`+"\n"; got != want {
		t.Fatalf("JSON with --fields = %q, want %q", got, want)
	}
	out.Reset()
	if err := root.PersistentFlags().Set("format", "jsonl"); err != nil {
		t.Fatal(err)
	}
	settings, err := a.outputSettingsFor(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := emitList(a, list, settings, result, items, "next"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), `{"id":7}`+"\n"+`{"next_cursor":"next","throttled_ms":25}`+"\n"; got != want {
		t.Fatalf("JSONL with --fields = %q, want %q", got, want)
	}
	out.Reset()
	a.failure(root, exitcode.FloodWait, "wait")
	if got, want := out.String(), `{"ok":false,"error":{"code":"flood_wait","message":"wait"},"throttled_ms":25}`+"\n"; got != want {
		t.Fatalf("failure = %q, want %q", got, want)
	}
	doc := schemaDocFor(root, a.specs)
	if !strings.Contains(doc.Output.Success, "throttled_ms") || !strings.Contains(doc.Output.Failure, "throttled_ms") {
		t.Fatalf("schema output omits optional wait: %+v", doc.Output)
	}
	if settings.format != output.FormatJSONL {
		t.Fatalf("format = %q", settings.format)
	}
}
