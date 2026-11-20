package cli

import (
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
)

// TestJSONAndJSONLStayTheSame is the main invariant of the plan: choosing a
// format is an addition, so the default and JSONL output of a list command must
// stay exactly what they were before the pipeline existed.
func TestJSONAndJSONLStayTheSame(t *testing.T) {
	e := loggedInEnv(t)
	want := `{"ok":true,"data":{"chats":[` +
		`{"id":1,"type":"user","title":"Ann","unread":0},` +
		`{"id":-2,"type":"group","title":"Family","unread":0},` +
		`{"id":-1000000000003,"type":"channel","title":"News","unread":0}]}}` + "\n"
	for _, args := range [][]string{
		{"chat", "list"},
		{"chat", "list", "--format", "json"},
	} {
		if r := e.run(args...); r.stdout != want || r.code != exitcode.OK {
			t.Errorf("%v: exit %d, got %q, want %q", args, r.code, r.stdout, want)
		}
	}
	wantJSONL := `{"id":1,"type":"user","title":"Ann","unread":0}` + "\n" +
		`{"id":-2,"type":"group","title":"Family","unread":0}` + "\n" +
		`{"id":-1000000000003,"type":"channel","title":"News","unread":0}` + "\n"
	if r := e.run("chat", "list", "--format", "jsonl"); r.stdout != wantJSONL || r.code != exitcode.OK {
		t.Errorf("jsonl: exit %d, got %q, want %q", r.code, r.stdout, wantJSONL)
	}
}

// TestFormatIsOneGlobalFlag checks that the format is chosen before or after the
// command name, that --human is the short form of --format human, and that a
// command that returns no list refuses JSONL instead of printing nothing.
func TestFormatIsOneGlobalFlag(t *testing.T) {
	e := loggedInEnv(t)

	before := e.run("--format", "toon", "chat", "list")
	after := e.run("chat", "list", "--format", "toon")
	if before.code != exitcode.OK || before.stdout != after.stdout {
		t.Errorf("the flag must work in both positions: %q vs %q", before.stdout, after.stdout)
	}
	if human, short := e.run("--format", "human", "chat", "list"), e.run("chat", "list", "--human"); human.code != exitcode.OK || short.code != exitcode.OK ||
		!strings.Contains(human.stdout, "Ann") || human.stdout != short.stdout {
		t.Errorf("--human must be --format human: exit %d and %d, %q vs %q", human.code, short.code, human.stdout, short.stdout)
	}

	// A command that returns one object has no items to print one per line.
	wantFailure(t, e.run("version", "--format", "jsonl"), exitcode.Usage)
	// An unknown format is a usage error, not a crash or a silent default.
	wantFailure(t, e.run("version", "--format", "xml"), exitcode.Usage)
	// A failure has no item per line to print, so JSONL still answers with the
	// envelope: an agent finds the error the same way in every format.
	wantFailure(t, e.run("--format", "jsonl", "chat", "list", "--profile", "nodaemon"), exitcode.DaemonNotRunning)
}

// TestQuietKeepsErrors is the rule of the decision: --quiet removes hints and
// progress, never an error. A quiet command that fails must still tell the agent
// why, in the envelope.
func TestQuietKeepsErrors(t *testing.T) {
	e := newEnv(t) // no daemon: the command fails
	r := e.run("--quiet", "chat", "list")
	wantFailure(t, r, exitcode.DaemonNotRunning)
	if r.stderr != "" {
		t.Errorf("a quiet run must not write to stderr: %q", r.stderr)
	}

	// The preset warning and the daemon log are hints, so --quiet drops them
	// while the daemon still starts.
	e = newEnv(t)
	e.usePreset()
	d := e.startDaemon("--client", "desktop", "--quiet")
	e.stopDaemon(d)
	if strings.Contains(d.res.stderr, "official client preset") {
		t.Errorf("--quiet must silence the preset warning: %q", d.res.stderr)
	}
}

// TestVerboseNeverPrintsContent checks the other half of the same rule: the
// diagnostics may name the call, never what it found.
func TestVerboseNeverPrintsContent(t *testing.T) {
	e := loggedInEnv(t)
	// The titles and the message text of the fixture are the content that must
	// stay out of the diagnostics.
	const content = "third"
	r := e.run("--verbose", "message", "history", "@teamchat")
	if r.code != exitcode.OK {
		t.Fatalf("history: exit %d, %q", r.code, r.stdout)
	}
	if r.stderr == "" {
		t.Fatal("--verbose must print diagnostics")
	}
	for _, secret := range []string{content, "Team", "Ann", "Family", "@teamchat"} {
		if strings.Contains(r.stderr, secret) {
			t.Errorf("--verbose leaked %q: %q", secret, r.stderr)
		}
	}
	if !strings.Contains(r.stderr, "message.history") {
		t.Errorf("the diagnostics must name the method: %q", r.stderr)
	}

	// Without --verbose nothing is added.
	if plain := e.run("message", "history", "@teamchat"); plain.stderr != "" {
		t.Errorf("stderr without --verbose: %q", plain.stderr)
	}
}
