package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/redact"
	"tg-cli/internal/schema"
)

// The output pipeline lives here: the global flags that choose a format and a
// level of talking are declared once, read once, and every command prints
// through app.emit and app.warn rather than writing to stdout or stderr itself.

// outputSettings are the choices of one run: the format, and how much the
// commands say on stderr.
type outputSettings struct {
	format    output.Format
	untrusted []string
	// fields are the paths --fields asked for, empty when it was not given.
	fields []string
	// quiet removes hints and progress lines; errors are never removed.
	quiet bool
	// verbose adds diagnostics about the call.
	verbose bool
}

// addOutputFlags declares --format, --human, --quiet and --verbose on the root
// command. They are persistent, so every command accepts them in any position.
func addOutputFlags(root *cobra.Command) {
	flags := root.PersistentFlags()
	flags.String("format", string(output.FormatJSON),
		"output format: json (one envelope, the default), jsonl (list items or a supported single-result envelope), toon (compact envelope) or human")
	flags.Bool("human", false, "short for --format human")
	flags.String("fields", "",
		"only these paths of the result, comma separated, like id,title or messages.sender.name; a path the result does not have is skipped")
	flags.Bool("quiet", false, "hide hints and progress on stderr; errors are still printed")
	flags.Bool("verbose", false, "print diagnostics about the call on stderr")
}

// outputSettingsFor reads the global output flags of a command.
//
// The flags are read from the root command, so no subcommand can shadow them,
// and they are accepted before or after the command name.
func (a *app) outputSettingsFor(cmd *cobra.Command) (outputSettings, error) {
	root := cmd.Root()
	quiet, _ := root.PersistentFlags().GetBool("quiet")
	verbose, _ := root.PersistentFlags().GetBool("verbose")
	settings := outputSettings{quiet: quiet, verbose: verbose}
	format, err := requestedFormat(root)
	if err != nil {
		return settings, err
	}
	settings.format = format
	if a.specs[cmd].SubscriptionMethod != "" || a.specs[cmd].DefaultJSONL {
		if !root.PersistentFlags().Changed("format") && format != output.FormatHuman {
			settings.format = output.FormatJSONL
		}
		if settings.format == output.FormatToon {
			return settings, fail(exitcode.Usage, "--format toon is not supported for a stream")
		}
	}
	if format == output.FormatHuman {
		for _, field := range schema.Fields(a.specs[cmd].Result) {
			if field.Untrusted {
				settings.untrusted = append(settings.untrusted, field.Path)
			}
		}
	}
	fields, _ := root.PersistentFlags().GetString("fields")
	if settings.fields, err = output.ParseFields(fields); err != nil {
		return settings, fail(exitcode.Usage, "%v", err)
	}
	// A command that returns no list has nothing to print one item per line.
	if format == output.FormatJSONL && a.specs[cmd].ItemPath == "" && !a.specs[cmd].ObjectJSONL {
		return settings, fail(exitcode.Usage, "--format jsonl is for commands that return a list, %q is not one", cmd.CommandPath())
	}
	return settings, nil
}

// requestedFormat reads --format and --human, which belong to the root command.
// It does not look at the command itself, so a failure can ask for the format
// without going through the rule that JSONL needs a list.
func requestedFormat(root *cobra.Command) (output.Format, error) {
	if human, _ := root.PersistentFlags().GetBool("human"); human {
		return output.FormatHuman, nil
	}
	name, _ := root.PersistentFlags().GetString("format")
	format, err := output.ParseFormat(name)
	if err != nil {
		return "", fail(exitcode.Usage, "%v", err)
	}
	return format, nil
}

// emit prints a success result: the envelope in the chosen format, or the items
// one per line for JSONL. Selected single-result JSONL commands use success.
//
// --fields goes through every format the same way: the envelope is narrowed
// first, so json, toon and human print the same selected data and JSONL prints
// the selected lines. A format is never given a shape of its own.
func (a *app) emit(settings outputSettings, data any, lines func(io.Writer) error) error {
	if settings.format == output.FormatJSONL {
		return lines(a.deps.Stdout)
	}
	return output.SelectWithUntrusted(a.deps.Stdout, settings.format, output.SuccessDataWithThrottle(data, a.throttledMS), settings.fields, settings.untrusted)
}

// success prints one result; selected commands allow it as one JSONL envelope.
func (a *app) success(cmd *cobra.Command, data any) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	partial := isPartial(data)
	if partial && len(settings.fields) > 0 {
		settings.fields = append(settings.fields, "partial", "errors")
	}
	var printErr error
	if settings.format == output.FormatJSONL && a.specs[cmd].ObjectJSONL {
		printErr = output.SelectWithUntrusted(a.deps.Stdout, output.FormatJSON, output.SuccessDataWithThrottle(data, a.throttledMS), settings.fields, settings.untrusted)
	} else {
		printErr = a.emit(settings, data, nil)
	}
	if printErr != nil {
		return printErr
	}
	if partial && settings.format == output.FormatHuman {
		if err := printPartialSummary(a.deps.Stdout, cmd, data); err != nil {
			return err
		}
	}
	if partial {
		return &cmdError{exit: exitcode.Failure, printed: true}
	}
	return nil
}

// warn writes a hint or a progress line to stderr, unless --quiet asked for
// silence. An error never goes through here: it reaches the agent as a failure
// envelope whatever the flags say.
func (a *app) warn(cmd *cobra.Command, format string, args ...any) {
	if a.settings(cmd).quiet {
		return
	}
	fmt.Fprintln(a.deps.Stderr, redact.Text(fmt.Sprintf(format, args...)))
}

// settings returns the output settings of a command for a path that prints no
// result: --quiet and --verbose still hold when --format is wrong, so a hint is
// silenced rather than printed because a later error will be reported.
func (a *app) settings(cmd *cobra.Command) outputSettings {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil && settings.format == "" {
		settings.format = output.FormatJSON
	}
	return settings
}

// diagnose writes a diagnostic line to stderr under --verbose. It reports the
// shape of a call (method, socket path, how long it took), never what the call
// returned: no message text, no chat title, no name, no phone, no secret.
func (a *app) diagnose(cmd *cobra.Command, format string, args ...any) {
	if cmd == nil || !a.settings(cmd).verbose {
		return
	}
	fmt.Fprintf(a.deps.Stderr, "debug: "+format+"\n", args...)
}

// failure prints the failure envelope in the format the run asked for, falling
// back to JSON when the flag value itself is the problem or the command never
// got far enough to know its formats.
func (a *app) failure(cmd *cobra.Command, exit int, message string) {
	format, err := requestedFormat(cmd.Root())
	if err != nil {
		format = output.FormatJSON // the flag value itself is the problem
	}
	// A write error on stdout cannot be reported anywhere else, and it must not
	// replace the command's own failure: the exit code already tells the agent
	// that something went wrong.
	_ = output.FailureWithThrottle(a.deps.Stdout, format, exitcode.Name(exit), redact.Text(message), a.throttledMS)
}
