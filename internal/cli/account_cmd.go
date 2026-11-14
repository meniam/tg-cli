package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) accountCommand() *cobra.Command {
	root := &cobra.Command{Use: "account", Short: "Read account state"}
	sessions := a.annotate(&cobra.Command{Use: "sessions", Short: "List account sessions (read only)", Args: cobra.NoArgs, RunE: a.accountSessions}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.SessionsResult](), ItemPath: "sessions"})
	blocked := a.annotate(&cobra.Command{Use: "blocked", Short: "List blocked peers (read only)", Long: "List blocked peers. Pass next_cursor with --cursor to continue. A filtered page can be empty while a cursor remains.", Args: cobra.NoArgs, RunE: a.accountBlocked}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.BlockedResult](), ItemPath: "peers"})
	notify := a.annotate(&cobra.Command{Use: "notify", Short: "Show default notification settings (read only)", Args: cobra.NoArgs, RunE: a.accountNotify}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.NotifyDefaultsResult](), ItemPath: "defaults"})
	for _, cmd := range []*cobra.Command{sessions, blocked, notify} {
		cmd.Flags().Bool("local", false, "read the saved snapshot without Telegram")
	}
	blocked.Flags().Int("limit", 100, "blocked peers per page, 1 to 1000")
	blocked.Flags().String("cursor", "", "continue after a previous page; cannot be used with --local")
	root.AddCommand(sessions, blocked, notify)
	return root
}
func (a *app) accountSessions(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	var r rpc.SessionsResult
	if err = a.callOne(cmd, rpc.MethodAccountSessions, rpc.LocalParams{Local: local}, &r); err != nil {
		return err
	}
	return emitList(a, cmd, settings, r, r.Sessions, "")
}
func (a *app) accountBlocked(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := chatListLimit(cmd, 1000)
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	cursor, _ := cmd.Flags().GetString("cursor")
	p := rpc.BlockedParams{Limit: limit, Local: local, Cursor: cursor}
	if _, err = rpc.BlockedOffset(p); err != nil {
		return fail(exitcode.Usage, "%v", err)
	}
	var r rpc.BlockedResult
	if err = a.callOne(cmd, rpc.MethodAccountBlocked, p, &r); err != nil {
		return err
	}
	return emitList(a, cmd, settings, r, r.Peers, r.NextCursor)
}
func (a *app) accountNotify(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	var r rpc.NotifyDefaultsResult
	if err = a.callOne(cmd, rpc.MethodAccountNotify, rpc.LocalParams{Local: local}, &r); err != nil {
		return err
	}
	return emitList(a, cmd, settings, r, r.Defaults, "")
}
