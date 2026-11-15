package cli

import (
	"github.com/spf13/cobra"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"time"
)

func (a *app) messageReadersCommand() *cobra.Command {
	return a.annotate(&cobra.Command{
		Use: "readers <chat> <id>", Short: "Read message read receipts", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := positiveID(args[1])
			if err != nil {
				return err
			}
			var result rpc.MessageReadersResult
			if err := a.callOne(cmd, rpc.MethodMessageReaders, rpc.MessageGetParams{Chat: args[0], ID: id}, &result); err != nil {
				return err
			}
			return a.success(cmd, result)
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.MessageReadersResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}})
}

func (a *app) accountProfileCommand() *cobra.Command {
	return a.annotate(&cobra.Command{Use: "profile", Short: "Read own profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var result rpc.AccountProfile
		if err := a.callOne(cmd, rpc.MethodAccountProfile, struct{}{}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.AccountProfile]()})
}

func (a *app) accountEmojiStatusesCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "emoji-statuses", Short: "List available emoji statuses", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		recent, _ := cmd.Flags().GetBool("recent")
		var result rpc.EmojiStatusesResult
		if err := a.callOne(cmd, rpc.MethodAccountEmojiStatuses, rpc.EmojiStatusesParams{Recent: recent}, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Statuses, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.EmojiStatusesResult](), ItemPath: "statuses"})
	cmd.Flags().Bool("recent", false, "show recently used statuses")
	return cmd
}

func (a *app) chatJoinRequestCommands() []*cobra.Command {
	out := make([]*cobra.Command, 0, 2)
	for _, cfg := range []struct{ name, method string }{{"approve", rpc.MethodChatApprove}, {"decline", rpc.MethodChatDecline}} {
		cfg := cfg
		cmd := a.annotate(&cobra.Command{Use: cfg.name + " <chat> <user>", Short: cfg.name + " a join request", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			dry, _ := cmd.Flags().GetBool("dry-run")
			var result rpc.JoinRequestActionResult
			if err := a.callOne(cmd, cfg.method, rpc.JoinRequestActionParams{Chat: args[0], User: args[1], DryRun: dry}, &result); err != nil {
				return err
			}
			return a.success(cmd, result)
		}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.JoinRequestActionResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "user", Required: true}}})
		cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
		out = append(out, cmd)
	}
	return out
}

func (a *app) chatSetCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "set <chat>", Short: "Change channel settings", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p := rpc.ChatSetParams{Chat: args[0]}
		p.DryRun, _ = cmd.Flags().GetBool("dry-run")
		for _, v := range []struct {
			name   string
			target **string
		}{{"signatures", &p.Signatures}, {"signature-profiles", &p.SignatureProfiles}, {"antispam", &p.Antispam}, {"forum", &p.Forum}, {"prehistory", &p.Prehistory}, {"join-requests", &p.JoinRequests}, {"join-to-send", &p.JoinToSend}} {
			if cmd.Flags().Changed(v.name) {
				s, _ := cmd.Flags().GetString(v.name)
				*v.target = &s
			}
		}
		if err := p.Validate(); err != nil {
			return fail(exitcode.Usage, "%v", err)
		}
		var result rpc.ChatSetResult
		if err := a.callOne(cmd, rpc.MethodChatSet, p, &result); err != nil {
			return err
		}
		return a.success(cmd, &result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.ChatSetResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	for _, name := range []string{"signatures", "signature-profiles", "antispam", "forum", "join-requests", "join-to-send"} {
		cmd.Flags().String(name, "", "set "+name+" on or off")
	}
	cmd.Flags().String("prehistory", "", "set prehistory visible or hidden")
	cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
	return cmd
}

