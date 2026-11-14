package cli

import (
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

const maxTextInputBytes = 64 << 10

func (a *app) messageWriteCommands() []*cobra.Command {
	send := a.annotate(&cobra.Command{Use: "send <chat>", Short: "Send a plain text message", Args: cobra.ExactArgs(1), RunE: a.messageSend}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.SendResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	send.Flags().String("text", "", "message text; reads stdin when absent")
	send.Flags().Int("reply-to", 0, "reply to this message id")
	send.Flags().Int("topic", 0, "send in this forum topic")
	send.Flags().Bool("silent", false, "send without a notification")
	send.Flags().Bool("dry-run", false, "describe the action without sending")

	edit := a.annotate(&cobra.Command{Use: "edit <chat> <id>", Short: "Edit a message", Args: cobra.ExactArgs(2), RunE: a.messageEdit}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.SendResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}})
	edit.Flags().String("text", "", "replacement text; reads stdin when absent")
	edit.Flags().Bool("yes", false, "confirm the edit")
	edit.Flags().Bool("dry-run", false, "describe the edit without changing Telegram")

	del := a.annotate(&cobra.Command{Use: "delete <chat> <id>...", Short: "Delete messages", Args: cobra.MinimumNArgs(2), RunE: a.messageDelete}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.DeleteResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true, Variadic: true}}})
	del.Flags().Bool("revoke", false, "delete for everyone")
	del.Flags().Bool("for-me", false, "delete only for this account")
	del.Flags().Bool("yes", false, "confirm the deletion")
	del.Flags().Bool("dry-run", false, "describe the deletion without changing Telegram")

	forward := a.annotate(&cobra.Command{Use: "forward <from> <to> <id>...", Short: "Forward messages", Args: cobra.MinimumNArgs(3), RunE: a.messageForward}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.ForwardResult](), Args: []argSpec{{Name: "from", Required: true}, {Name: "to", Required: true}, {Name: "id", Required: true, Variadic: true}}})
	forward.Flags().Bool("silent", false, "forward without a notification")
	forward.Flags().Bool("dry-run", false, "describe the action without forwarding")
	return []*cobra.Command{send, edit, del, forward}
}

func (a *app) writeText(cmd *cobra.Command) (string, error) {
	specified := cmd.Flags().Changed("text")
	value, _ := cmd.Flags().GetString("text")
	var stdin []byte
	if a.deps.Stdin != nil && (!specified || !terminalInput(a.deps.Stdin)) {
		var err error
		stdin, err = io.ReadAll(io.LimitReader(a.deps.Stdin, maxTextInputBytes+1))
		if err != nil {
			return "", fail(exitcode.Failure, "read message text: %v", err)
		}
		if len(stdin) > maxTextInputBytes {
			return "", fail(exitcode.Usage, "message text is too long")
		}
	}
	if specified && len(stdin) > 0 {
		return "", fail(exitcode.Usage, "use either --text or stdin, not both")
	}
	if !specified {
		value = string(stdin)
	}
	if strings.TrimSpace(value) == "" {
		return "", fail(exitcode.Usage, "message text must not be empty")
	}
	if utf8.RuneCountInString(value) > 4096 {
		return "", fail(exitcode.Usage, "message text must be at most 4096 characters")
	}
	return value, nil
}
func terminalInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
func positiveMessageID(text string) (int, error) {
	id, err := strconv.Atoi(text)
	if err != nil || id <= 0 {
		return 0, fail(exitcode.Usage, "message id must be positive: %q", text)
	}
	return id, nil
}
func messageIDs(args []string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, arg := range args {
		id, err := positiveMessageID(arg)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
func (a *app) writeCall(cmd *cobra.Command, method string, p, result any) error {
	if err := a.callOne(cmd, method, p, result); err != nil {
		if ce, ok := err.(*cmdError); ok && ce.exit == exitcode.WriteDisabled {
			a.warn(cmd, "add the chat to the write list in this profile's access.json")
		}
		return err
	}
	return a.success(cmd, result)
}
func (a *app) messageSend(cmd *cobra.Command, args []string) error {
	text, err := a.writeText(cmd)
	if err != nil {
		return err
	}
	reply, _ := cmd.Flags().GetInt("reply-to")
	topic, _ := cmd.Flags().GetInt("topic")
	if reply < 0 || topic < 0 {
		return fail(exitcode.Usage, "reply and topic ids must not be negative")
	}
	silent, _ := cmd.Flags().GetBool("silent")
	dry, _ := cmd.Flags().GetBool("dry-run")
	var result rpc.SendResult
	return a.writeCall(cmd, rpc.MethodMessageSend, rpc.SendParams{Chat: args[0], Text: text, ReplyTo: reply, Topic: topic, Silent: silent, DryRun: dry}, &result)
}
func (a *app) messageEdit(cmd *cobra.Command, args []string) error {
	id, err := positiveMessageID(args[1])
	if err != nil {
		return err
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	if !dry && !yes {
		return fail(exitcode.Usage, "add --yes to edit or --dry-run to preview")
	}
	text, err := a.writeText(cmd)
	if err != nil {
		return err
	}
	var result rpc.SendResult
	return a.writeCall(cmd, rpc.MethodMessageEdit, rpc.EditParams{Chat: args[0], ID: id, Text: text, DryRun: dry}, &result)
}
func (a *app) messageDelete(cmd *cobra.Command, args []string) error {
	ids, err := messageIDs(args[1:])
	if err != nil {
		return err
	}
	revoke, _ := cmd.Flags().GetBool("revoke")
	forMe, _ := cmd.Flags().GetBool("for-me")
	if revoke == forMe {
		return fail(exitcode.Usage, "choose exactly one of --revoke or --for-me")
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	if !dry && !yes {
		return fail(exitcode.Usage, "add --yes to delete or --dry-run to preview")
	}
	var result rpc.DeleteResult
	return a.writeCall(cmd, rpc.MethodMessageDelete, rpc.DeleteParams{Chat: args[0], IDs: ids, Revoke: revoke, ForMe: forMe, DryRun: dry}, &result)
}
func (a *app) messageForward(cmd *cobra.Command, args []string) error {
	ids, err := messageIDs(args[2:])
	if err != nil {
		return err
	}
	silent, _ := cmd.Flags().GetBool("silent")
	dry, _ := cmd.Flags().GetBool("dry-run")
	var result rpc.ForwardResult
	return a.writeCall(cmd, rpc.MethodMessageForward, rpc.ForwardParams{From: args[0], To: args[1], IDs: ids, Silent: silent, DryRun: dry}, &result)
}
