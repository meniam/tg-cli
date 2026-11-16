package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func (a *app) messageTranslateCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "translate <chat> <id>... | translate <message-link>", Short: "Translate messages with Telegram", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var p rpc.TranslateParams
		var err error
		if len(args) == 1 {
			var id int
			p.Chat, id, err = messageTarget(args)
			p.IDs = []int{id}
		} else {
			p.Chat = args[0]
			p.IDs, err = messageIDs(args[1:])
		}
		if err != nil {
			return err
		}
		p.ToLang, _ = cmd.Flags().GetString("to")
		if err = rpc.ValidateTranslate(&p); err != nil {
			return translate(err)
		}
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		var result rpc.TranslateResult
		if err = a.callOne(cmd, rpc.MethodMessageTranslate, p, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Items, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.TranslateResult](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Variadic: true}}, ItemPath: "items"})
	cmd.Flags().String("to", "", "target language as two Latin letters (required)")
	return cmd
}