func (a *app) accountSetCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "set", Short: "Change own profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		p := rpc.AccountSetParams{}
		p.DryRun, _ = cmd.Flags().GetBool("dry-run")
		p.EmojiStatusUntil, _ = cmd.Flags().GetString("emoji-status-until")
		for _, v := range []struct {
			name   string
			target **string
		}{{"birthday", &p.Birthday}, {"personal-channel", &p.PersonalChannel}, {"emoji-status", &p.EmojiStatus}} {
			if cmd.Flags().Changed(v.name) {
				s, _ := cmd.Flags().GetString(v.name)
				*v.target = &s
			}
		}
		if err := p.Validate(time.Now()); err != nil {
			return fail(exitcode.Usage, "%v", err)
		}
		var result rpc.AccountSetResult
		if err := a.callOne(cmd, rpc.MethodAccountSet, p, &result); err != nil {
			return err
		}
		return a.success(cmd, &result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.AccountSetResult]()})
	cmd.Flags().String("birthday", "", "birthday YYYY-MM-DD, MM-DD or none")
	cmd.Flags().String("personal-channel", "", "personal channel or none")
	cmd.Flags().String("emoji-status", "", "emoji document id or none")
	cmd.Flags().String("emoji-status-until", "", "status expiry in RFC 3339")
	cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
	return cmd
}

func (a *app) chatRequestsCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "requests <chat>", Short: "List pending join requests", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 1 || limit > 100 {
			return fail(exitcode.Usage, "limit must be between 1 and 100")
		}
		cursor, _ := cmd.Flags().GetString("cursor")
		search, _ := cmd.Flags().GetString("search")
		var result rpc.JoinRequestsResult
		if err := a.callOne(cmd, rpc.MethodChatRequests, rpc.JoinRequestsParams{Chat: args[0], Limit: limit, Cursor: cursor, Search: search}, &result); err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Requests, result.NextCursor)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.JoinRequestsResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "requests"})
	cmd.Flags().Int("limit", 20, "requests per page, 1 to 100")
	cmd.Flags().String("cursor", "", "continue from a previous page")
	cmd.Flags().String("search", "", "search pending requests")
	return cmd
}

func (a *app) accountPrivacyCommand() *cobra.Command {
	root := &cobra.Command{Use: "privacy", Short: "Read and replace privacy rules"}
	get := a.annotate(&cobra.Command{Use: "get <key>", Short: "Read privacy rules", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !rpc.ValidPrivacyKey(args[0]) {
			return fail(exitcode.Usage, "unknown privacy key")
		}
		var r rpc.PrivacyRules
		if err := a.callOne(cmd, rpc.MethodAccountPrivacyGet, rpc.PrivacyGetParams{Key: args[0]}, &r); err != nil {
			return err
		}
		return a.success(cmd, r)
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.PrivacyRules](), Args: []argSpec{{Name: "key", Required: true}}})
	set := a.annotate(&cobra.Command{Use: "set <key>", Short: "Replace privacy rules", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p := rpc.PrivacySetParams{Key: args[0]}
		p.Default, _ = cmd.Flags().GetString("default")
		p.AllowUsers, _ = cmd.Flags().GetStringArray("allow-user")
		p.DenyUsers, _ = cmd.Flags().GetStringArray("deny-user")
		p.AllowChats, _ = cmd.Flags().GetStringArray("allow-chat")
		p.DenyChats, _ = cmd.Flags().GetStringArray("deny-chat")
		p.AllowPremium, _ = cmd.Flags().GetBool("allow-premium")
		p.AllowBots, _ = cmd.Flags().GetBool("allow-bots")
		p.DenyBots, _ = cmd.Flags().GetBool("deny-bots")
		p.AllowCloseFriends, _ = cmd.Flags().GetBool("allow-close-friends")
		p.ReplaceHidden, _ = cmd.Flags().GetBool("replace-hidden")
		p.Yes, _ = cmd.Flags().GetBool("yes")
		p.DryRun, _ = cmd.Flags().GetBool("dry-run")
		if err := p.Validate(); err != nil {
			return fail(exitcode.Usage, "%v", err)
		}
		var r rpc.PrivacyRules
		if err := a.callOne(cmd, rpc.MethodAccountPrivacySet, p, &r); err != nil {
			return err
		}
		return a.success(cmd, r)
	}}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.PrivacyRules](), Args: []argSpec{{Name: "key", Required: true}}})
	set.Flags().String("default", "", "default: everybody, contacts or nobody")
	for _, name := range []string{"allow-user", "deny-user", "allow-chat", "deny-chat"} {
		set.Flags().StringArray(name, nil, "add "+name+" exception")
	}
	for _, name := range []string{"allow-premium", "allow-bots", "deny-bots", "allow-close-friends", "replace-hidden", "yes", "dry-run"} {
		set.Flags().Bool(name, false, name)
	}
	root.AddCommand(get, set)
	return root
}
