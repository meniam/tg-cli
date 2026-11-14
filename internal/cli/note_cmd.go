package cli

import (
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) noteCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "note", Short: "Add, list, or delete local notes"}
	add := a.annotate(&cobra.Command{
		Use: "add [flags] <body|->", Short: "Add a note to a chat, person, or message",
		Long: "Add a local note. Use --chat for a chat, --person for a person, or --chat with\n" +
			"--message for a message. Use - to read the whole body from stdin. Tags can be\n" +
			"repeated. For a negative chat id, use note add --chat -12345 -- body\n" +
			"(or --chat=-12345). The -- ends flags before a body starting with -.",
		Args: cobra.ExactArgs(1), RunE: a.noteAdd,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.Note](), Args: []argSpec{{Name: "body", Required: true}}, Choices: authorChoices()})
	add.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return fail(exitcode.Usage, "%v (for a negative chat id use --chat=-12345)", err)
	})
	add.Flags().String("chat", "", "chat reference for a chat or message note")
	add.Flags().String("person", "", "person reference for a person note")
	add.Flags().Int64("message", 0, "message id when --chat identifies a message note")
	add.Flags().String("author", "owner", "note author: owner or ai")
	add.Flags().String("model", "", "AI model name, only with --author ai")
	add.Flags().StringArray("tag", nil, "tag to add; repeat for multiple tags")

	list := a.annotate(&cobra.Command{
		Use: "list", Short: "List local notes, newest first",
		Long: "List local notes. --chat filters chat and message notes; --person filters\n" +
			"person notes; --chat with --message filters one message. Use --before-id\n" +
			"to continue after a page. JSONL prints one note per line and no cursor line.\n" +
			"For a negative chat id use --chat=-12345 (or --chat -12345 --).",
		Args: cobra.NoArgs, RunE: a.noteList,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.NoteListResult](), Choices: authorChoices(), ItemPath: "notes"})
	list.Flags().String("chat", "", "filter by chat reference")
	list.Flags().String("person", "", "filter by person reference")
	list.Flags().Int64("message", 0, "filter one message id, requires --chat")
	list.Flags().String("author", "", "filter by owner or ai")
	list.Flags().String("tag", "", "filter by tag")
	list.Flags().Int("limit", 0, "notes per page, 1 to 500; 0 means 50")
	list.Flags().Int64("before-id", 0, "return notes with a smaller id")

	deleteCmd := a.annotate(&cobra.Command{
		Use: "delete <id> | --author owner|ai --yes", Short: "Delete one note or all notes by author",
		Args: cobra.ArbitraryArgs, RunE: a.noteDelete,
	}, dangerous, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.NoteDeleteResult](), Args: []argSpec{{Name: "id", Required: false}}, Choices: authorChoices()})
	deleteCmd.Flags().String("author", "", "delete all notes by this author: owner or ai")
	deleteCmd.Flags().Bool("yes", false, "confirm deletion of all notes by author")
	cmd.AddCommand(add, list, deleteCmd)
	return cmd
}

func noteTarget(chat, person string, messageID int64) (string, error) {
	switch {
	case messageID < 0:
		return "", fail(exitcode.Usage, "--message must be a positive id")
	case chat != "" && person == "" && messageID == 0:
		return "chat", nil
	case chat == "" && person != "" && messageID == 0:
		return "person", nil
	case chat != "" && person == "" && messageID > 0:
		return "message", nil
	default:
		return "", fail(exitcode.Usage, "choose --chat, --person, or --chat with --message")
	}
}

func (a *app) noteAdd(cmd *cobra.Command, args []string) error {
	chat, _ := cmd.Flags().GetString("chat")
	person, _ := cmd.Flags().GetString("person")
	messageID, _ := cmd.Flags().GetInt64("message")
	if cmd.Flags().Changed("message") && messageID <= 0 {
		return fail(exitcode.Usage, "--message must be a positive id")
	}
	kind, err := noteTarget(chat, person, messageID)
	if err != nil {
		return err
	}
	author, _ := cmd.Flags().GetString("author")
	if author != "owner" && author != "ai" {
		return fail(exitcode.Usage, "--author must be owner or ai")
	}
	model, _ := cmd.Flags().GetString("model")
	if model != "" && author != "ai" {
		return fail(exitcode.Usage, "--model requires --author ai")
	}
	body := args[0]
	if body == "-" {
		if a.deps.Stdin == nil {
			return fail(exitcode.Usage, "stdin is unavailable for note body")
		}
		data, err := io.ReadAll(a.deps.Stdin)
		if err != nil {
			return fail(exitcode.Failure, "could not read note body from stdin")
		}
		body = string(data)
	}
	tags, _ := cmd.Flags().GetStringArray("tag")
	p := rpc.NoteAddParams{Kind: kind, Chat: chat, Person: person, MessageID: messageID, Body: body, Author: author, Model: model, Tags: tags}
	var result rpc.Note
	if err := a.callOne(cmd, rpc.MethodNoteAdd, p, &result); err != nil {
		return err
	}
	return a.success(cmd, result)
}

func (a *app) noteList(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	chat, _ := cmd.Flags().GetString("chat")
	person, _ := cmd.Flags().GetString("person")
	messageID, _ := cmd.Flags().GetInt64("message")
	if cmd.Flags().Changed("message") && messageID <= 0 {
		return fail(exitcode.Usage, "--message must be a positive id")
	}
	if person != "" && chat != "" || messageID != 0 && (chat == "" || person != "") {
		return fail(exitcode.Usage, "--message requires --chat and cannot be combined with --person")
	}
	author, _ := cmd.Flags().GetString("author")
	if author != "" && author != "owner" && author != "ai" {
		return fail(exitcode.Usage, "--author must be owner or ai")
	}
	p := rpc.NoteListParams{Chat: chat, Person: person, MessageID: messageID, Author: author}
	p.Tag, _ = cmd.Flags().GetString("tag")
	p.Limit, _ = cmd.Flags().GetInt("limit")
	p.BeforeID, _ = cmd.Flags().GetInt64("before-id")
	var result rpc.NoteListResult
	if err := a.callOne(cmd, rpc.MethodNoteList, p, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Notes, "")
}

func (a *app) noteDelete(cmd *cobra.Command, args []string) error {
	author, _ := cmd.Flags().GetString("author")
	yes, _ := cmd.Flags().GetBool("yes")
	if author != "" {
		if len(args) != 0 {
			return fail(exitcode.Usage, "give either a note id or --author, not both")
		}
		if author != "owner" && author != "ai" {
			return fail(exitcode.Usage, "--author must be owner or ai")
		}
		if !yes {
			return fail(exitcode.Usage, "would delete all %s notes; confirm with `note delete --author %s --yes`", author, author)
		}
		var result rpc.NoteDeleteResult
		if err := a.callOne(cmd, rpc.MethodNoteDeleteByAuthor, rpc.NoteDeleteByAuthorParams{Author: author}, &result); err != nil {
			return err
		}
		return a.success(cmd, result)
	}
	if len(args) != 1 {
		return fail(exitcode.Usage, "give a note id or --author owner|ai --yes")
	}
	if yes {
		return fail(exitcode.Usage, "--yes is only for deletion by --author")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fail(exitcode.Usage, "note id must be a positive number")
	}
	var result rpc.NoteDeleteResult
	if err := a.callOne(cmd, rpc.MethodNoteDelete, rpc.NoteDeleteParams{ID: id}, &result); err != nil {
		return err
	}
	return a.success(cmd, result)
}
