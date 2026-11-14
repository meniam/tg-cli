package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func (a *app) messageExportCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use: "export <chat>", Short: "Stream locally saved messages in ID order",
		Long: "Export messages already saved in this profile's database, oldest ID first.\n" +
			"--local is required; exporting directly from Telegram is not implemented.\n" +
			"The default format is jsonl, one message per line. Use --after with the last\n" +
			"printed id to resume. --limit caps the total number of printed messages.\n" +
			"--since and --until are inclusive RFC 3339 times. Put -- before a negative chat id.",
		Args: cobra.ExactArgs(1), RunE: a.messageExport,
	}, noRisk, commandSpec{
		Safety: safetyRead, Result: typeOf[rpc.ExportPage](), ItemPath: "messages",
		Formats: []output.Format{output.FormatJSONL, output.FormatHuman}, DefaultJSONL: true,
		Args: []argSpec{{Name: "chat", Required: true}},
	})
	cmd.Flags().Bool("local", false, "read only messages already stored locally")
	cmd.Flags().String("since", "", "messages sent at this time or later (RFC 3339)")
	cmd.Flags().String("until", "", "messages sent at this time or earlier (RFC 3339)")
	cmd.Flags().Bool("include-deleted", false, "include locally marked deleted messages")
	cmd.Flags().Int("limit", 0, "maximum number of output messages; 0 means no limit")
	cmd.Flags().Int("after", 0, "resume after this message id; 0 means from the beginning")
	return cmd
}

func exportUntrustedPaths() []string {
	var paths []string
	for _, field := range schema.Fields(typeOf[rpc.ExportMessage]()) {
		if field.Untrusted {
			paths = append(paths, field.Path)
		}
	}
	return paths
}

func (a *app) messageExport(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	if !local {
		return fail(exitcode.Usage, "--local is required; export from Telegram is not implemented")
	}
	if settings.format != output.FormatJSONL && settings.format != output.FormatHuman {
		return fail(exitcode.Usage, "message export supports only jsonl and human formats")
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		return fail(exitcode.Usage, "--limit must not be negative")
	}
	after, _ := cmd.Flags().GetInt("after")
	if after < 0 {
		return fail(exitcode.Usage, "--after must not be negative")
	}
	since, _ := cmd.Flags().GetString("since")
	until, _ := cmd.Flags().GetString("until")
	includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
	p := rpc.ExportParams{Chat: args[0], Since: since, Until: until, IncludeDeleted: includeDeleted, After: after}
	client, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer client.Close()
	paths := output.ItemPaths(settings.fields, a.specs[cmd].ItemPath)
	untrusted := exportUntrustedPaths()
	frameWarned := false
	count, lastPrinted := 0, 0
	stop := func(err error) error {
		if count > 0 {
			a.warn(cmd, "last exported id: %d; resume with --after %d and the same filters", lastPrinted, lastPrinted)
		}
		return err
	}
	finish := func() error {
		a.warn(cmd, "exported %d locally saved messages; check completeness with `tg chat sync status <chat>`", count)
		return nil
	}
	for {
		var page rpc.ExportPage
		if err := a.call(cmd.Context(), cmd, client, rpc.MethodMessageExportLocal, p, &page); err != nil {
			return stop(err)
		}
		if len(page.Messages) == 0 && page.NextAfter != 0 {
			return stop(fail(exitcode.Failure, "the daemon returned an empty export page with a cursor"))
		}
		for _, message := range page.Messages {
			if message.ID <= p.After || count > 0 && message.ID <= lastPrinted {
				return stop(fail(exitcode.Failure, "the daemon returned messages out of order"))
			}
			selected, err := output.FilterData(message, paths)
			if err != nil {
				return stop(fail(exitcode.Failure, "filter export message: %v", err))
			}
			if settings.format == output.FormatJSONL {
				err = output.Lines(a.deps.Stdout, []any{selected}, "")
			} else {
				err = writeStreamHuman(a.deps.Stdout, selected, untrusted, &frameWarned)
			}
			if err != nil {
				return stop(fail(exitcode.Failure, "write export message: %v", err))
			}
			count++
			lastPrinted = message.ID
			if limit > 0 && count == limit {
				return finish()
			}
		}
		if page.NextAfter == 0 {
			return finish()
		}
		if len(page.Messages) == 0 || page.NextAfter != page.Messages[len(page.Messages)-1].ID || page.NextAfter <= p.After {
			return stop(fail(exitcode.Failure, "the daemon returned an invalid export cursor"))
		}
		p.After = page.NextAfter
	}
}
