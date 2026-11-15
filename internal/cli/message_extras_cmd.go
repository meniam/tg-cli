package cli

import (
	"github.com/spf13/cobra"
	"path/filepath"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"unicode/utf8"
)

func (a *app) messageExtrasCommands() []*cobra.Command {
	buttons := a.annotate(&cobra.Command{Use: "buttons <chat> <id>", Short: "Show message buttons", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		chat, id, err := messageTarget(args)
		if err != nil {
			return err
		}
		var result rpc.ButtonsResult
		if err = a.callOne(cmd, rpc.MethodMessageButtons, rpc.ButtonsParams{Chat: chat, ID: id}, &result); err != nil {
			return err
		}
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Buttons, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.ButtonsResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}, ItemPath: "buttons"})
	press := a.annotate(&cobra.Command{Use: "press <chat> <id>", Short: "Press a callback button", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		if !dry && !yes {
			return fail(exitcode.Usage, "add --yes to confirm or --dry-run to preview")
		}
		chat, id, err := messageTarget(args)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("button") == cmd.Flags().Changed("text") {
			return fail(exitcode.Usage, "choose exactly one of --button or --text")
		}
		p := rpc.PressParams{Chat: chat, ID: id, DryRun: dry}
		if cmd.Flags().Changed("button") {
			n, _ := cmd.Flags().GetInt("button")
			if n < 0 {
				return fail(exitcode.Usage, "button index must not be negative")
			}
			p.Button = &n
		} else {
			text, _ := cmd.Flags().GetString("text")
			p.Text = &text
		}
		var result rpc.PressResult
		if err = a.callOne(cmd, rpc.MethodMessagePress, p, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.PressResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}})
	press.Flags().Int("button", 0, "zero-based button index")
	press.Flags().String("text", "", "exact button text")
	press.Flags().Bool("yes", false, "confirm the action")
	press.Flags().Bool("dry-run", false, "check without pressing")
	return []*cobra.Command{buttons, press, a.inlineQueryCommand(), a.sendInlineCommand(), a.editMediaCommand(), a.sendStickerCommand()}
}

