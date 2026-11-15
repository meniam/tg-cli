package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

func (a *app) chatMembersCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use:   "members <chat>",
		Short: "List members of a group, supergroup or channel (read only)",
		Long: "List members of a group, supergroup or channel. A channel may require administrator access.\n" +
			"Use -- before a negative chat id, for example: tg chat members -- -1001234567890.\n" +
			"--filter and --search may be combined: Telegram selects the role, then the text is\n" +
			"matched against names and usernames in that page only. Use next_offset to continue.",
		Args: cobra.ExactArgs(1),
		RunE: a.chatMembers,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MembersResult](), Args: []argSpec{{Name: "chat", Required: true}}, Choices: []flagChoices{{Flag: "filter", Values: []string{"recent", "admins", "bots"}}}, ItemPath: "members"})
	cmd.Flags().Bool("all", false, "stream all pages as JSONL")
	cmd.Flags().String("search", "", "find members by name or username")
	cmd.Flags().Int("offset", 0, "number of members to skip, 0 or greater")
	cmd.Flags().Int("limit", 50, "members per page, 1 to 200")
	cmd.Flags().String("filter", "", "member filter: recent, admins or bots")
	return cmd
}

func (a *app) chatTopicsCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use:   "topics <chat>",
		Short: "List topics of a forum chat (read only)",
		Long: "List topics of a forum chat. Use -- before a negative chat id.\n" +
			"Pass next_cursor back with --cursor to continue.",
		Args: cobra.ExactArgs(1),
		RunE: a.chatTopics,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.TopicsResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "topics"})
	cmd.Flags().Int("limit", 50, "topics per page, 1 to 100")
	cmd.Flags().String("cursor", "", "continue after a previous page")
	return cmd
}

func (a *app) chatCommonCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use:   "common <user>",
		Short: "List chats shared with a user (read only)",
		Long: "List chats shared with a user given by @username or positive numeric id.\n" +
			"Closed chats are hidden. A page may be empty even when next_cursor is present;\n" +
			"pass that cursor back with --cursor to continue.",
		Args: cobra.ExactArgs(1),
		RunE: a.chatCommon,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.CommonChatsResult](), Args: []argSpec{{Name: "user", Required: true}}, ItemPath: "chats"})
	cmd.Flags().Int("limit", 50, "shared chats per page, 1 to 100")
	cmd.Flags().String("cursor", "", "continue after a previous page")
	return cmd
}

func chatListLimit(cmd *cobra.Command, maximum int) (int, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > maximum {
		return 0, fail(exitcode.Usage, "--limit must be between 1 and %d", maximum)
	}
	return limit, nil
}

func (a *app) chatMembers(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all")
	if all {
		return a.chatMembersAll(cmd, args, settings)
	}
	limit, err := chatListLimit(cmd, 200)
	if err != nil {
		return err
	}
	offset, _ := cmd.Flags().GetInt("offset")
	if offset < 0 {
		return fail(exitcode.Usage, "--offset must not be negative")
	}
	filter, _ := cmd.Flags().GetString("filter")
	if filter != "" && filter != "recent" && filter != "admins" && filter != "bots" {
		return fail(exitcode.Usage, "--filter must be recent, admins, or bots")
	}
	search, _ := cmd.Flags().GetString("search")
	var res rpc.MembersResult
	if err := a.callOne(cmd, rpc.MethodChatMembers, rpc.MembersParams{Chat: args[0], Search: search, Offset: offset, Limit: limit, Filter: filter}, &res); err != nil {
		return err
	}
	if err := emitMembersList(a, cmd, settings, res); err != nil {
		return err
	}
	if res.NextOffset > 0 {
		a.warn(cmd, "more members: continue with --offset %d", res.NextOffset)
	}
	return nil
}

func emitMembersList(a *app, cmd *cobra.Command, settings outputSettings, res rpc.MembersResult) error {
	if settings.format != output.FormatJSONL {
		return emitList(a, cmd, settings, res, res.Members, "")
	}
	paths := output.ItemPaths(settings.fields, a.specs[cmd].ItemPath)
	return a.emit(settings, res, func(w io.Writer) error {
		if err := output.SelectLinesWithThrottle(w, res.Members, "", paths, 0); err != nil {
			return err
		}
		if res.NextOffset == 0 && a.throttledMS == 0 {
			return nil
		}
		return json.NewEncoder(w).Encode(struct {
			NextOffset  int   `json:"next_offset,omitempty"`
			ThrottledMS int64 `json:"throttled_ms,omitempty"`
		}{res.NextOffset, a.throttledMS})
	})
}

func (a *app) chatTopics(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := chatListLimit(cmd, 100)
	if err != nil {
		return err
	}
	cursor, _ := cmd.Flags().GetString("cursor")
	var res rpc.TopicsResult
	if err := a.callOne(cmd, rpc.MethodChatTopics, rpc.TopicsParams{Chat: args[0], Limit: limit, Cursor: cursor}, &res); err != nil {
		return err
	}
	if err := emitList(a, cmd, settings, res, res.Topics, res.NextCursor); err != nil {
		return err
	}
	if res.NextCursor != "" {
		a.warn(cmd, "more topics: continue with --cursor %q", res.NextCursor)
	}
	return nil
}

func (a *app) chatCommon(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := chatListLimit(cmd, 100)
	if err != nil {
		return err
	}
	cursor, _ := cmd.Flags().GetString("cursor")
	var res rpc.CommonChatsResult
	if err := a.callOne(cmd, rpc.MethodChatCommon, rpc.CommonChatsParams{User: args[0], Limit: limit, Cursor: cursor}, &res); err != nil {
		return err
	}
	if err := emitList(a, cmd, settings, res, res.Chats, res.NextCursor); err != nil {
		return err
	}
	if res.NextCursor != "" {
		if len(res.Chats) == 0 {
			a.warn(cmd, "closed chats are hidden; this page is empty. Continue with --cursor %q", res.NextCursor)
		} else {
			a.warn(cmd, "more shared chats: continue with --cursor %q", res.NextCursor)
		}
	}
	return nil
}
