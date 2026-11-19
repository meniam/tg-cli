package cli

import (
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
)

// TestFieldsNarrowsEveryFormat is the CLI half of the --fields contract: the
// same paths select the same data in json, jsonl, toon and human, and a failure
// keeps its envelope whatever the paths say.
func TestFieldsNarrowsEveryFormat(t *testing.T) {
	e := loggedInEnv(t)

	// The chats of the fixture, reduced to id and title.
	const paths = "chats.id,chats.title"
	wantJSON := `{"ok":true,"data":{"chats":[{"id":1,"title":"Ann"},{"id":-2,"title":"Family"},{"id":-1000000000003,"title":"News"}]}}` + "\n"
	if r := e.run("chat", "list", "--fields", paths); r.stdout != wantJSON || r.code != exitcode.OK {
		t.Errorf("json --fields %q: exit %d, got %q", paths, r.code, r.stdout)
	}
	// JSONL lines carry one chat, so the same path keeps the same field.
	wantJSONL := `{"id":1,"title":"Ann"}` + "\n" + `{"id":-2,"title":"Family"}` + "\n" + `{"id":-1000000000003,"title":"News"}` + "\n"
	if r := e.run("chat", "list", "--fields", paths, "--format", "jsonl"); r.stdout != wantJSONL || r.code != exitcode.OK {
		t.Errorf("jsonl --fields %q: exit %d, got %q", paths, r.code, r.stdout)
	}

	// toon and human get the same selected data, not a format of their own.
	if r := e.run("chat", "list", "--fields", paths, "--format", "toon"); r.code != exitcode.OK ||
		!strings.Contains(r.stdout, "chats[3]{id,title}:") {
		t.Errorf("toon --fields: exit %d, %q", r.code, r.stdout)
	}
	if r := e.run("chat", "list", "--fields", paths, "--format", "human"); r.code != exitcode.OK ||
		strings.Contains(r.stdout, "type") || !strings.Contains(r.stdout, "Ann") || !strings.Contains(r.stdout, "Family") {
		t.Errorf("human --fields must not show a field that was not asked for: %q", r.stdout)
	}

	// A failure is the envelope, and --fields does not change it: in json the
	// envelope is unchanged, and in human the one error line still tells the
	// reason, because a person reads it.
	wantFailure(t, e.run("--fields", "id", "--profile", "nodaemon", "chat", "list"), exitcode.DaemonNotRunning)
	humanErr := e.run("chat", "list", "--fields", "id", "--format", "human", "--profile", "nodaemon")
	if humanErr.code != exitcode.DaemonNotRunning || !strings.Contains(humanErr.stdout, "daemon is not running") {
		t.Errorf("a failure must still be reported in human: exit %d, %q", humanErr.code, humanErr.stdout)
	}

	// A path that cannot name a field is a usage error, not a silent empty answer.
	wantFailure(t, e.run("chat", "list", "--fields", "chats..id"), exitcode.Usage)
}

// A short path names a list item in every format. A result field keeps its
// top-level meaning even when item fields are selected alongside it.
func TestFieldsShortListPathsAndResultField(t *testing.T) {
	e := loggedInEnv(t)
	const short = "id,title"
	const full = "chats.id,chats.title"
	const wantJSON = `{"ok":true,"data":{"chats":[{"id":1,"title":"Ann"},{"id":-2,"title":"Family"},{"id":-1000000000003,"title":"News"}]}}` + "\n"
	const wantJSONL = `{"id":1,"title":"Ann"}` + "\n" + `{"id":-2,"title":"Family"}` + "\n" + `{"id":-1000000000003,"title":"News"}` + "\n"
	for _, format := range []string{"json", "jsonl", "toon", "human"} {
		t.Run(format, func(t *testing.T) {
			got := e.run("chat", "list", "--fields", short, "--format", format)
			prefixed := e.run("chat", "list", "--fields", full, "--format", format)
			if got.code != exitcode.OK || prefixed.code != exitcode.OK || got.stdout != prefixed.stdout {
				t.Fatalf("short: exit %d, %q; full: exit %d, %q", got.code, got.stdout, prefixed.code, prefixed.stdout)
			}
			switch format {
			case "json":
				if got.stdout != wantJSON {
					t.Errorf("got %q, want %q", got.stdout, wantJSON)
				}
			case "jsonl":
				if got.stdout != wantJSONL {
					t.Errorf("got %q, want %q", got.stdout, wantJSONL)
				}
			default:
				if !strings.Contains(got.stdout, "Ann") || !strings.Contains(got.stdout, "Family") || strings.Contains(got.stdout, "type") {
					t.Errorf("selected chat fields missing or extra: %q", got.stdout)
				}
			}
		})
	}

	for _, format := range []string{"json", "toon", "human"} {
		t.Run("cursor_"+format, func(t *testing.T) {
			got := e.run("message", "history", "@teamchat", "--limit", "2", "--fields", "id,next_cursor", "--format", format)
			prefixed := e.run("message", "history", "@teamchat", "--limit", "2", "--fields", "messages.id,next_cursor", "--format", format)
			if got.code != exitcode.OK || prefixed.code != exitcode.OK || got.stdout != prefixed.stdout {
				t.Fatalf("short: exit %d, %q; full: exit %d, %q", got.code, got.stdout, prefixed.code, prefixed.stdout)
			}
			if !strings.Contains(got.stdout, "CURSOR-AFTER") {
				t.Errorf("top-level cursor missing: %q", got.stdout)
			}
		})
	}
}

// TestFieldsNeverShowsMoreThanTheResult checks the other half: --fields cannot
// bring a value that the command did not return. A path of the schema that this
// response does not have (a nullable field, a map) adds nothing.
func TestFieldsNeverShowsMoreThanTheResult(t *testing.T) {
	e := loggedInEnv(t)
	// The fixture chats have no username, so asking for it keeps only the id.
	r := e.run("chat", "list", "--fields", "chats.username,chats.id")
	if r.code != exitcode.OK {
		t.Fatalf("exit %d, %q", r.code, r.stdout)
	}
	if strings.Contains(r.stdout, "username") || strings.Contains(r.stdout, `"Ann"`) {
		t.Errorf("a field the response does not have must not appear: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, `"id":1`) {
		t.Errorf("the asked field must be there: %q", r.stdout)
	}
}
