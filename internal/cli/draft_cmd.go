package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func (a *app) chatDraftCommand() *cobra.Command {
	root := &cobra.Command{Use: "draft", Short: "Manage chat drafts"}
	get := a.annotate(&cobra.Command{Use: "get <chat>", Short: "Get a chat draft", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var result rpc.Draft
		if err := a.callOne(cmd, rpc.MethodChatDraftGet, rpc.DraftParams{Chat: args[0]}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.Draft](), Args: []argSpec{{Name: "chat", Required: true}}})
	list := a.annotate(&cobra.Command{Use: "list", Short: "List visible drafts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		var result rpc.DraftList
		if err := a.callOne(cmd, rpc.MethodChatDraftList, rpc.DraftParams{}, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Drafts, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.DraftList](), ItemPath: "drafts"})
	set := a.annotate(&cobra.Command{Use: "set <chat>", Short: "Replace a chat draft", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		text, err := a.writeText(cmd)
		if err != nil {
			return err
		}
		reply, _ := cmd.Flags().GetInt("reply-to")
		dry, _ := cmd.Flags().GetBool("dry-run")
		var result rpc.DraftWriteResult
		return a.writeCall(cmd, rpc.MethodChatDraftSet, rpc.DraftParams{Chat: args[0], Text: text, ReplyTo: reply, DryRun: dry}, &result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.DraftWriteResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	set.Flags().String("text", "", "draft text; reads stdin when absent")
	set.Flags().Int("reply-to", 0, "reply to this message id")
	set.Flags().Bool("dry-run", false, "check without changing Telegram")
	clear := a.annotate(&cobra.Command{Use: "clear <chat>", Short: "Clear a chat draft", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		var result rpc.DraftWriteResult
		return a.writeCall(cmd, rpc.MethodChatDraftClear, rpc.DraftParams{Chat: args[0], DryRun: dry}, &result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.DraftWriteResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	clear.Flags().Bool("dry-run", false, "check without changing Telegram")
	root.AddCommand(get, list, set, clear)
	return root
}
