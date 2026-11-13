package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

// Output formats of the commands that return lists.
const (
	formatJSON  = "json"
	formatJSONL = "jsonl"
)

// outputFormats returns every accepted --format value, in a fixed order.
func outputFormats() []string { return []string{formatJSON, formatJSONL} }

// addFormatFlag adds --format to a command that returns a list.
func addFormatFlag(cmd *cobra.Command) {
	cmd.Flags().String("format", formatJSON, "output format: json (one envelope) or jsonl (one item per line, then {\"next_cursor\":...} when more follow)")
}

// format reads and checks --format.
func format(cmd *cobra.Command) (string, error) {
	f, _ := cmd.Flags().GetString("format")
	if f != formatJSON && f != formatJSONL {
		return "", fail(exitcode.Usage, "--format must be json or jsonl, got %q", f)
	}
	return f, nil
}

// emitList prints a list result: the JSON envelope, or JSONL for --format jsonl.
func emitList[T any](a *app, f string, result any, items []T, nextCursor string) error {
	if f == formatJSONL {
		return output.Lines(a.deps.Stdout, items, nextCursor)
	}
	return a.success(result)
}

// callOne connects to the daemon, runs one RPC and closes the connection.
func (a *app) callOne(cmd *cobra.Command, method string, params, result any) error {
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	return a.call(cmd.Context(), c, method, params, result)
}

func (a *app) chatResolve(cmd *cobra.Command, args []string) error {
	var res rpc.Chat
	if err := a.callOne(cmd, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(res)
}

func (a *app) chatInfo(cmd *cobra.Command, args []string) error {
	var res rpc.ChatInfo
	if err := a.callOne(cmd, rpc.MethodChatInfo, rpc.ChatRefParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(res)
}

func (a *app) chatInviteInfo(cmd *cobra.Command, args []string) error {
	var res rpc.InviteInfo
	if err := a.callOne(cmd, rpc.MethodChatInviteInfo, rpc.ChatRefParams{Chat: args[0]}, &res); err != nil {
		return err
	}
	return a.success(res)
}

func (a *app) messageCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "message", Short: "Read messages of a chat"}

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
	}, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), Args: []argSpec{{Name: "chat", Required: true}}, Choices: formatChoices()})
	history.Flags().Int("limit", 0, "messages per page, 1 to 100 (default 20)")
	history.Flags().String("since", "", "only messages sent at this time or later (RFC 3339)")
	history.Flags().String("until", "", "only messages sent at this time or earlier (RFC 3339)")
	history.Flags().Int("topic", 0, "read one topic of a forum chat, by the topic id")
	history.Flags().String("cursor", "", "continue after a previous page, the value of next_cursor")
	addFormatFlag(history)

	get := a.annotate(&cobra.Command{
		Use:   "get <chat> <id>",
		Short: "Read one message by its id (read only)",
		Args:  cobra.ExactArgs(2),
		RunE:  a.messageGet,
	}, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Message](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}})

	pinned := a.annotate(&cobra.Command{
		Use:   "pinned <chat>",
		Short: "Read the pinned messages of a chat (read only)",
		Args:  cobra.ExactArgs(1),
		RunE:  a.messagePinned,
	}, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessagesResult](), Args: []argSpec{{Name: "chat", Required: true}}, Choices: formatChoices()})
	pinned.Flags().Int("limit", 0, "how many pinned messages, 1 to 100 (default 20)")
	addFormatFlag(pinned)

	cmd.AddCommand(history, get, pinned)
	return cmd
}

func (a *app) messageHistory(cmd *cobra.Command, args []string) error {
	f, err := format(cmd)
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
	return emitList(a, f, res, res.Messages, res.NextCursor)
}

func (a *app) messageGet(cmd *cobra.Command, args []string) error {
	id, err := strconv.Atoi(args[1])
	if err != nil || id <= 0 {
		return fail(exitcode.Usage, "the message id must be a positive number, got %q", args[1])
	}
	var res rpc.Message
	if err := a.callOne(cmd, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: args[0], ID: id}, &res); err != nil {
		return err
	}
	return a.success(res)
}

func (a *app) messagePinned(cmd *cobra.Command, args []string) error {
	f, err := format(cmd)
	if err != nil {
		return err
	}
	p := rpc.PinnedParams{Chat: args[0]}
	p.Limit, _ = cmd.Flags().GetInt("limit")
	var res rpc.MessagesResult
	if err := a.callOne(cmd, rpc.MethodMessagePinned, p, &res); err != nil {
		return err
	}
	return emitList(a, f, res, res.Messages, "")
}
