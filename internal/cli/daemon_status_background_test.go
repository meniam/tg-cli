package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

func TestBackgroundSyncStatusAppearsInSchemaAndHuman(t *testing.T) {
	cmd, ok := schemaCommands(t)["daemon status"]
	if !ok {
		t.Fatal("daemon status missing")
	}
	want := map[string]bool{
		"background_sync": false, "background_sync.enabled": false, "background_sync.window": false,
		"background_sync.window.start": false, "background_sync.window.end": false,
		"background_sync.state": false, "background_sync.next_run_at": false, "background_sync.last_run_at": false,
		"background_sync.last_result": false, "background_sync.current_chat_id": false,
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
	next, chat := int64(1800000000), int64(-1001)
	block := rpc.BackgroundSyncStatus{
		Enabled: true, Window: rpc.BackgroundSyncWindow{Start: "08:00", End: "20:00"},
		State: rpc.BackgroundSyncWaitingInterval, NextRunAt: &next, LastResult: rpc.BackgroundSyncDone, CurrentChatID: &chat,
	}
	if err := a.success(status, rpc.StatusResult{BackgroundSync: block}); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{
		`(?m)^background_sync\s*:$`, `(?m)^  enabled\s*:\s+true$`, `(?m)^  state\s*:\s+waiting_interval$`,
		`(?m)^  window\s*:$`, `(?m)^    start\s*:\s+08:00$`, `(?m)^    end\s*:\s+20:00$`,
		`(?m)^  next_run_at\s*:\s+1800000000$`, `(?m)^  last_result\s*:\s+done$`, `(?m)^  current_chat_id\s*:\s+-1001$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(stdout.String()) {
			t.Errorf("human status missing %q: %q", pattern, stdout.String())
		}
	}
}
