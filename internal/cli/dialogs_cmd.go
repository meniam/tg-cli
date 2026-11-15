package cli

import (
	"github.com/spf13/cobra"
	"math"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"time"
)

func (a *app) dialogCommands() []*cobra.Command {
	var out []*cobra.Command
	for _, entry := range []struct {
		name, method string
		archive      bool
	}{{"archive", rpc.MethodChatArchive, true}, {"unarchive", rpc.MethodChatUnarchive, true}, {"pin", rpc.MethodChatPinDialog, false}, {"unpin", rpc.MethodChatUnpinDialog, false}} {
		entry := entry
		use := entry.name + " <chat>"
		args := cobra.ExactArgs(1)
		result := typeOf[rpc.DialogPinResult]()
		if entry.archive {
			use += "..."
			args = cobra.RangeArgs(1, 100)
			result = typeOf[rpc.DialogArchiveResult]()
		}
		cmd := a.annotate(&cobra.Command{Use: use, Short: entry.name + " dialogs", Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 100 {
				return fail(exitcode.Usage, "at most 100 chats are allowed")
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			p := rpc.DialogParams{Chats: args, DryRun: dry}
			if entry.archive {
				var r rpc.DialogArchiveResult
				return a.writeCall(cmd, entry.method, p, &r)
			}
			var r rpc.DialogPinResult
			return a.writeCall(cmd, entry.method, p, &r)
		}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: result, Args: []argSpec{{Name: "chat", Required: true, Variadic: entry.archive}}})
		cmd.Flags().Bool("dry-run", false, "preview without changing Telegram")
		out = append(out, cmd)
	}
	out = append(out, a.notifyCommands()...)
	return out
}

func (a *app) notifyCommands() []*cobra.Command {
	var out []*cobra.Command
	for _, name := range []string{"mute", "unmute", "notify"} {
		name := name
		cmd := a.annotate(&cobra.Command{Use: name + " <chat>", Short: "Change chat notifications", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			p := rpc.NotifyParams{Chat: args[0]}
			p.DryRun, _ = cmd.Flags().GetBool("dry-run")
			if name == "mute" {
				forValue, _ := cmd.Flags().GetString("for")
				until, _ := cmd.Flags().GetString("until")
				if forValue != "" && until != "" {
					return fail(exitcode.Usage, "--for and --until cannot be combined")
				}
				ts := int64(math.MaxInt32)
				if forValue != "" {
					d, err := time.ParseDuration(forValue)
					if err != nil || d <= 0 {
						return fail(exitcode.Usage, "invalid mute duration")
					}
					ts = time.Now().Add(d).Unix()
				}
				if until != "" {
					v, err := time.Parse(time.RFC3339, until)
					if err != nil {
						return fail(exitcode.Usage, "invalid mute timestamp")
					}
					ts = v.Unix()
				}
				if ts <= time.Now().Unix() || ts > math.MaxInt32 {
					return fail(exitcode.Usage, "mute time must be in the future and within Telegram range")
				}
				v := int(ts)
				p.MuteUntil = &v
			} else if name == "unmute" {
				v := 0
				p.MuteUntil = &v
			} else {
				if cmd.Flags().Changed("silent") {
					v, _ := cmd.Flags().GetBool("silent")
					p.Silent = &v
				}
				if cmd.Flags().Changed("previews") {
					v, _ := cmd.Flags().GetBool("previews")
					p.Previews = &v
				}
				if p.Silent == nil && p.Previews == nil {
					return fail(exitcode.Usage, "provide --silent or --previews")
				}
			}
			method := map[string]string{"mute": rpc.MethodChatMute, "unmute": rpc.MethodChatUnmute, "notify": rpc.MethodChatNotify}[name]
			var r rpc.NotifyResult
			return a.writeCall(cmd, method, p, &r)
		}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.NotifyResult](), Args: []argSpec{{Name: "chat", Required: true}}})
		cmd.Flags().Bool("dry-run", false, "preview without changing Telegram")
		if name == "mute" {
			cmd.Flags().String("for", "", "mute for a duration")
			cmd.Flags().String("until", "", "mute until an RFC3339 time")
		}
		if name == "notify" {
			cmd.Flags().Bool("silent", false, "send notifications silently")
			cmd.Flags().Bool("previews", false, "show message previews")
		}
		out = append(out, cmd)
	}
	return out
}