func (a *app) stickerCommand() *cobra.Command {
	root := &cobra.Command{Use: "sticker", Short: "Inspect sticker sets"}
	set := a.annotate(&cobra.Command{Use: "set <name>", Short: "Inspect a sticker set", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		name, err := rpc.StickerShortName(args[0])
		if err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		var result rpc.StickerSetResult
		if err := a.callOne(cmd, rpc.MethodStickerSet, rpc.StickerSetParams{Name: name}, &result); err != nil {
			return err
		}
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Stickers, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.StickerSetResult](), Args: []argSpec{{Name: "name", Required: true}}, ItemPath: "stickers"})
	search := a.annotate(&cobra.Command{Use: "search <query>", Short: "Search sticker sets", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 1 || limit > 100 {
			return fail(exitcode.Usage, "limit must be between 1 and 100")
		}
		var result rpc.StickerSetsResult
		if err := a.callOne(cmd, rpc.MethodStickerSearch, rpc.StickerSearchParams{Query: args[0], Limit: limit}, &result); err != nil {
			return err
		}
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Sets, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.StickerSetsResult](), Args: []argSpec{{Name: "query", Required: true}}, ItemPath: "sets"})
	search.Flags().Int("limit", 20, "maximum number of sets, 1 to 100")
	installed := a.annotate(&cobra.Command{Use: "installed", Short: "List installed sticker sets", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var result rpc.StickerSetsResult
		if err := a.callOne(cmd, rpc.MethodStickerInstalled, struct{}{}, &result); err != nil {
			return err
		}
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Sets, "")
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.StickerSetsResult](), ItemPath: "sets"})
	root.AddCommand(set, search, installed)
	return root
}
func (a *app) sendStickerCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "send-sticker <chat>", Short: "Send a sticker from a set", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("set")
		name, err := rpc.StickerShortName(name)
		if err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		if cmd.Flags().Changed("index") == cmd.Flags().Changed("emoji") {
			return fail(exitcode.Usage, "choose exactly one of --index or --emoji")
		}
		p := rpc.SendStickerParams{Chat: args[0], Set: name}
		if cmd.Flags().Changed("index") {
			v, _ := cmd.Flags().GetInt("index")
			if v < 0 {
				return fail(exitcode.Usage, "index must not be negative")
			}
			p.Index = &v
		} else {
			v, _ := cmd.Flags().GetString("emoji")
			if v == "" {
				return fail(exitcode.Usage, "emoji must not be empty")
			}
			p.Emoji = &v
		}
		p.ReplyTo, _ = cmd.Flags().GetInt("reply-to")
		p.Topic, _ = cmd.Flags().GetInt("topic")
		p.Silent, _ = cmd.Flags().GetBool("silent")
		p.DryRun, _ = cmd.Flags().GetBool("dry-run")
		if p.ReplyTo < 0 || p.Topic < 0 {
			return fail(exitcode.Usage, "reply and topic must not be negative")
		}
		keyed, err := withIdempotencyKey(cmd, p)
		if err != nil {
			return err
		}
		var result rpc.SendStickerResult
		if err := a.callOne(cmd, rpc.MethodMessageSendSticker, keyed, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.SendStickerResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.Flags().String("set", "", "sticker set name")
	cmd.Flags().Int("index", 0, "zero-based sticker index")
	cmd.Flags().String("emoji", "", "sticker emoji")
	cmd.Flags().Int("reply-to", 0, "reply to this message id")
	cmd.Flags().Int("topic", 0, "send in this forum topic")
	cmd.Flags().Bool("silent", false, "send without notification")
	cmd.Flags().Bool("dry-run", false, "check without sending")
	cmd.Flags().String("idempotency-key", "", "idempotency key: 1 to 64 characters A-Za-z0-9._:-")
	return cmd
}

func (a *app) editMediaCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "edit-media <chat> <id>", Short: "Replace message media", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		if !dry && !yes {
			return fail(exitcode.Usage, "add --yes to confirm or --dry-run to preview")
		}
		chat, id, err := messageTarget(args)
		if err != nil {
			return err
		}
		as, _ := cmd.Flags().GetString("as")
		file, _ := cmd.Flags().GetString("file")
		if (as != "photo" && as != "video" && as != "audio" && as != "document") || file == "" {
			return fail(exitcode.Usage, "--as and --file are required")
		}
		file, err = filepath.Abs(file)
		if err != nil {
			return fail(exitcode.Usage, "invalid file path")
		}
		duration, _ := cmd.Flags().GetInt("duration")
		width, _ := cmd.Flags().GetInt("width")
		height, _ := cmd.Flags().GetInt("height")
		if duration < 0 || width < 0 || height < 0 {
			return fail(exitcode.Usage, "media metadata must not be negative")
		}
		p := rpc.EditMediaParams{Chat: chat, ID: id, As: as, File: file, Duration: duration, Width: width, Height: height, DryRun: dry}
		if cmd.Flags().Changed("caption") {
			caption, _ := cmd.Flags().GetString("caption")
			if !utf8.ValidString(caption) || utf8.RuneCountInString(caption) > 1024 {
				return fail(exitcode.Usage, "caption must be at most 1024 characters")
			}
			p.Caption = &caption
		}
		var result rpc.EditMediaResult
		if err := a.callOne(cmd, rpc.MethodMessageEditMedia, p, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.EditMediaResult](), Args: []argSpec{{Name: "chat", Required: true}, {Name: "id", Required: true}}, Choices: []flagChoices{{Flag: "as", Values: []string{"photo", "video", "audio", "document"}}}})
	cmd.Flags().String("as", "", "media type: photo, video, audio or document")
	cmd.Flags().String("file", "", "file path")
	cmd.Flags().String("caption", "", "replacement caption")
	cmd.Flags().Int("duration", 0, "media duration in seconds")
	cmd.Flags().Int("width", 0, "video width")
	cmd.Flags().Int("height", 0, "video height")
	cmd.Flags().Bool("yes", false, "confirm the action")
	cmd.Flags().Bool("dry-run", false, "check without uploading")
	return cmd
}

