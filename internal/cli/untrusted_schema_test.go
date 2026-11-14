package cli

import (
	"slices"
	"testing"
)

func TestCommandUntrustedPathsAreComplete(t *testing.T) {
	want := map[string][]string{
		"chat list":        {"chats.title", "chats.username"},
		"chat resolve":     {"title", "username"},
		"chat info":        {"title", "username", "about"},
		"chat invite-info": {"title", "about"},
		"message history":  {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message get":      {"sender.name", "sender.username", "text"},
		"message pinned":   {"messages.sender.name", "messages.sender.username", "messages.text"},
		"search":           {"hits.title", "hits.snippet"},
	}
	commands := schemaCommands(t)
	for path, expected := range want {
		cmd, ok := commands[path]
		if !ok {
			t.Errorf("missing command %s", path)
			continue
		}
		var actual []string
		for _, field := range cmd.Fields {
			if field.Untrusted {
				actual = append(actual, field.Path)
			}
		}
		if !slices.Equal(actual, expected) {
			t.Errorf("%s: untrusted=%v, want %v", path, actual, expected)
		}
	}
	for path, cmd := range commands {
		if _, ok := want[path]; ok {
			continue
		}
		for _, field := range cmd.Fields {
			if field.Untrusted {
				t.Errorf("unexpected untrusted field %s in %s", field.Path, path)
			}
		}
	}
}
