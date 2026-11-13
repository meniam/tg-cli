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
		strings.Contains(r.stdout, "type") {
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