func (a *app) inlineQueryCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "inline-query <chat>", Short: "Query an inline bot", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		bot, _ := cmd.Flags().GetString("bot")
		query, _ := cmd.Flags().GetString("query")
		offset, _ := cmd.Flags().GetString("offset")
		if bot == "" || !cmd.Flags().Changed("query") {
			return fail(exitcode.Usage, "--bot and --query are required")
		}
		var result rpc.InlineQueryResult
		if err := a.callOne(cmd, rpc.MethodMessageInlineQuery, rpc.InlineQueryParams{Chat: args[0], Bot: bot, Query: query, Offset: offset}, &result); err != nil {
			return err
		}
		settings, err := a.outputSettingsFor(cmd)
		if err != nil {
			return err
		}
		return emitList(a, cmd, settings, result, result.Results, "")
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.InlineQueryResult](), Args: []argSpec{{Name: "chat", Required: true}}, ItemPath: "results"})
	cmd.Flags().String("bot", "", "inline bot reference")
	cmd.Flags().String("query", "", "query sent to the bot")
	cmd.Flags().String("offset", "", "result page offset")
	return cmd
}
func (a *app) sendInlineCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "send-inline <chat>", Short: "Send an inline result", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		query, _ := cmd.Flags().GetString("query-id")
		resultID, _ := cmd.Flags().GetString("result")
		reply, _ := cmd.Flags().GetInt("reply-to")
		topic, _ := cmd.Flags().GetInt("topic")
		silent, _ := cmd.Flags().GetBool("silent")
		dry, _ := cmd.Flags().GetBool("dry-run")
		if query == "" || resultID == "" || reply < 0 || topic < 0 {
			return fail(exitcode.Usage, "query-id and result are required; reply and topic must not be negative")
		}
		p := rpc.SendInlineParams{Chat: args[0], QueryID: query, Result: resultID, ReplyTo: reply, Topic: topic, Silent: silent, DryRun: dry}
		keyed, err := withIdempotencyKey(cmd, p)
		if err != nil {
			return err
		}
		var result rpc.SendResult
		if err = a.callOne(cmd, rpc.MethodMessageSendInline, keyed, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.SendResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.Flags().String("query-id", "", "query id from inline-query")
	cmd.Flags().String("result", "", "result id from inline-query")
	cmd.Flags().Int("reply-to", 0, "reply to this message id")
	cmd.Flags().Int("topic", 0, "send in this forum topic")
	cmd.Flags().Bool("silent", false, "send without notification")
	cmd.Flags().Bool("dry-run", false, "check without sending")
	cmd.Flags().String("idempotency-key", "", "idempotency key: 1 to 64 characters A-Za-z0-9._:-")
	return cmd
}
func (a *app) chatAutoDeleteCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{Use: "auto-delete <chat>", Short: "Set the chat auto-delete timer", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		if !dry && !yes {
			return fail(exitcode.Usage, "add --yes to confirm or --dry-run to preview")
		}
		period, _ := cmd.Flags().GetString("period")
		if _, err := rpc.ParseAutoDeletePeriod(period); err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		var result rpc.AutoDeleteResult
		if err := a.callOne(cmd, rpc.MethodChatAutoDelete, rpc.AutoDeleteParams{Chat: args[0], Period: period, DryRun: dry}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}}, dangerous, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.AutoDeleteResult](), Args: []argSpec{{Name: "chat", Required: true}}})
	cmd.Flags().String("period", "", "off, 1d, 1w, 1m or seconds")
	cmd.Flags().Bool("yes", false, "confirm the action")
	cmd.Flags().Bool("dry-run", false, "check without changing Telegram")
	return cmd
}
