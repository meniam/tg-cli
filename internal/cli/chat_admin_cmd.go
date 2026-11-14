package cli

import (
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tg-cli/internal/chatref"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) chatAdminCommands() []*cobra.Command {
	var commands []*cobra.Command
	for _, cfg := range []struct {
		name, method string
		args         int
		danger       bool
	}{
		{"join", rpc.MethodChatJoin, 1, false}, {"leave", rpc.MethodChatLeave, 1, true}, {"create", rpc.MethodChatCreate, 1, false}, {"edit", rpc.MethodChatEdit, 1, false}, {"kick", rpc.MethodChatKick, 2, true}, {"ban", rpc.MethodChatBan, 2, true}, {"unban", rpc.MethodChatUnban, 2, false}, {"restrict", rpc.MethodChatRestrict, 2, false}, {"permissions", rpc.MethodChatPermissions, 1, false}, {"slow-mode", rpc.MethodChatSlowMode, 2, false},
	} {
		c := cfg
		risk := noRisk
		if c.danger {
			risk = dangerous
		}
		names := []argSpec{{Name: "chat", Required: true}}
		usage := c.name + " <chat>"
		if c.name == "create" {
			names[0].Name = "title"
			usage = c.name + " <title>"
		}
		if c.args == 2 {
			name := "user"
			if c.name == "slow-mode" {
				name = "seconds"
			}
			names = append(names, argSpec{Name: name, Required: true})
			usage += " <" + name + ">"
		}
		spec := commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.ChatAdminResult](), Args: names}
		switch c.name {
		case "join":
			spec.Result = typeOf[rpc.ChatJoinResult]()
		case "leave":
			spec.Result = typeOf[rpc.ChatLeaveResult]()
		case "create":
			spec.Result = typeOf[rpc.ChatCreateResult]()
			spec.Choices = []flagChoices{{Flag: "type", Values: []string{"supergroup", "channel"}}}
		case "edit":
			spec.Result = typeOf[rpc.ChatEditResult]()
		case "restrict", "permissions":
			spec.Choices = []flagChoices{{Flag: "deny", Values: rpc.ChatAdminRights}}
		}
		cmd := a.annotate(&cobra.Command{Use: usage, Short: "Manage chat: " + c.name, Args: cobra.ExactArgs(c.args), RunE: func(cmd *cobra.Command, args []string) error {
			return a.runChatAdmin(cmd, c.name, c.method, c.danger, args)
		}}, risk, spec)
		cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
		if c.danger {
			cmd.Flags().Bool("yes", false, "confirm the action")
		}
		switch c.name {
		case "create":
			cmd.Flags().String("type", "", "chat type: supergroup or channel")
			cmd.Flags().String("about", "", "description")
		case "edit":
			cmd.Flags().String("title", "", "new title")
			cmd.Flags().String("about", "", "new description")
		case "ban":
			cmd.Flags().String("until", "", "expiry in RFC 3339")
		case "restrict":
			cmd.Flags().String("deny", "", "comma-separated denied rights")
			cmd.Flags().Bool("clear", false, "clear all restrictions")
			cmd.Flags().String("until", "", "expiry in RFC 3339")
		case "permissions":
			cmd.Flags().String("deny", "", "comma-separated denied rights")
			cmd.Flags().Bool("clear", false, "clear all restrictions")
		}
		commands = append(commands, cmd)
	}
	return commands
}
func (a *app) runChatAdmin(cmd *cobra.Command, name, method string, danger bool, args []string) error {
	dry, _ := cmd.Flags().GetBool("dry-run")
	if danger {
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes && !dry {
			return fail(exitcode.Usage, "add --yes to confirm or --dry-run to preview")
		}
	}
	p := map[string]any{"dry_run": dry}
	if name == "create" {
		p["title"] = args[0]
		kind, _ := cmd.Flags().GetString("type")
		if kind != "channel" && kind != "supergroup" {
			return fail(exitcode.Usage, "type must be supergroup or channel")
		}
		p["type"] = kind
		if err := rpc.ValidateChatTitle(args[0]); err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
	} else {
		ref, err := chatref.Parse(args[0])
		if err != nil || (ref.Kind == chatref.Invite && name != "join") {
			return fail(exitcode.Usage, "invalid chat reference")
		}
		p["chat"] = args[0]
	}
	if name == "kick" || name == "ban" || name == "unban" || name == "restrict" {
		ref, err := chatref.Parse(args[1])
		if err != nil || (ref.Kind != chatref.Username && (ref.Kind != chatref.ID || ref.ID <= 0)) {
			return fail(exitcode.Usage, "user must be a username or positive id")
		}
		p["user"] = args[1]
	}
	if name == "create" || name == "edit" {
		for _, flag := range []string{"title", "about"} {
			if cmd.Flags().Lookup(flag) != nil && cmd.Flags().Changed(flag) {
				v, _ := cmd.Flags().GetString(flag)
				var err error
				if flag == "title" {
					err = rpc.ValidateChatTitle(v)
				} else {
					err = rpc.ValidateChatAbout(v)
				}
				if err != nil {
					return fail(exitcode.Usage, "%s", err.Error())
				}
				p[flag] = v
			}
		}
		if name == "edit" && !cmd.Flags().Changed("title") && !cmd.Flags().Changed("about") {
			return fail(exitcode.Usage, "title or about is required")
		}
	}
	if name == "ban" || name == "restrict" {
		until, _ := cmd.Flags().GetString("until")
		if err := rpc.ValidateUntil(until, time.Now()); err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		p["until"] = until
	}
	if name == "restrict" || name == "permissions" {
		deny, _ := cmd.Flags().GetString("deny")
		clear, _ := cmd.Flags().GetBool("clear")
		var rights []string
		if deny != "" {
			rights = strings.Split(deny, ",")
		}
		if err := rpc.ValidateRights(rights, clear); err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		p["deny"] = rights
		p["clear"] = clear
	}
	if name == "slow-mode" {
		seconds, err := strconv.Atoi(args[1])
		if err != nil {
			return fail(exitcode.Usage, "invalid slow mode seconds")
		}
		if err = rpc.ValidateSlowModeSeconds(seconds); err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		p["seconds"] = seconds
	}
	var result map[string]any
	if err := a.callOne(cmd, method, p, &result); err != nil {
		return err
	}
	return a.success(cmd, result)
}
