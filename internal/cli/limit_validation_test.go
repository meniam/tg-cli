package cli

import (
	"bytes"
	"context"
	"testing"

	"tg-cli/internal/exitcode"
)

func TestExplicitLimitMustBePositive(t *testing.T) {
	commands := [][]string{
		{"account", "blocked"}, {"chat", "changes", "42"}, {"chat", "common", "42"},
		{"chat", "list"}, {"chat", "members", "42"}, {"chat", "requests", "42"},
		{"chat", "state"}, {"chat", "topics", "42"}, {"chat", "unread"},
		{"contact", "list"}, {"contact", "search", "term"}, {"message", "comments", "42", "7"},
		{"message", "export", "42", "--local"}, {"message", "history", "42"},
		{"message", "pinned", "42"}, {"message", "search", "42", "term"},
		{"message", "unread", "42"}, {"note", "list"}, {"saved", "history", "42"},
		{"saved", "list"}, {"saved", "search"}, {"search", "term"}, {"sticker", "search", "term"},
	}
	for _, args := range commands {
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
