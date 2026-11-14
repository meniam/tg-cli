package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/rpc"
)

func (a *app) folderCommand() *cobra.Command {
	root := &cobra.Command{Use: "folder", Short: "Read chat folders"}
	list := a.annotate(&cobra.Command{Use: "list", Short: "List chat folders (read only)", Long: "List folder rules and explicit chat lists. Use --local to read the last saved snapshot without a Telegram connection.", Args: cobra.NoArgs, RunE: a.folderList}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.FoldersResult](), ItemPath: "folders"})
	list.Flags().Bool("local", false, "read the saved snapshot without Telegram")
	root.AddCommand(list)
	return root
}
func (a *app) folderList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	var result rpc.FoldersResult
	if err = a.callOne(cmd, rpc.MethodFolderList, rpc.LocalParams{Local: local}, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Folders, "")
}
