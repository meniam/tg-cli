package cli

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/chatref"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// searchCommandResult describes both result shapes of message search in schema.
type searchCommandResult struct {
	Messages   []rpc.Message   `json:"messages,omitempty"`
	Hits       []rpc.PublicHit `json:"hits,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func discoveryMediaChoices() []flagChoices {
	return []flagChoices{{Flag: "media", Values: []string{"photo", "video", "document", "voice", "audio", "link", "gif", "pinned"}}}
}
func (a *app) messageDiscoveryCommands() []*cobra.Command {
	search := a.annotate(&cobra.Command{
		Use: "search <chat> <query> | search --public <query>", Short: "Search Telegram messages (read only)",
		Long: "Search one chat, or use --public to search across Telegram. The query is passed as plain text, including hashtags. A cursor continues the same search.",
		Args: func(cmd *cobra.Command, args []string) error {
			public, _ := cmd.Flags().GetBool("public")
			want := 2
			if public {
				want = 1
			}
			if len(args) != want {
				return fail(exitcode.Usage, "message search needs %d argument(s) with the selected --public mode", want)
			}
			return nil
		}, RunE: a.messageSearch,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[searchCommandResult](), Args: []argSpec{{Name: "chat", Required: false}, {Name: "query", Required: true}}, ItemPath: "messages", PublicItemPath: "hits", Choices: discoveryMediaChoices()})
	search.Flags().Bool("public", false, "search globally rather than inside one chat")
	search.Flags().String("media", "", "filter by media type in one chat")
	search.Flags().Int("limit", 20, "results per page, 1 to 100")
	search.Flags().String("cursor", "", "continue after a previous page")

	context := a.annotate(&cobra.Command{Use: "context <chat> <id> | context <message-link>", Short: "Read messages around one message (read only)", Args: cobra.RangeArgs(1, 2), RunE: a.messageContext}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Required: false}}, ItemPath: "messages"})
	context.Flags().Int("before", 5, "messages before the target, 0 to 50 (default 5)")
	context.Flags().Int("after", 5, "messages after the target, 0 to 50 (default 5)")
	context.Flags().Bool("local", false, "read only the local database, without Telegram")

	comments := a.annotate(&cobra.Command{Use: "comments <chat> <id> | comments <message-link>", Short: "Read comments on a channel post (read only)", Args: cobra.RangeArgs(1, 2), RunE: a.messageComments}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Required: false}}, ItemPath: "messages"})
	comments.Flags().Int("limit", 20, "comments per page, 1 to 100")
	comments.Flags().String("cursor", "", "continue after a previous page")

	link := a.annotate(&cobra.Command{Use: "link <chat> <id>", Short: "Build a link to a channel message (read only)", Args: cobra.ExactArgs(2), RunE: a.messageLink}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.LinkResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}})
	link.Flags().Int("topic", 0, "forum topic id, when the message belongs to a topic")
	return []*cobra.Command{search, context, comments, link}
}

func (a *app) chatMarkReadCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "mark-read <chat>", Short: "Mark a chat as read (account write)", Args: cobra.ExactArgs(1), RunE: a.chatMarkRead}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.MarkReadResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.Flags().Bool("dry-run", false, "check write access without marking the chat read")
	return cmd
}

func cliPageLimit(cmd *cobra.Command) (int, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > 100 {
		return 0, fail(exitcode.Usage, "--limit must be between 1 and 100")
	}
	return limit, nil
}
func positiveID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, fail(exitcode.Usage, "the message id must be a positive number, got %q", value)
	}
	return id, nil
}
func messageTarget(args []string) (string, int, error) {
	if len(args) == 1 {
		ref, err := chatref.ParseMessageLink(args[0])
		if err != nil {
			return "", 0, fail(exitcode.Usage, "%v", err)
		}
		return ref.Chat.String(), ref.ID, nil
	}
	if len(args) != 2 {
		return "", 0, fail(exitcode.Usage, "expected a chat and message id, or one message link")
	}
	id, err := positiveID(args[1])
	if err != nil {
		return "", 0, err
	}
	return args[0], id, nil
}
func (a *app) messageSearch(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := cliPageLimit(cmd)
	if err != nil {
		return err
	}
	media, _ := cmd.Flags().GetString("media")
	cursor, _ := cmd.Flags().GetString("cursor")
	public, _ := cmd.Flags().GetBool("public")
	if media != "" {
		valid := false
		for _, name := range discoveryMediaChoices()[0].Values {
			if media == name {
				valid = true
				break
			}
		}
		if !valid {
			return fail(exitcode.Usage, "unknown media filter %q", media)
		}
	}
	if public {
		if media != "" {
			return fail(exitcode.Usage, "--media is only available for one-chat search")
		}
		if strings.TrimSpace(args[0]) == "" {
			return fail(exitcode.Usage, "search query must not be empty")
		}
		var res rpc.PublicSearchResult
		if err := a.callOne(cmd, rpc.MethodMessageSearchPublic, rpc.PublicSearchParams{Query: args[0], Limit: limit, Cursor: cursor}, &res); err != nil {
			return err
		}
		return emitListAtPath(a, cmd, settings, "hits", res, res.Hits, res.NextCursor)
	}
	if strings.TrimSpace(args[1]) == "" && media == "" {
		return fail(exitcode.Usage, "search query or media filter is required")
	}
	var res rpc.MessagesResult
	if err := a.callOne(cmd, rpc.MethodMessageSearch, rpc.SearchMessagesParams{Chat: args[0], Query: args[1], Media: media, Limit: limit, Cursor: cursor}, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Messages, res.NextCursor)
}
func (a *app) messageContext(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	chat, id, err := messageTarget(args)
	if err != nil {
		return err
	}
	before, _ := cmd.Flags().GetInt("before")
	after, _ := cmd.Flags().GetInt("after")
	local, _ := cmd.Flags().GetBool("local")
	if before < 0 || before > 50 || after < 0 || after > 50 {
		return fail(exitcode.Usage, "--before and --after must be between 0 and 50")
	}
	// Include zero explicitly: omitted RPC values mean the Daemon's default of five.
	p := map[string]any{"chat": chat, "id": id, "before": before, "after": after, "local": local}
	var res rpc.MessagesResult
	if err := a.callOne(cmd, rpc.MethodMessageContext, p, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Messages, res.NextCursor)
}
func (a *app) messageComments(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	chat, id, err := messageTarget(args)
	if err != nil {
		return err
	}
	limit, err := cliPageLimit(cmd)
	if err != nil {
		return err
	}
	cursor, _ := cmd.Flags().GetString("cursor")
	var res rpc.MessagesResult
	if err := a.callOne(cmd, rpc.MethodMessageComments, rpc.CommentsParams{Chat: chat, ID: id, Limit: limit, Cursor: cursor}, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Messages, res.NextCursor)
}
func (a *app) messageLink(cmd *cobra.Command, args []string) error {
	id, err := positiveID(args[1])
	if err != nil {
		return err
	}
	topic, _ := cmd.Flags().GetInt("topic")
	if topic < 0 {
		return fail(exitcode.Usage, "--topic must not be negative")
	}
	var res rpc.LinkResult
	if err := a.callOne(cmd, rpc.MethodMessageLink, rpc.LinkParams{Chat: args[0], ID: id, Topic: topic}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}
func (a *app) chatMarkRead(cmd *cobra.Command, args []string) error {
	dry, _ := cmd.Flags().GetBool("dry-run")
	var res rpc.MarkReadResult
	return a.writeCall(cmd, rpc.MethodChatMarkRead, rpc.MarkReadParams{Chat: args[0], DryRun: dry}, &res)
}
