package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
)

func (a *app) userCommand() *cobra.Command {
	group := &cobra.Command{Use: "user", Short: "Read and manage users"}
	profile := a.annotate(&cobra.Command{Use: "profile <user>", Short: "Read a user profile", Args: cobra.ExactArgs(1), RunE: a.userProfile}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.UserProfile](), ObjectJSONL: true, Args: []argSpec{{Name: "user", Required: true}}})
	for _, item := range []struct {
		name   string
		method string
	}{{"block", rpc.MethodUserBlock}, {"unblock", rpc.MethodUserUnblock}} {
		method := item.method
		cmd := a.annotate(&cobra.Command{Use: item.name + " <user>", Short: item.name + " a user", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if err := userRef(args[0], true); err != nil {
				return err
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			var result rpc.BlockResult
			return a.writeCall(cmd, method, rpc.BlockParams{User: args[0], DryRun: dry}, &result)
		}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.BlockResult](), ObjectJSONL: true, Args: []argSpec{{Name: "user", Required: true}}})
		cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
		group.AddCommand(cmd)
	}
	group.AddCommand(profile)
	return group
}
func (a *app) contactCommand() *cobra.Command {
	group := &cobra.Command{Use: "contact", Short: "Read and manage contacts"}
	list := a.annotate(&cobra.Command{Use: "list", Short: "List contacts", Args: cobra.NoArgs, RunE: a.contactList}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ContactListResult](), ItemPath: "contacts"})
	list.Flags().Int("limit", 100, "contacts per page, 1 to 1000")
	list.Flags().Int("offset", 0, "number of contacts to skip")
	list.Flags().Bool("local", false, "read only the local database")
	add := a.annotate(&cobra.Command{Use: "add <user>", Short: "Add a contact", Args: cobra.ExactArgs(1), RunE: a.contactAdd}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.ContactAddResult](), ObjectJSONL: true, Args: []argSpec{{Name: "user", Required: true}}})
	add.Flags().String("first-name", "", "contact first name")
	add.Flags().String("last-name", "", "contact last name")
	add.Flags().String("phone", "", "contact phone number")
	add.Flags().Bool("dry-run", false, "check without changing Telegram")
	del := a.annotate(&cobra.Command{Use: "delete <user>", Short: "Delete a contact", Args: cobra.ExactArgs(1), RunE: a.contactDelete}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.ContactDeleteResult](), ObjectJSONL: true, Args: []argSpec{{Name: "user", Required: true}}})
	del.Flags().Bool("yes", false, "confirm deletion")
	del.Flags().Bool("dry-run", false, "check without changing Telegram")
	search := a.annotate(&cobra.Command{Use: "search <query>", Short: "Search contacts and public users", Args: cobra.ExactArgs(1), RunE: a.contactSearch}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ContactSearchResult](), Args: []argSpec{{Name: "query", Required: true}}, ItemPath: "contacts"})
	search.Flags().Int("limit", 20, "maximum results, 1 to 100")
	search.Flags().Bool("local", false, "search only local contacts")
	group.AddCommand(list, add, del, search)
	return group
}
func userRef(value string, allowPhone bool) error {
	if err := rpc.ValidateUserRef(value, allowPhone); err != nil {
		return fail(exitcode.Usage, "invalid user reference")
	}
	return nil
}
func (a *app) userProfile(cmd *cobra.Command, args []string) error {
	if err := userRef(args[0], true); err != nil {
		return err
	}
	var res rpc.UserProfile
	if err := a.callOne(cmd, rpc.MethodUserProfile, rpc.UserRefParams{User: args[0]}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}
func (a *app) contactAdd(cmd *cobra.Command, args []string) error {
	if err := userRef(args[0], false); err != nil {
		return err
	}
	first, _ := cmd.Flags().GetString("first-name")
	last, _ := cmd.Flags().GetString("last-name")
	phone, _ := cmd.Flags().GetString("phone")
	dry, _ := cmd.Flags().GetBool("dry-run")
	var res rpc.ContactAddResult
	return a.writeCall(cmd, rpc.MethodContactAdd, rpc.ContactAddParams{User: args[0], FirstName: first, LastName: last, Phone: phone, DryRun: dry}, &res)
}
func (a *app) contactDelete(cmd *cobra.Command, args []string) error {
	if err := userRef(args[0], true); err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	dry, _ := cmd.Flags().GetBool("dry-run")
	if !yes && !dry {
		return fail(exitcode.Usage, "--yes is required to delete a contact")
	}
	var res rpc.ContactDeleteResult
	return a.writeCall(cmd, rpc.MethodContactDelete, rpc.ContactDeleteParams{User: args[0], Yes: yes, DryRun: dry}, &res)
}
func (a *app) contactList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := chatListLimit(cmd, 1000)
	if err != nil {
		return err
	}
	offset, _ := cmd.Flags().GetInt("offset")
	if offset < 0 {
		return fail(exitcode.Usage, "--offset must not be negative")
	}
	local, _ := cmd.Flags().GetBool("local")
	var res rpc.ContactListResult
	if err = a.callOne(cmd, rpc.MethodContactList, rpc.ContactListParams{Limit: limit, Offset: offset, Local: local}, &res); err != nil {
		return err
	}
	if settings.format != output.FormatJSONL {
		return emitList(a, cmd, settings, res, res.Contacts, "")
	}
	paths := output.ItemPaths(settings.fields, a.specs[cmd].ItemPath)
	return a.emit(settings, res, func(w io.Writer) error {
		if err := output.SelectLinesWithThrottle(w, res.Contacts, "", paths, 0); err != nil {
			return err
		}
		if res.NextOffset == 0 && a.throttledMS == 0 {
			return nil
		}
		return json.NewEncoder(w).Encode(struct {
			NextOffset  int   `json:"next_offset,omitempty"`
			ThrottledMS int64 `json:"throttled_ms,omitempty"`
		}{res.NextOffset, a.throttledMS})
	})
}
func (a *app) contactSearch(cmd *cobra.Command, args []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	limit, err := chatListLimit(cmd, 100)
	if err != nil {
		return err
	}
	local, _ := cmd.Flags().GetBool("local")
	p := rpc.ContactSearchParams{Query: args[0], Limit: limit, Local: local}
	if err = rpc.ValidateContactSearchParams(p); err != nil {
		return fail(exitcode.Usage, "invalid contact search")
	}
	var res rpc.ContactSearchResult
	if err = a.callOne(cmd, rpc.MethodContactSearch, p, &res); err != nil {
		return err
	}
	return emitList(a, cmd, settings, res, res.Contacts, "")
}
