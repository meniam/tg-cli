package cli

import (
	"github.com/spf13/cobra"
	"strconv"
	"strings"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) folderInviteCommand() *cobra.Command {
	return a.annotate(&cobra.Command{Use: "invite-info <link>", Short: "Inspect a shared folder invite without joining", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var result rpc.FolderInviteResult
		if err := a.callOne(cmd, rpc.MethodFolderInviteInfo, rpc.FolderInviteParams{Link: args[0]}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.FolderInviteResult](), Args: []argSpec{{Name: "link", Required: true}}})
}

func (a *app) folderOrderCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "order <id>...", Short: "Reorder chat folders", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ids := make([]int, 0, len(args))
		for _, arg := range args {
			id, err := strconv.Atoi(arg)
			if err != nil || id < 2 {
				return fail(exitcode.Usage, "invalid folder id")
			}
			ids = append(ids, id)
		}
		dry, _ := cmd.Flags().GetBool("dry-run")
		var result rpc.FolderOrderResult
		return a.writeCall(cmd, rpc.MethodFolderOrder, rpc.FolderOrderParams{IDs: ids, DryRun: dry}, &result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.FolderOrderResult](), Args: []argSpec{{Name: "id", Required: true, Variadic: true}}})
	cmd.Flags().Bool("dry-run", false, "preview without changing Telegram")
	return cmd
}

func (a *app) folderMutationCommands() []*cobra.Command {
	var cmds []*cobra.Command
	for _, action := range []string{"create", "edit", "delete"} {
		action := action
		risk := noRisk
		if action == "delete" {
			risk = dangerous
		}
		resultType := typeOf[rpc.FolderWriteResult]()
		if action == "delete" {
			resultType = typeOf[rpc.FolderDeleteResult]()
		}
		name := "id"
		if action == "create" {
			name = "title"
		}
		cmd := a.annotate(&cobra.Command{Use: action + " <" + name + ">", Short: action + " a folder", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			p := rpc.FolderWriteParams{}
			p.DryRun, _ = cmd.Flags().GetBool("dry-run")
			if action == "create" {
				v := args[0]
				p.Title = &v
			} else {
				id, err := strconv.Atoi(args[0])
				if err != nil || id < 2 {
					return fail(exitcode.Usage, "invalid folder id")
				}
				p.ID = id
			}
			if action == "delete" {
				p.Yes, _ = cmd.Flags().GetBool("yes")
				if !p.Yes && !p.DryRun {
					return fail(exitcode.Usage, "--yes is required")
				}
			} else {
				for _, entry := range []struct {
					name string
					dst  **string
				}{{"title", &p.Title}, {"emoticon", &p.Emoticon}, {"rules", &p.Rules}} {
					if cmd.Flags().Lookup(entry.name) != nil && cmd.Flags().Changed(entry.name) {
						v, _ := cmd.Flags().GetString(entry.name)
						*entry.dst = &v
					}
				}
				if cmd.Flags().Changed("color") {
					v, _ := cmd.Flags().GetInt("color")
					p.Color = &v
				}
				for _, entry := range []struct {
					name string
					dst  *[]string
				}{{"include", &p.Include}, {"add", &p.Add}, {"remove", &p.Remove}, {"exclude", &p.Exclude}, {"unexclude", &p.Unexclude}, {"pin", &p.Pin}, {"unpin", &p.Unpin}} {
					if cmd.Flags().Lookup(entry.name) != nil {
						*entry.dst, _ = cmd.Flags().GetStringArray(entry.name)
					}
				}
				if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
					return fail(exitcode.Usage, "folder title is required")
				}
			}
			method := map[string]string{"create": rpc.MethodFolderCreate, "edit": rpc.MethodFolderEdit, "delete": rpc.MethodFolderDelete}[action]
			if action == "delete" {
				var result rpc.FolderDeleteResult
				return a.writeCall(cmd, method, p, &result)
			}
			var result rpc.FolderWriteResult
			return a.writeCall(cmd, method, p, &result)
		}}, risk, commandSpec{Safety: safetyAccountWrite, Result: resultType, Args: []argSpec{{Name: name, Required: true}}})
		cmd.Flags().Bool("dry-run", false, "preview without changing Telegram")
		if action == "delete" {
			cmd.Flags().Bool("yes", false, "confirm deletion")
		} else {
			if action == "edit" {
				cmd.Flags().String("title", "", "new folder title")
			}
			cmd.Flags().String("emoticon", "", "folder emoticon")
			cmd.Flags().Int("color", 0, "folder color ID")
			cmd.Flags().String("rules", "", "comma-separated folder rules")
			flags := []string{"exclude", "pin"}
			if action == "create" {
				flags = append(flags, "include")
			} else {
				flags = append(flags, "add", "remove", "unexclude", "unpin")
			}
			for _, flag := range flags {
				cmd.Flags().StringArray(flag, nil, "chat reference; repeat for multiple chats")
			}
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}
