package cli

import (
	"io"

	"github.com/spf13/cobra"

	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// emitList prints a list result: the envelope in the chosen format, or one item
// per line for JSONL. The item lines are built only when they are asked for.
//
// --fields accepts result paths and item-relative paths for every format.
func emitList[T any](a *app, cmd *cobra.Command, settings outputSettings, result any, items []T, nextCursor string) error {
	return emitListAtPath(a, cmd, settings, a.specs[cmd].ItemPath, result, items, nextCursor)
}

func emitListAtPath[T any](a *app, cmd *cobra.Command, settings outputSettings, itemPath string, result any, items []T, nextCursor string) error {
	var lines func(io.Writer) error
	if settings.format == output.FormatJSONL {
		paths := output.ItemPaths(settings.fields, itemPath)
		lines = func(w io.Writer) error {
			return output.SelectLinesWithThrottle(w, items, nextCursor, paths, a.throttledMS)
		}
	} else {
		settings.fields = output.ResultPaths(settings.fields, itemPath, schema.Fields(a.specs[cmd].Result))
	}
	return a.emit(settings, result, lines)
}

// callOne connects to the daemon, runs one RPC and closes the connection.
func (a *app) callOne(cmd *cobra.Command, method string, params, result any) error {
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	return a.call(cmd.Context(), cmd, c, method, params, result)
}

func (a *app) chatResolve(cmd *cobra.Command, args []string) error {
	var res rpc.Chat
	if err := a.callOne(cmd, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) chatInfo(cmd *cobra.Command, args []string) error {
	var res rpc.ChatInfo
	if err := a.callOne(cmd, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) chatInviteInfo(cmd *cobra.Command, args []string) error {
	var res rpc.InviteInfo
	if err := a.callOne(cmd, rpc.MethodChatInviteInfo, rpc.ChatRefParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) messageCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "message", Short: "Read and write messages"}

	history := a.annotate(&cobra.Command{
		Use:   "history <chat>",
		Short: "Read a chat from the newest message to the oldest (read only)",
		Long: "Read a chat from the newest message to the oldest. <chat> is @username, a numeric\n" +
			"id (users are positive, groups negative, channels start from -1000000000000), a\n" +
			"phone number of a contact like +15551234567, or a t.me link. Put -- before a\n" +
			"negative id: message history -- -1001234567890\n\n" +
			"The page holds 20 messages by default and 100 at most. When more follow, the\n" +
			"answer has next_cursor: pass it back with --cursor to continue. --since and\n" +
			"--until take RFC 3339 times, like 2026-01-31T10:00:00Z; both limits include a\n" +
			"message sent exactly at that time.",
		Args: cobra.ExactArgs(1),
		RunE: a.messageHistory,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "messages"})
	history.Flags().Int("limit", 0, "messages per page, 1 to 100; 0 means 20")
	history.Flags().String("since", "", "only messages sent at this time or later (RFC 3339)")
	history.Flags().String("until", "", "only messages sent at this time or earlier (RFC 3339)")
	history.Flags().Int("topic", 0, "read one topic of a forum chat, by the topic id")
	history.Flags().String("cursor", "", "continue after a previous page, the value of next_cursor")

	get := a.annotate(&cobra.Command{
		Use:   "get <chat> <id> | get <message-link>",
		Short: "Read one message by its id (read only)",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  a.messageGet,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Message](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Required: false}}})

	pinned := a.annotate(&cobra.Command{
		Use:   "pinned <chat>",
		Short: "Read the pinned messages of a chat (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.messagePinned,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "messages"})
	pinned.Flags().Int("limit", 0, "pinned messages per page, 1 to 100; 0 means 20")

	cmd.AddCommand(history, get, pinned, a.watchCommand(), a.messageExportCommand(), a.messageUnreadCommand())
	cmd.AddCommand(a.messageDiscoveryCommands()...)
	cmd.AddCommand(a.messageScheduledCommand())
	cmd.AddCommand(a.messageReadersCommand())
	cmd.AddCommand(a.messageWriteCommands()...)
	return cmd
}

func (a *app) messageHistory(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	p := rpc.HistoryParams{Chat: args[0]}
	p.Limit, _ = cmd.Flags().GetInt("limit")
	p.Since, _ = cmd.Flags().GetString("since")
	p.Until, _ = cmd.Flags().GetString("until")
	p.Topic, _ = cmd.Flags().GetInt("topic")
	p.Cursor, _ = cmd.Flags().GetString("cursor")

	var res rpc.MessagesResult
	if err := a.callOne(cmd, rpc.MethodMessageHistory, p, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Messages, res.NextCursor)
}

func (a *app) messageGet(cmd *cobra.Command, args []string) error {
	chat, id, err := messageTarget(args)
	if err != nil {
		return err
	}
	var res rpc.Message
	if err := a.callOne(cmd, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: chat, ID: id}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

func (a *app) messagePinned(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	p := rpc.PinnedParams{Chat: args[0]}
	p.Limit, _ = cmd.Flags().GetInt("limit")
	var res rpc.MessagesResult
	if err := a.callOne(cmd, rpc.MethodMessagePinned, p, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Messages, "")
}
