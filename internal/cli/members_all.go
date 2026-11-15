package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

func (a *app) chatMembersAll(cmd *cobra.Command, args []string, settings outputSettings) error {
	if settings.format != output.FormatJSONL {
		return fail(exitcode.Usage, "--all requires --format jsonl")
	}
	if cmd.Flags().Changed("offset") || cmd.Flags().Changed("limit") {
		return fail(exitcode.Usage, "--all cannot be combined with --offset or --limit")
	}
	filter, _ := cmd.Flags().GetString("filter")
	if filter != "" && filter != "recent" && filter != "admins" && filter != "bots" {
		return fail(exitcode.Usage, "--filter must be recent, admins, or bots")
	}
	search, _ := cmd.Flags().GetString("search")
	client, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer client.Close()
	p := rpc.MembersParams{Chat: args[0], Search: search, Filter: filter, Limit: 200}
	paths := output.ItemPaths(settings.fields, a.specs[cmd].ItemPath)
	for {
		var page rpc.MembersResult
		if err := a.call(cmd.Context(), cmd, client, rpc.MethodChatMembers, p, &page); err != nil {
			if p.Offset > 0 {
				fmt.Fprintf(a.deps.Stderr, "last offset %d\n", p.Offset)
			}
			return err
		}
		for _, member := range page.Members {
			selected, err := output.FilterData(member, paths)
			if err != nil {
				return fail(exitcode.Failure, "filter member: %v", err)
			}
			if err := output.Lines(a.deps.Stdout, []any{selected}, ""); err != nil {
				return fail(exitcode.Failure, "write member: %v", err)
			}
		}
		if page.NextOffset == 0 {
			return nil
		}
		if page.NextOffset <= p.Offset {
			return fail(exitcode.Failure, "the daemon returned an invalid members offset")
		}
		p.Offset = page.NextOffset
	}
}
