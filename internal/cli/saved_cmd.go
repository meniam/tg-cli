package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) savedCommand() *cobra.Command {
	root := &cobra.Command{Use: "saved", Short: "Read and manage Saved Messages"}
	tags := a.annotate(&cobra.Command{Use: "tags", Short: "List Saved Messages tags", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		peer, _ := cmd.Flags().GetString("peer")
		var out rpc.SavedTagsResult
		if err = a.callOne(cmd, rpc.MethodSavedTags, rpc.SavedTagsParams{Peer: peer}, &out); err != nil {
			return err
		}
		return emitList(a, cmd, settings, out, out.Tags, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.SavedTagsResult](), ItemPath: "tags"})
	tags.Flags().String("peer", "", "filter by Saved Messages source")
	rename := a.annotate(&cobra.Command{Use: "tag-rename <emoji>", Short: "Rename a Saved Messages tag", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		clear, _ := cmd.Flags().GetBool("clear")
		dry, _ := cmd.Flags().GetBool("dry-run")
		p := rpc.SavedTagRenameParams{Emoji: args[0], Clear: clear, DryRun: dry}
		if cmd.Flags().Changed("title") {
			v, _ := cmd.Flags().GetString("title")
			p.Title = &v
		}
		if (p.Title == nil) == !p.Clear {
			return fail(exitcode.Usage, "provide exactly one of --title or --clear")
		}
		var out rpc.SavedTagRenameResult
		return a.writeCall(cmd, rpc.MethodSavedTagRename, p, &out)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.SavedTagRenameResult](), Args: []argSpec{{Name: "emoji", Required: true}}})
	rename.Flags().String("title", "", "new tag title")
	rename.Flags().Bool("clear", false, "remove the tag title")
	rename.Flags().Bool("dry-run", false, "preview without changing Telegram")
	root.AddCommand(tags, rename, a.savedMessagesCommand(false), a.savedMessagesCommand(true), a.savedListCommand())
	return root
}

func (a *app) savedMessagesCommand(search bool) *cobra.Command {
	name := "history <peer>"
	argsCheck := cobra.ExactArgs(1)
	if search {
		name = "search [query]"
		argsCheck = cobra.MaximumNArgs(1)
	}
	cmd := a.annotate(&cobra.Command{Use: name, Short: "Read Saved Messages", Args: argsCheck, RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		cursor, _ := cmd.Flags().GetString("cursor")
		p := rpc.SavedMessagesParams{Limit: limit, Cursor: cursor}
		method := rpc.MethodSavedHistory
		if search {
			method = rpc.MethodSavedSearch
			if len(args) > 0 {
				p.Query = args[0]
			}
			p.Peer, _ = cmd.Flags().GetString("peer")
			p.Tag, _ = cmd.Flags().GetString("tag")
			if p.Query == "" && p.Tag == "" {
				return fail(exitcode.Usage, "search query or tag is required")
			}
		} else {
			p.Peer = args[0]
		}
		var out rpc.MessagesResult
		if err = a.callOne(cmd, method, p, &out); err != nil {
			return err
		}
		return emitList(a, cmd, settings, out, out.Messages, out.NextCursor)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), ItemPath: "messages", Args: []argSpec{{Name: map[bool]string{true: "query", false: "peer"}[search], Required: !search}}})
	cmd.Flags().Int("limit", 50, "maximum messages, 1 to 100")
	cmd.Flags().String("cursor", "", "continue from the previous page")
	if search {
		cmd.Flags().String("peer", "", "filter by source")
		cmd.Flags().String("tag", "", "filter by tag")
	}
	return cmd
}

func (a *app) savedListCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "list", Short: "List Saved Messages sources", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		cursor, _ := cmd.Flags().GetString("cursor")
		var result rpc.SavedListResult
		if err = a.callOne(cmd, rpc.MethodSavedList, rpc.SavedListParams{Limit: limit, Cursor: cursor}, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Dialogs, result.NextCursor)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.SavedListResult](), ItemPath: "dialogs"})
	cmd.Flags().Int("limit", 50, "maximum dialogs, 1 to 100")
	cmd.Flags().String("cursor", "", "continue from the previous page")
	return cmd
}
