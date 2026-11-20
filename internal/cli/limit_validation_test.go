package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
)

// audit02LimitCommands maps every command that has a --limit flag to a valid
// invocation of it. TestAudit02_LimitCommandsAreComplete keeps it equal to the
// command tree, so a new command with --limit cannot skip the validation test.
var audit02LimitCommands = map[string][]string{
	"account blocked": {"account", "blocked"}, "chat changes": {"chat", "changes", "42"}, "chat common": {"chat", "common", "42"},
	"chat list": {"chat", "list"}, "chat members": {"chat", "members", "42"}, "chat overview": {"chat", "overview"},
	"chat requests": {"chat", "requests", "42"}, "chat state": {"chat", "state"}, "chat topics": {"chat", "topics", "42"},
	"chat unread": {"chat", "unread"}, "contact list": {"contact", "list"}, "contact search": {"contact", "search", "term"},
	"message comments": {"message", "comments", "42", "7"}, "message export": {"message", "export", "42", "--local"},
	"message history": {"message", "history", "42"}, "message pinned": {"message", "pinned", "42"},
	"message search": {"message", "search", "42", "term"}, "message unread": {"message", "unread", "42"},
	"note list": {"note", "list"}, "saved history": {"saved", "history", "42"}, "saved list": {"saved", "list"},
	"saved search": {"saved", "search"}, "search": {"search", "term"}, "sticker search": {"sticker", "search", "term"}, "voice list": {"voice", "list"},
}

func TestAudit02_LimitCommandsAreComplete(t *testing.T) {
	_, root := annotatedApp()
	found := map[string]bool{}
	walkCommands(root, func(cmd *cobra.Command, path string) {
		if cmd.Flags().Lookup("limit") != nil {
			found[path] = true
		}
	})
	for path := range found {
		if _, ok := audit02LimitCommands[path]; !ok {
			t.Errorf("%q has a --limit flag but is missing from audit02LimitCommands", path)
		}
	}
	for path := range audit02LimitCommands {
		if !found[path] {
			t.Errorf("%q is in audit02LimitCommands but has no --limit flag", path)
		}
	}
}

func TestExplicitLimitMustBePositive(t *testing.T) {
	for _, args := range audit02LimitCommands {
		for _, limit := range []string{"0", "-1"} {
			var out, stderr bytes.Buffer
			cmd := append(append([]string{}, args...), "--limit="+limit)
			code := Execute(context.Background(), cmd, Deps{Stdout: &out, Stderr: &stderr})
			if code != exitcode.Usage || !bytes.Contains(out.Bytes(), []byte("--limit must be at least 1")) {
				t.Fatalf("%v: code %d, output %s", cmd, code, out.String())
			}
		}
	}
}
