package cli

import (
	"slices"
	"testing"
)

func TestCommandUntrustedPathsAreComplete(t *testing.T) {
	want := map[string][]string{
		"saved list":                 {"dialogs.title"},
		"saved history":              {"messages.sender.name", "messages.sender.username", "messages.text"},
		"saved search":               {"messages.sender.name", "messages.sender.username", "messages.text"},
		"saved tags":                 {"tags.title"},
		"saved tag-rename":           {"title"},
		"folder invite-info":         {"title", "chats.title"},
		"sticker search":             {"sets.short_name", "sets.title"},
		"sticker installed":          {"sets.short_name", "sets.title"},
		"sticker set":                {"set.short_name", "set.title", "stickers.emoji"},
		"message send-sticker":       {"sticker.emoji"},
		"message inline-query":       {"next_offset", "results.id", "results.type", "results.title", "results.description", "results.url"},
		"message buttons":            {"buttons.text", "buttons.url", "buttons.copy_text"},
		"message press":              {"button.text", "message", "url"},
		"folder list":                {"folders.title", "folders.pinned.title", "folders.include.title", "folders.exclude.title"},
		"account blocked":            {"peers.title", "peers.username"},
		"account terminate":          {"device_model", "platform", "app_name"},
		"account profile":            {"first_name", "last_name", "username", "bio"},
		"account emoji-statuses":     {"statuses.title", "statuses.slug"},
		"account sessions":           {"sessions.device_model", "sessions.platform", "sessions.system_version", "sessions.app_name", "sessions.app_version"},
		"user profile":               {"first_name", "last_name", "username", "usernames", "bio"},
		"contact list":               {"contacts.first_name", "contacts.last_name", "contacts.username"},
		"contact search":             {"contacts.first_name", "contacts.last_name", "contacts.username"},
		"contact add":                {"first_name", "last_name"},
		"chat list":                  {"chats.title", "chats.username"},
		"chat unread":                {"chats.title", "chats.username"},
		"chat changes":               {"current.title", "current.username", "current.about", "versions.title", "versions.username", "versions.about"},
		"chat requests":              {"requests.name", "requests.username", "requests.about"},
		"chat stats":                 {"top_posters.name", "top_admins.name", "top_inviters.name", "data"},
		"chat members":               {"members.name", "members.username"},
		"chat topics":                {"topics.title"},
		"chat common":                {"chats.title", "chats.username"},
		"chat resolve":               {"title", "username"},
		"chat info":                  {"title", "username", "about", "restriction_reason.text"},
		"chat overview":              {"chats.title", "chats.username", "chats.first_message.text", "chats.last_message.text"},
		"chat invite-info":           {"title", "about"},
		"chat draft get":             {"text"},
		"chat draft list":            {"drafts.text"},
		"message scheduled list":     {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message scheduled delete":   {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message scheduled send-now": {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message history":            {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message unread":             {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message download":           {"files.mime", "files.file_name"},
		"message get":                {"sender.name", "sender.username", "text"},
		"message readers":            {"readers.name", "readers.username"},
		"message transcribe":         {"text"},
		"message translate":          {"items.text"},
		"message pinned":             {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message search":             {"messages.sender.name", "messages.sender.username", "messages.text", "hits.chat.title", "hits.chat.username", "hits.message.sender.name", "hits.message.sender.username", "hits.message.text"},
		"message context":            {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message export":             {"messages.sender.name", "messages.sender.username", "messages.text"},
		"message comments":           {"messages.sender.name", "messages.sender.username", "messages.text"},
		"search":                     {"hits.title", "hits.snippet", "fragments.text"},
		// An event carries message text and names inside data, so the whole payload is untrusted.
		"message watch": {"events.data"},
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
