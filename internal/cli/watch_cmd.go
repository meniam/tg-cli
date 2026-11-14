package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

type watchResult struct {
	Events []rpc.Event `json:"events"`
}

var errWatchComplete = errors.New("watch event limit reached")

func watchTypes() []string {
	return []string{rpc.EventMessageNew, rpc.EventMessageEdit, rpc.EventMessageDelete, rpc.EventReaction, rpc.EventRead, rpc.EventTyping, rpc.EventStatus, rpc.EventGap, rpc.EventLagged}
}

func (a *app) watchCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use: "watch", Short: "Watch live Telegram events (read only)",
		Long: "Watch events through the Daemon. The default format is jsonl, one event per line. Use --after with the last event cursor to resume.",
		Args: cobra.NoArgs, RunE: a.messageWatch,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[watchResult](), ItemPath: "events", Formats: []output.Format{output.FormatJSONL, output.FormatJSON, output.FormatHuman}, SubscriptionMethod: rpc.MethodSubscribe, Choices: []flagChoices{{Flag: "types", Values: watchTypes()}}})
	cmd.Flags().StringArray("chat", nil, "watch a chat by reference; repeat for more chats")
	cmd.Flags().Int("topic", 0, "watch one forum topic by id")
	cmd.Flags().String("types", "", "comma-separated event types")
	cmd.Flags().String("after", "", "resume after an event cursor")
	cmd.Flags().Int("max-events", 0, "stop after this many events")
	return cmd
}

func (a *app) messageWatch(cmd *cobra.Command, _ []string) error {
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	maxEvents, _ := cmd.Flags().GetInt("max-events")
	if cmd.Flags().Changed("max-events") && maxEvents <= 0 {
		return fail(exitcode.Usage, "--max-events must be positive")
	}
	if settings.format == output.FormatJSON && maxEvents == 0 {
		return fail(exitcode.Usage, "--format json requires --max-events")
	}
	topic, _ := cmd.Flags().GetInt("topic")
	if topic < 0 {
		return fail(exitcode.Usage, "--topic must not be negative")
	}
	after, _ := cmd.Flags().GetString("after")
	if _, err := rpc.CursorID(after); err != nil {
		return fail(exitcode.Usage, "invalid event cursor")
	}
	rawTypes, _ := cmd.Flags().GetString("types")
	var types []string
	if rawTypes != "" {
		allowed := make(map[string]bool)
		for _, typ := range watchTypes() {
			allowed[typ] = true
		}
		for _, part := range strings.Split(rawTypes, ",") {
			typ := strings.TrimSpace(part)
			if !allowed[typ] {
				return fail(exitcode.Usage, "unknown event type %q", typ)
			}
			types = append(types, typ)
		}
	}
	chats, _ := cmd.Flags().GetStringArray("chat")
	params := rpc.SubscribeParams{Chats: chats, Topic: topic, Types: types, After: after}
	client, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer client.Close()
	events := make([]any, 0)
	count := 0
	started := false
	frameWarned := false
	_, err = client.SubscribeWithStart(cmd.Context(), params, func(result rpc.SubscribeResult) {
		started = true
		a.warn(cmd, "watching from cursor %s", result.Cursor)
	}, func(event rpc.Event) error {
		selected, err := output.FilterData(event, settings.fields)
		if err != nil {
			return err
		}
		switch settings.format {
		case output.FormatJSONL:
			if err := output.Lines(a.deps.Stdout, []any{selected}, ""); err != nil {
				return err
			}
		case output.FormatHuman:
			if err := writeWatchHuman(a.deps.Stdout, selected, watchUntrustedPaths(event.Type), &frameWarned); err != nil {
				return err
			}
		case output.FormatJSON:
			events = append(events, selected)
		}
		count++
		if maxEvents > 0 && count >= maxEvents {
			return errWatchComplete
		}
		return nil
	})
	switch {
	case errors.Is(err, errWatchComplete), errors.Is(err, context.Canceled):
		if settings.format == output.FormatJSON {
			return output.Success(a.deps.Stdout, output.FormatJSON, struct {
				Events []any `json:"events"`
			}{Events: events})
		}
		return nil
	case err == nil:
		return nil
	}
	var lagged *rpc.LaggedError
	if errors.As(err, &lagged) {
		a.warn(cmd, "the stream lagged: resume with --after %s", lagged.Cursor)
		return fail(exitcode.Failure, "the stream lagged")
	}
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpc.CodeCursorExpired {
		a.warn(cmd, "the cursor expired: run message watch without --after")
	}
	if errors.Is(err, rpc.ErrClosed) {
		if started {
			return fail(exitcode.DaemonNotRunning, "the daemon closed the stream")
		}
		return fail(exitcode.DaemonNotRunning, "the daemon closed the connection")
	}
	return translate(err)
}

// writeWatchHuman keeps one trusted summary line per event and lets the
// existing human formatter frame every schema-marked text field.
func writeWatchHuman(w io.Writer, selected any, untrusted []string, warned *bool) error {
	var rendered bytes.Buffer
	if err := output.SelectWithUntrusted(&rendered, output.FormatHuman, output.SuccessData(selected), nil, untrusted); err != nil {
		return err
	}
	var trusted, framed []string
	for _, line := range strings.Split(strings.TrimSuffix(rendered.String(), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "note: lines starting with"):
			if !*warned {
				if _, err := fmt.Fprintln(w, line); err != nil {
					return err
				}
				*warned = true
			}
		case strings.HasPrefix(line, "| "), line == "|":
			framed = append(framed, line)
		default:
			trusted = append(trusted, strings.TrimSpace(line))
		}
	}
	if _, err := fmt.Fprintln(w, strings.Join(trusted, "  ")); err != nil {
		return err
	}
	for _, line := range framed {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func watchUntrustedPaths(typ string) []string {
	var data reflect.Type
	switch typ {
	case rpc.EventMessageNew, rpc.EventMessageEdit:
		data = typeOf[rpc.Message]()
	case rpc.EventMessageDelete:
		data = typeOf[rpc.MessageDeleteData]()
	case rpc.EventReaction:
		data = typeOf[rpc.ReactionData]()
	case rpc.EventRead:
		data = typeOf[rpc.ReadData]()
	case rpc.EventTyping:
		data = typeOf[rpc.TypingData]()
	case rpc.EventStatus:
		data = typeOf[rpc.StatusData]()
	case rpc.EventGap:
		data = typeOf[rpc.GapData]()
	}
	if data == nil {
		return nil
	}
	var paths []string
	for _, field := range schema.Fields(data) {
		if field.Untrusted {
			paths = append(paths, fmt.Sprintf("data.%s", field.Path))
		}
	}
	return paths
}
