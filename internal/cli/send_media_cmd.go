package cli

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

var mediaTypes = []string{"photo", "video", "audio", "voice", "document"}

func (a *app) messageSendMediaCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use: "send-media <chat>", Short: "Send files as photo, video, audio, voice or document",
		Long: "Send one file or an album of 2 to 10 files of the same type. Use --as to choose the type; " +
			"repeat --file for an album. The owner must allow the chat in write and the file directory " +
			"in access.files. Put -- before a negative chat id. JSONL prints one sent message per line; " +
			"use JSON, TOON or human to see the full dry-run result.",
		Args: cobra.ExactArgs(1), RunE: a.messageSendMedia,
	}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.SendMediaResult](),
		Args: []argSpec{{Name: "chat", Required: true}}, Choices: []flagChoices{{Flag: "as", Values: mediaTypes}}, ItemPath: "messages"})
	cmd.Flags().String("idempotency-key", "", "idempotency key: 1 to 64 characters A-Za-z0-9._:-")
	cmd.Flags().String("as", "", "required media type: photo, video, audio, voice or document")
	cmd.Flags().StringArray("file", nil, "file path; repeat for an album (1 to 10 files)")
	cmd.Flags().String("caption", "", "caption for the first file (up to 1024 Unicode code points)")
	cmd.Flags().Int("reply-to", 0, "reply to this message id")
	cmd.Flags().Int("topic", 0, "send in this forum topic")
	cmd.Flags().Bool("silent", false, "send without a notification")
	cmd.Flags().Bool("dry-run", false, "check files and describe the action without uploading")
	cmd.Flags().Int("duration", 0, "media duration in seconds; overrides detected duration")
	cmd.Flags().Int("width", 0, "video width; overrides detected width")
	cmd.Flags().Int("height", 0, "video height; overrides detected height")
	cmd.Flags().String("self-destruct", "", "self-destruct after 1 to 60 seconds or view-once")
	return cmd
}

func (a *app) messageSendMedia(cmd *cobra.Command, args []string) error {
	as, _ := cmd.Flags().GetString("as")
	if !cmd.Flags().Changed("as") || !mediaTypeAllowed(as) {
		return fail(exitcode.Usage, "--as is required: use photo, video, audio, voice or document")
	}
	paths, _ := cmd.Flags().GetStringArray("file")
	selfDestruct, _ := cmd.Flags().GetString("self-destruct")
	if cmd.Flags().Changed("self-destruct") {
		if _, err := rpc.ParseSelfDestruct(selfDestruct); err != nil {
			return fail(exitcode.Usage, "%s", err.Error())
		}
		if (as != "photo" && as != "video") || len(paths) != 1 {
			return fail(exitcode.Usage, "self-destruct requires one photo or video")
		}
	}
	if len(paths) == 0 || len(paths) > 10 || (as == "voice" && len(paths) != 1) {
		return fail(exitcode.Usage, "provide 1 to 10 --file paths; voice requires exactly one")
	}
	files := make([]string, len(paths))
	for i, path := range paths {
		if path == "" {
			return fail(exitcode.Usage, "--file path must not be empty")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fail(exitcode.Usage, "invalid --file path")
		}
		files[i] = absolute
	}
	caption, _ := cmd.Flags().GetString("caption")
	if !utf8.ValidString(caption) {
		return fail(exitcode.Usage, "caption must be at most 1024 characters")
	}
	if utf8.RuneCountInString(caption) > 1024 {
		return translate(rpc.CaptionTooLong())
	}
	reply, _ := cmd.Flags().GetInt("reply-to")
	topic, _ := cmd.Flags().GetInt("topic")
	duration, _ := cmd.Flags().GetInt("duration")
	width, _ := cmd.Flags().GetInt("width")
	height, _ := cmd.Flags().GetInt("height")
	if reply < 0 || topic < 0 || duration < 0 || width < 0 || height < 0 {
		return fail(exitcode.Usage, "reply, topic and media metadata must not be negative")
	}
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	silent, _ := cmd.Flags().GetBool("silent")
	dry, _ := cmd.Flags().GetBool("dry-run")
	p := rpc.SendMediaParams{Chat: args[0], As: as, Files: files, Caption: caption,
		ReplyTo: reply, Topic: topic, Silent: silent, DryRun: dry,
		Duration: duration, Width: width, Height: height, SelfDestruct: selfDestruct}
	keyed, err := withIdempotencyKey(cmd, p)
	if err != nil {
		return err
	}
	var result rpc.SendMediaResult
	if err := a.callOne(cmd, rpc.MethodMessageSendMedia, keyed, &result); err != nil {
		if ce, ok := err.(*cmdError); ok && ce.exit == exitcode.WriteDisabled {
			if strings.Contains(ce.msg, "access.files") {
				a.warn(cmd, "ask the owner to add the file directory to access.files")
			} else if strings.Contains(ce.msg, "file roots to files") || strings.Contains(ce.msg, "files in access.json") {
				a.warn(cmd, "ask the owner to add the file directory to access.files")
			} else {
				a.warn(cmd, "ask the owner to add the chat to access.write")
			}
		}
		return err
	}
	if err := emitList(a, cmd, settings, result, result.Messages, ""); err != nil {
		return err
	}
	if result.DryRun {
		a.warn(cmd, "dry run: no files were sent")
	} else {
		ids := make([]string, len(result.Messages))
		for i, item := range result.Messages {
			ids[i] = strconv.Itoa(item.ID)
		}
		a.warn(cmd, "sent %d message(s): %s", len(result.Messages), strings.Join(ids, ", "))
	}
	return nil
}

func mediaTypeAllowed(as string) bool {
	for _, value := range mediaTypes {
		if value == as {
			return true
		}
	}
	return false
}
