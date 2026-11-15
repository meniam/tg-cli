package cli

import (
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) messageExportMonthCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "export-month <chat>...", Short: "Export a completed UTC month to an allowed directory", Args: cobra.RangeArgs(1, 50), RunE: a.messageExportMonth}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.ExportMonthResult](), ItemPath: "files", Args: []argSpec{{Name: "chat", Required: true, Variadic: true}}})
	cmd.Flags().String("month", "", "completed UTC month (YYYY-MM)")
	cmd.Flags().String("output", "", "required absolute output directory")
	cmd.Flags().Bool("include-deleted", false, "include locally marked deleted messages")
	return cmd
}
func (a *app) messageExportMonth(cmd *cobra.Command, args []string) error {
	month, _ := cmd.Flags().GetString("month")
	if _, _, err := rpc.ParseExportMonth(month, time.Now()); err != nil {
		return fail(exitcode.Usage, "month must be a completed UTC month in YYYY-MM format")
	}
	dir, _ := cmd.Flags().GetString("output")
	if !filepath.IsAbs(dir) {
		return fail(exitcode.Usage, "--output must be an absolute directory")
	}
	deleted, _ := cmd.Flags().GetBool("include-deleted")
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var result rpc.ExportMonthResult
	if err = a.callOne(cmd, rpc.MethodMessageExportMonth, rpc.ExportMonthParams{Chats: args, Month: month, Output: dir, IncludeDeleted: deleted}, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Files, "")
}
func (a *app) messageExportVerifyCommand() *cobra.Command {
	return a.annotate(&cobra.Command{Use: "export-verify <archive-directory>", Short: "Verify an exported month", Args: cobra.ExactArgs(1), RunE: a.messageExportVerify}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ExportVerifyResult](), ItemPath: "files", Args: []argSpec{{Name: "archive_directory", Required: true}}})
}
func (a *app) messageExportVerify(cmd *cobra.Command, args []string) error {
	if !filepath.IsAbs(args[0]) {
		return fail(exitcode.Usage, "archive directory must be absolute")
	}
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	var result rpc.ExportVerifyResult
	if err = a.callOne(cmd, rpc.MethodMessageExportVerify, rpc.ExportVerifyParams{Dir: args[0]}, &result); err != nil {
		return err
	}
	if err = emitList(a, cmd, settings, result, result.Files, ""); err != nil {
		return err
	}
	if !result.OK {
		return &cmdError{exit: exitcode.Failure, printed: true}
	}
	return nil
}
