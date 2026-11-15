package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
)

// prepareGroupHint changes only the selected group for this execution.
func prepareGroupHint(root *cobra.Command, args []string) {
	cmd, remaining, err := root.Find(args)
	if err != nil || cmd.Runnable() || !cmd.HasAvailableSubCommands() {
		return
	}
	for _, arg := range remaining {
		if arg == "--help" || arg == "-h" {
			return
		}
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		var names []string
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				names = append(names, sub.Name())
			}
		}
		return fail(exitcode.Usage, "choose a subcommand: %s %s; run tg schema for details", cmd.CommandPath(), strings.Join(names, "|"))
	}
}
