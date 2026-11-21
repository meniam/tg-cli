package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
)

// autostartRun is a run of `tg autostart ...` in a temporary home with a fake launchctl.
type autostartRun struct {
	code           int
	stdout, stderr string
	calls          [][]string
	home           string
}

// runAutostart calls the command with a launchctl that records its arguments. loaded
// says whether `launchctl print` finds the job, and fails makes named verbs fail.
func runAutostart(t *testing.T, args []string, goos string, env map[string]string, loaded bool, fails map[string]string) autostartRun {
	t.Helper()
	home := t.TempDir()
	var run autostartRun
	run.home = home
	bin := filepath.Join(home, "bin", "tg")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	state := loaded
	run.code = Execute(context.Background(), args, Deps{
		Stdout: &stdout, Stderr: &stderr,
		ConfigPath: filepath.Join(home, "config.toml"),
		Getenv: func(key string) string {
			if key == "HOME" {
				return home
			}
			return env[key]
		},
		GOOS:       goos,
		Executable: func() (string, error) { return bin, nil },
		RunLaunchctl: func(a ...string) (string, error) {
			run.calls = append(run.calls, append([]string(nil), a...))
			if msg, bad := fails[a[0]]; bad {
				return msg, os.ErrInvalid
			}
			switch a[0] {
			case "print":
				if !state {
					return "Could not find service", os.ErrNotExist
				}
			case "bootstrap":
				state = true
			case "bootout":
				state = false
			}
			return "", nil
		},
	})
	run.stdout, run.stderr = stdout.String(), stderr.String()
	return run
}

func autostartData(t *testing.T, run autostartRun) autostartResult {
	t.Helper()
	var envelope struct {
		Data autostartResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &envelope); err != nil {
		t.Fatalf("stdout %q: %v", run.stdout, err)
	}
	return envelope.Data
}

func (r autostartRun) verbs() []string {
	var verbs []string
	for _, c := range r.calls {
		verbs = append(verbs, c[0])
	}
	return verbs
}

func TestAutostartOnWritesTheJobAndLoadsIt(t *testing.T) {
	run := runAutostart(t, []string{"autostart", "on"}, "darwin", nil, false, nil)
	if run.code != exitcode.OK {
		t.Fatalf("run=%+v", run)
	}
	result := autostartData(t, run)
	want := filepath.Join(run.home, "Library", "LaunchAgents", "dev.tg-cli.daemon.plist")
	if result.Path != want || result.Label != "dev.tg-cli.daemon" || !result.Enabled || !result.Loaded || result.Profile != "default" {
		t.Fatalf("result=%+v", result)
	}
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	plist := string(raw)
	for _, part := range []string{
		"<string>dev.tg-cli.daemon</string>", "<string>--profile</string>", "<string>default</string>",
		"<string>daemon</string>", "<string>start</string>", "<string>--foreground</string>",
		"<key>RunAtLoad</key><true/>", "<key>SuccessfulExit</key><false/>", "/opt/homebrew/bin", "launchd.err",
	} {
		if !strings.Contains(plist, part) {
			t.Errorf("job file lacks %q:\n%s", part, plist)
		}
	}
	if strings.Contains(plist, "TG_CLI_HOME") {
		t.Errorf("a home that was not set must not be written:\n%s", plist)
	}
	if info, err := os.Stat(want); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode of the job file: %v %v", info, err)
	}
	if got := run.verbs(); strings.Join(got, ",") != "print,bootstrap,print" {
		t.Fatalf("launchctl calls: %v", run.calls)
	}
	if boot := run.calls[1]; len(boot) != 3 || !strings.HasPrefix(boot[1], "gui/") || boot[2] != want {
		t.Fatalf("bootstrap call: %v", boot)
	}
	if result.Executable != filepath.Join(run.home, "bin", "tg") && !strings.HasSuffix(result.Executable, "/bin/tg") {
		t.Fatalf("executable=%q", result.Executable)
	}
}

func TestAutostartOnNamesAnotherProfileAndKeepsTGCLIHome(t *testing.T) {
	run := runAutostart(t, []string{"autostart", "on", "--profile", "work"}, "darwin", map[string]string{"TG_CLI_HOME": "/data/tg"}, false, nil)
	if run.code != exitcode.OK {
		t.Fatalf("run=%+v", run)
	}
	result := autostartData(t, run)
	if result.Label != "dev.tg-cli.daemon.work" || !strings.HasSuffix(result.Path, "dev.tg-cli.daemon.work.plist") || result.Profile != "work" {
		t.Fatalf("result=%+v", result)
	}
	raw, _ := os.ReadFile(result.Path)
	for _, part := range []string{"<string>work</string>", "<key>TG_CLI_HOME</key><string>/data/tg</string>"} {
		if !strings.Contains(string(raw), part) {
			t.Errorf("job file lacks %q:\n%s", part, raw)
		}
	}
}

func TestAutostartOnTwiceReplacesTheLoadedJob(t *testing.T) {
	run := runAutostart(t, []string{"autostart", "on"}, "darwin", nil, true, nil)
	if run.code != exitcode.OK {
		t.Fatalf("run=%+v", run)
	}
	if got := strings.Join(run.verbs(), ","); got != "print,bootout,bootstrap,print" {
		t.Fatalf("a loaded job must be unloaded first: %v", run.calls)
	}
}

func TestAutostartOnRefusesOwnApplicationCredentials(t *testing.T) {
	for _, key := range []string{"TG_API_ID", "TG_API_HASH"} {
		run := runAutostart(t, []string{"autostart", "on"}, "darwin", map[string]string{key: "x"}, false, nil)
		if run.code != exitcode.Usage || len(run.calls) != 0 {
			t.Fatalf("%s: %+v", key, run)
		}
		if _, err := os.Stat(filepath.Join(run.home, "Library")); err == nil {
			t.Fatalf("%s: a file was written", key)
		}
	}
}

