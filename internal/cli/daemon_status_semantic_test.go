package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

func TestSemanticSearchStatusAppearsInSchemaAndHuman(t *testing.T) {
	cmd, ok := schemaCommands(t)["daemon status"]
	if !ok {
		t.Fatal("daemon status missing")
	}
	want := map[string]bool{
		"semantic_search": false, "semantic_search.enabled": false, "semantic_search.state": false,
		"semantic_search.reason": false, "semantic_search.model": false, "semantic_search.indexed": false,
		"semantic_search.total": false, "semantic_search.finished_at": false, "semantic_search.next_run_at": false,
	}
	for _, field := range cmd.Fields {
		if _, ok := want[field.Path]; ok {
			want[field.Path] = true
		}
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("missing schema field %s", path)
		}
	}

	var stdout bytes.Buffer
	a := &app{deps: Deps{Stdout: &stdout}, specs: map[*cobra.Command]commandSpec{}}
	root := a.rootCommand()
	status, _, err := root.Find([]string{"daemon", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("human", "true"); err != nil {
		t.Fatal(err)
	}
	block := rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexIndexing, Model: "bge-m3", Indexed: 6382, Total: 6500}
	if err := a.success(status, rpc.StatusResult{SemanticSearch: block}); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{
		`(?m)^semantic_search\s*:$`, `(?m)^  enabled\s*:\s+true$`, `(?m)^  state\s*:\s+indexing$`,
		`(?m)^  model\s*:\s+bge-m3$`, `(?m)^  indexed\s*:\s+6382$`, `(?m)^  total\s*:\s+6500$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(stdout.String()) {
			t.Errorf("human status missing %q: %q", pattern, stdout.String())
		}
	}
}
