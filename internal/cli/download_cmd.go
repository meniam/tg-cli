package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) messageDownloadCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "download <chat> <id>... | download <message-link>", Short: "Download message media to an allowed directory", Args: cobra.MinimumNArgs(1), RunE: a.messageDownload}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.DownloadResult](), Args: []argSpec{{Name: "chat_or_link", Required: true}, {Name: "id", Variadic: true}}, ItemPath: "files"})
	cmd.Flags().String("output", "", "required absolute output directory")
	cmd.Flags().Bool("dry-run", false, "check media without writing files")
	return cmd
}
func (a *app) messageDownload(cmd *cobra.Command, args []string) error {
	var chat string
	var ids []int
	var err error
	if len(args) == 1 {
		var id int
		chat, id, err = messageTarget(args)
		ids = []int{id}
	} else {
		chat = args[0]
		ids, err = messageIDs(args[1:])
	}
	if err != nil {
		return err
	}
	if len(ids) > 20 {
		return fail(exitcode.Usage, "provide at most 20 message ids")
	}
	dir, _ := cmd.Flags().GetString("output")
	if !filepath.IsAbs(dir) {
		return fail(exitcode.Usage, "--output must be an absolute directory")
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var res rpc.DownloadResult
	if err := a.callOne(cmd, rpc.MethodMessageDownload, rpc.DownloadParams{Chat: chat, IDs: ids, Output: dir, DryRun: dry}, &res); err != nil {
		return err
	}
	if err := emitList(a, cmd, settings, res, res.Files, ""); err != nil {
		return err
	}
	if res.Partial {
		return &cmdError{exit: exitcode.Failure, printed: true}
	}
	return nil
}