func TestAutostartOnlyWorksOnMacOS(t *testing.T) {
	for _, command := range []string{"on", "off", "status"} {
		run := runAutostart(t, []string{"autostart", command}, "linux", nil, false, nil)
		if run.code != exitcode.Usage || len(run.calls) != 0 || !strings.Contains(run.stdout+run.stderr, "macOS") {
			t.Fatalf("%s: %+v", command, run)
		}
	}
}

func TestAutostartOnReportsALaunchctlFailureAndKeepsTheFile(t *testing.T) {
	run := runAutostart(t, []string{"autostart", "on"}, "darwin", nil, false, map[string]string{"bootstrap": "Bootstrap failed: 5: Input/output error"})
	if run.code != exitcode.Failure || !strings.Contains(run.stdout+run.stderr, "Input/output error") {
		t.Fatalf("run=%+v", run)
	}
	if _, err := os.Stat(filepath.Join(run.home, "Library", "LaunchAgents", "dev.tg-cli.daemon.plist")); err != nil {
		t.Fatalf("the job file must stay for the owner to look at: %v", err)
	}
}

func TestAutostartOffUnloadsAndRemovesTheJob(t *testing.T) {
	on := runAutostart(t, []string{"autostart", "on"}, "darwin", nil, false, nil)
	path := autostartData(t, on).Path
	// A second run in the same home: the fake loaded state starts true, the file exists.
	off := runAutostartIn(t, on.home, []string{"autostart", "off"}, true)
	if off.code != exitcode.OK {
		t.Fatalf("off=%+v", off)
	}
	result := autostartData(t, off)
	if result.Enabled || result.Loaded || result.Note == "" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the job file must be gone: %v", err)
	}
	if got := strings.Join(off.verbs(), ","); got != "print,bootout" {
		t.Fatalf("calls: %v", off.calls)
	}
}

func TestAutostartOffWithNothingInstalledIsQuiet(t *testing.T) {
	run := runAutostart(t, []string{"autostart", "off"}, "darwin", nil, false, nil)
	if run.code != exitcode.OK {
		t.Fatalf("run=%+v", run)
	}
	if got := strings.Join(run.verbs(), ","); got != "print" {
		t.Fatalf("nothing is loaded, nothing to unload: %v", run.calls)
	}
	if result := autostartData(t, run); result.Enabled || result.Loaded || result.Note != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestAutostartStatusChangesNothing(t *testing.T) {
	on := runAutostart(t, []string{"autostart", "on"}, "darwin", nil, false, nil)
	status := runAutostartIn(t, on.home, []string{"autostart", "status"}, true)
	if status.code != exitcode.OK {
		t.Fatalf("status=%+v", status)
	}
	result := autostartData(t, status)
	if !result.Enabled || !result.Loaded || result.Executable == "" {
		t.Fatalf("result=%+v", result)
	}
	if got := strings.Join(status.verbs(), ","); got != "print" {
		t.Fatalf("status may only look: %v", status.calls)
	}
	none := runAutostart(t, []string{"autostart", "status"}, "darwin", nil, false, nil)
	if r := autostartData(t, none); r.Enabled || r.Loaded || r.Executable != "" {
		t.Fatalf("nothing installed: %+v", r)
	}
}

func TestAutostartRefusesATemporaryBinary(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"autostart", "on"}, Deps{
		Stdout: &stdout, Stderr: &stderr, ConfigPath: filepath.Join(home, "config.toml"),
		Getenv:       func(key string) string { return map[string]string{"HOME": home}[key] },
		GOOS:         "darwin",
		Executable:   func() (string, error) { return "/var/folders/x/go-build123/b001/exe/tg", nil },
		RunLaunchctl: func(...string) (string, error) { return "", nil },
	})
	if code != exitcode.Usage {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "Library")); err == nil {
		t.Fatal("a job file was written for a temporary binary")
	}
}

func TestRenderAutostartPlistEscapesXML(t *testing.T) {
	plist := renderAutostartPlist("dev.tg-cli.daemon.a&b", "/opt/tg <x>/tg", "a&b", "/p/launchd.err", "/h&m")
	for _, bad := range []string{"a&b<", "<x>", "/h&m<"} {
		if strings.Contains(plist, bad) {
			t.Errorf("unescaped text %q:\n%s", bad, plist)
		}
	}
	for _, good := range []string{"a&amp;b", "/opt/tg &lt;x&gt;/tg", "/h&amp;m"} {
		if !strings.Contains(plist, good) {
			t.Errorf("missing %q:\n%s", good, plist)
		}
	}
}

// runAutostartIn runs the command in an existing home, with the fake launchd state set.
func runAutostartIn(t *testing.T, home string, args []string, loaded bool) autostartRun {
	t.Helper()
	run := autostartRun{home: home}
	bin := filepath.Join(home, "bin", "tg")
	var stdout, stderr bytes.Buffer
	state := loaded
	run.code = Execute(context.Background(), args, Deps{
		Stdout: &stdout, Stderr: &stderr, ConfigPath: filepath.Join(home, "config.toml"),
		Getenv:     func(key string) string { return map[string]string{"HOME": home}[key] },
		GOOS:       "darwin",
		Executable: func() (string, error) { return bin, nil },
		RunLaunchctl: func(a ...string) (string, error) {
			run.calls = append(run.calls, append([]string(nil), a...))
			switch a[0] {
			case "print":
				if !state {
					return "", os.ErrNotExist
				}
			case "bootout":
				state = false
			case "bootstrap":
				state = true
			}
			return "", nil
		},
	})
	run.stdout, run.stderr = stdout.String(), stderr.String()
	return run
}
