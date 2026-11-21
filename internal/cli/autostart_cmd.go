package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// autostartLabelBase names the launchd job of the default profile; another
// profile adds its name, so that each profile has its own job.
const autostartLabelBase = "dev.tg-cli.daemon"

// autostartPATH is the search path of the job. launchd starts a job with a short
// default path that lacks Homebrew, where ffmpeg of the voice transcription lives.
const autostartPATH = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// autostartResult is the answer of the autostart commands. Enabled means the job
// file exists, Loaded that launchd knows the job.
type autostartResult struct {
	Profile    string `json:"profile"`
	Label      string `json:"label"`
	Path       string `json:"path"`
	Enabled    bool   `json:"enabled"`
	Loaded     bool   `json:"loaded"`
	Executable string `json:"executable,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (a *app) autostartCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "autostart", Short: "Start the daemon at login (macOS launchd)"}
	on := a.annotate(&cobra.Command{
		Use: "on", Short: "Start the daemon of the profile at every login",
		Long: "Write a launchd job for the profile and load it. The job runs `tg daemon start --foreground`\n" +
			"at login and starts the daemon again after a crash; `tg daemon stop` ends it for good until\n" +
			"the next login. The job uses the settings saved for the profile. Application credentials of your\n" +
			"own (TG_API_ID, TG_API_HASH) are never written to the job file. macOS only.",
		Args: cobra.NoArgs, RunE: a.autostartOn,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[autostartResult]()})
	off := a.annotate(&cobra.Command{
		Use: "off", Short: "Stop starting the daemon at login",
		Long: "Unload the launchd job of the profile and delete its file. A daemon that launchd runs stops\n" +
			"with the job; a daemon you started yourself keeps running. macOS only.",
		Args: cobra.NoArgs, RunE: a.autostartOff,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[autostartResult]()})
	status := a.annotate(&cobra.Command{
		Use: "status", Short: "Show whether the daemon starts at login",
		Long: "Show whether the launchd job of the profile is installed and loaded. It changes nothing. macOS only.",
		Args: cobra.NoArgs, RunE: a.autostartStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[autostartResult]()})
	cmd.AddCommand(on, off, status)
	return cmd
}

func (a *app) autostartGOOS() string {
	if a.deps.GOOS != "" {
		return a.deps.GOOS
	}
	return runtime.GOOS
}

func (a *app) launchctl(args ...string) (string, error) {
	if a.deps.RunLaunchctl != nil {
		return a.deps.RunLaunchctl(args...)
	}
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// autostartTarget collects the names the three commands share.
type autostartTarget struct {
	profile string
	label   string
	file    string
	domain  string // gui/<uid>
	logPath string
	socket  string
}

func (a *app) autostartTarget(cmd *cobra.Command) (autostartTarget, error) {
	if a.autostartGOOS() != "darwin" {
		return autostartTarget{}, fail(exitcode.Usage, "autostart works through launchd and so on macOS only")
	}
	profile, paths, err := a.profile(cmd)
	if err != nil {
		return autostartTarget{}, err
	}
	home := a.deps.Getenv("HOME")
	if home == "" {
		if home, err = os.UserHomeDir(); err != nil {
			return autostartTarget{}, fail(exitcode.Failure, "cannot find the home directory: %v", err)
		}
	}
	label := autostartLabelBase
	if profile != "default" {
		label += "." + profile
	}
	return autostartTarget{
		profile: profile, label: label,
		file:    filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		domain:  fmt.Sprintf("gui/%d", os.Getuid()),
		logPath: filepath.Join(paths.Dir, "launchd.err"),
		socket:  paths.Socket,
	}, nil
}

// loaded asks launchd whether the job is known.
func (a *app) autostartLoaded(t autostartTarget) bool {
	_, err := a.launchctl("print", t.domain+"/"+t.label)
	return err == nil
}

// daemonAnswers reports whether a daemon already listens on the socket.
func (a *app) daemonAnswers(ctx context.Context, socket string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := rpc.Dial(ctx, "unix", socket)
	if err != nil {
		return false
	}
	_ = client.Close()
	return true
}

var plistProgramPath = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)

// autostartInstalledExecutable reads the binary a job file points at.
func autostartInstalledExecutable(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	if m := plistProgramPath.FindSubmatch(data); m != nil {
		return xmlUnescape(string(m[1]))
	}
	return ""
}

func xmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'").Replace(s)
}

func plistString(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

// renderAutostartPlist builds the launchd job: run the daemon in the foreground
// at login, restart it only after a crash, and give it a search path with Homebrew.
func renderAutostartPlist(label, executable, profile, logPath, tgHome string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	fmt.Fprintf(&b, "  <key>Label</key><string>%s</string>\n", plistString(label))
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range []string{executable, "--profile", profile, "daemon", "start", "--foreground"} {
		fmt.Fprintf(&b, "    <string>%s</string>\n", plistString(arg))
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key><true/>\n")
	b.WriteString("  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n")
	b.WriteString("  <key>ThrottleInterval</key><integer>30</integer>\n")
	b.WriteString("  <key>ProcessType</key><string>Background</string>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	fmt.Fprintf(&b, "    <key>PATH</key><string>%s</string>\n", plistString(autostartPATH))
	if tgHome != "" {
		fmt.Fprintf(&b, "    <key>TG_CLI_HOME</key><string>%s</string>\n", plistString(tgHome))
	}
	b.WriteString("  </dict>\n")
	fmt.Fprintf(&b, "  <key>StandardErrorPath</key><string>%s</string>\n", plistString(logPath))
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func (a *app) autostartOn(cmd *cobra.Command, _ []string) error {
	t, err := a.autostartTarget(cmd)
	if err != nil {
		return err
	}
	if a.deps.Getenv("TG_API_ID") != "" || a.deps.Getenv("TG_API_HASH") != "" {
		return fail(exitcode.Usage, "autostart does not store application credentials: unset TG_API_ID and TG_API_HASH and use the saved official client preset")
	}
	executable, err := a.autostartExecutable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.file), 0o755); err != nil {
		return fail(exitcode.Failure, "cannot create %s: %v", filepath.Dir(t.file), err)
	}
	already := a.daemonAnswers(cmd.Context(), t.socket)
	if a.autostartLoaded(t) {
		// A second `on` replaces the job: the binary or the profile settings may have changed.
		if out, err := a.launchctl("bootout", t.domain+"/"+t.label); err != nil {
			return fail(exitcode.Failure, "launchctl bootout failed: %s", launchctlText(out, err))
		}
	}
	content := renderAutostartPlist(t.label, executable, t.profile, t.logPath, a.deps.Getenv("TG_CLI_HOME"))
	if err := writeFileAtomic(t.file, []byte(content), 0o644); err != nil {
		return fail(exitcode.Failure, "cannot write %s: %v", t.file, err)
	}
	if out, err := a.launchctl("bootstrap", t.domain, t.file); err != nil {
		return fail(exitcode.Failure, "launchctl bootstrap failed: %s (the job file stays at %s)", launchctlText(out, err), t.file)
	}
	result := autostartResult{Profile: t.profile, Label: t.label, Path: t.file, Enabled: true, Loaded: a.autostartLoaded(t), Executable: executable}
	if already {
		result.Note = "A daemon is already running outside launchd. launchd supervises the daemon from the next login, or after `tg daemon stop` and `tg autostart on` again."
	}
	return a.success(cmd, result)
}

func (a *app) autostartOff(cmd *cobra.Command, _ []string) error {
	t, err := a.autostartTarget(cmd)
	if err != nil {
		return err
	}
	result := autostartResult{Profile: t.profile, Label: t.label, Path: t.file}
	if a.autostartLoaded(t) {
		if out, err := a.launchctl("bootout", t.domain+"/"+t.label); err != nil {
			return fail(exitcode.Failure, "launchctl bootout failed: %s", launchctlText(out, err))
		}
		result.Note = "The daemon that launchd ran was stopped with the job. Start it with `tg daemon start`."
	}
	if err := os.Remove(t.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(exitcode.Failure, "cannot remove %s: %v", t.file, err)
	}
	return a.success(cmd, result)
}

func (a *app) autostartStatus(cmd *cobra.Command, _ []string) error {
	t, err := a.autostartTarget(cmd)
	if err != nil {
		return err
	}
	result := autostartResult{Profile: t.profile, Label: t.label, Path: t.file, Loaded: a.autostartLoaded(t)}
	if _, err := os.Stat(t.file); err == nil {
		result.Enabled = true
		result.Executable = autostartInstalledExecutable(t.file)
	}
	return a.success(cmd, result)
}

// autostartExecutable is the binary the job runs: the running one, with links resolved.
func (a *app) autostartExecutable() (string, error) {
	find := a.deps.Executable
	if find == nil {
		find = os.Executable
	}
	path, err := find()
	if err != nil {
		return "", fail(exitcode.Failure, "cannot find the running binary: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if strings.Contains(path, "/go-build") || strings.HasPrefix(path, os.TempDir()) {
		return "", fail(exitcode.Usage, "run the installed tg (for example ~/.local/bin/tg), not a temporary build: %s", path)
	}
	return path, nil
}

func launchctlText(out string, err error) string {
	if out != "" {
		return out
	}
	return err.Error()
}

// writeFileAtomic writes through a temporary file in the same directory, so a
// reader never sees a half-written job file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".autostart-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// autostartLine is the row of tg status about the start at login. It is empty
// where autostart does not exist (not macOS) or the state cannot be read: the
// dashboard then leaves the row out.
func (a *app) autostartLine(cmd *cobra.Command) string {
	t, err := a.autostartTarget(cmd)
	if err != nil {
		return ""
	}
	enable := "tg autostart on"
	if t.profile != "default" {
		enable += " --profile " + t.profile
	}
	_, statErr := os.Stat(t.file)
	switch loaded := a.autostartLoaded(t); {
	case statErr == nil && loaded:
		return "On · launchd starts the daemon at login"
	case statErr == nil:
		return "Installed, not loaded · run " + enable
	default:
		return "Off · enable with " + enable
	}
}
