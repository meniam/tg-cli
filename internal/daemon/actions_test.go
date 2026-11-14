package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type actionTelegram struct {
	*fakeTelegram
	calls     []string
	callErr   error
	reactions []rpc.ReactParams
	pins      []rpc.PinParams
	unpins    []rpc.UnpinParams
	polls     []rpc.PollParams
}

func (f *actionTelegram) React(_ context.Context, _ chatref.Ref, p rpc.ReactParams) (rpc.ReactResult, error) {
	f.calls = append(f.calls, "react")
	f.reactions = append(f.reactions, p)
	return rpc.ReactResult{ID: p.ID, Emoji: p.Emoji, Removed: p.Remove}, f.callErr
}
func (f *actionTelegram) Pin(_ context.Context, _ chatref.Ref, p rpc.PinParams) (rpc.PinResult, error) {
	f.calls = append(f.calls, "pin")
	f.pins = append(f.pins, p)
	return rpc.PinResult{ID: p.ID, Pinned: true}, f.callErr
}
func (f *actionTelegram) Unpin(_ context.Context, _ chatref.Ref, p rpc.UnpinParams) (rpc.PinResult, error) {
	f.calls = append(f.calls, "unpin")
	f.unpins = append(f.unpins, p)
	return rpc.PinResult{ID: p.ID}, f.callErr
}
func (f *actionTelegram) SendPoll(_ context.Context, _ chatref.Ref, p rpc.PollParams) (rpc.SendResult, error) {
	f.calls = append(f.calls, "poll")
	f.polls = append(f.polls, p)
	return rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z"}, f.callErr
}

func actionDaemon(t *testing.T) (*daemon, *actionTelegram, *store.DB, string, *bytes.Buffer) {
	t.Helper()
	d, fake, db, path := accessTestDaemon(t)
	actor := &actionTelegram{fakeTelegram: fake}
	d.opts.Telegram = actor
	var log bytes.Buffer
	d.log = slog.New(slog.NewTextHandler(&log, nil))
	return d, actor, db, path, &log
}

func actionCalls(t *testing.T, d *daemon) []struct {
	name string
	run  func() (any, error)
} {
	t.Helper()
	ctx := context.Background()
	return []struct {
		name string
		run  func() (any, error)
	}{
		{"react", func() (any, error) {
			return d.messageReact(ctx, params(t, rpc.ReactParams{Chat: "42", ID: 7, Emoji: "👍"}))
		}},
		{"pin", func() (any, error) { return d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42", ID: 7})) }},
		{"unpin", func() (any, error) { return d.messageUnpin(ctx, params(t, rpc.UnpinParams{Chat: "42", ID: 7})) }},
		{"poll", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Question?", Options: []string{"A", "B"}}))
		}},
	}
}

func TestReactPinPollAccessAndDryRun(t *testing.T) {
	d, actor, _, path, _ := actionDaemon(t)
	for _, tc := range actionCalls(t, d) {
		_, err := tc.run()
		if code := writeCode(t, err); code != rpc.CodeWriteDisabled {
			t.Errorf("%s outside write: code %d", tc.name, code)
		}
	}
	if len(actor.calls) != 0 {
		t.Fatalf("write without permission: %v", actor.calls)
	}
	writeAccess(t, path, `{"allow":[77],"write":[42]}`)
	for _, tc := range actionCalls(t, d) {
		_, err := tc.run()
		if code := writeCode(t, err); code != rpc.CodeNotFound {
			t.Errorf("%s closed: code %d", tc.name, code)
		}
	}
	if len(actor.calls) != 0 {
		t.Fatalf("write to closed chat: %v", actor.calls)
	}
	writeAccess(t, path, `{"write":[42]}`)
	actor.user = nil
	for _, tc := range actionCalls(t, d) {
		_, err := tc.run()
		if code := writeCode(t, err); code != rpc.CodeNotAuthorized {
			t.Errorf("%s without login: code %d", tc.name, code)
		}
	}
	if len(actor.calls) != 0 {
		t.Fatalf("write without login: %v", actor.calls)
	}
	actor.user = &rpc.User{ID: 42}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() (any, error)
	}{
		{"react", func() (any, error) {
			return d.messageReact(ctx, params(t, rpc.ReactParams{Chat: "42", ID: 7, Emoji: "👍", DryRun: true}))
		}},
		{"pin", func() (any, error) {
			return d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42", ID: 7, DryRun: true}))
		}},
		{"unpin", func() (any, error) {
			return d.messageUnpin(ctx, params(t, rpc.UnpinParams{Chat: "42", ID: 7, DryRun: true}))
		}},
		{"poll", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Question?", Options: []string{"A", "B"}, DryRun: true}))
		}},
	} {
		result, err := tc.run()
		if err != nil {
			t.Fatalf("%s dry-run: %v", tc.name, err)
		}
		raw, _ := json.Marshal(result)
		if !bytes.Contains(raw, []byte(`"dry_run":true`)) || !bytes.Contains(raw, []byte(`"action":"`+tc.name+`"`)) || !bytes.Contains(raw, []byte(`"count":1`)) {
			t.Errorf("%s dry-run: %s", tc.name, raw)
		}
		if tc.name == "poll" && result.(rpc.SendResult).TextLength != len("Question?") {
			t.Errorf("poll dry-run text length: %+v", result)
		}
	}
	if len(actor.calls) != 0 {
		t.Fatalf("dry-run wrote: %v", actor.calls)
	}
}

func TestReactPinPollValidBoundaries(t *testing.T) {
	d, actor, _, path, _ := actionDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	options := make([]string, 10)
	for i := range options {
		options[i] = string(rune('0' + i))
	}
	for _, p := range []rpc.PollParams{
		{Chat: "42", Question: strings.Repeat("Я", 255), Options: []string{strings.Repeat("Я", 100), "B"}, DryRun: true},
		{Chat: "42", Question: "Q", Options: options, Quiz: true, Correct: 10, Explanation: strings.Repeat("Я", 198) + "\n\n", DryRun: true},
	} {
		result, err := d.messagePoll(context.Background(), params(t, p))
		if err != nil || !result.(rpc.SendResult).DryRun {
			t.Fatalf("valid boundary: %+v %v", result, err)
		}
	}
	if len(actor.calls) != 0 {
		t.Fatalf("boundary dry-run wrote: %v", actor.calls)
	}
}

func TestReactPinPollValidationBeforeAccount(t *testing.T) {
	d, actor, _, _, _ := actionDaemon(t)
	actor.user = nil
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() (any, error)
	}{
		{"empty emoji", func() (any, error) { return d.messageReact(ctx, params(t, rpc.ReactParams{Chat: "42", ID: 7})) }},
		{"emoji whitespace", func() (any, error) {
			return d.messageReact(ctx, params(t, rpc.ReactParams{Chat: "42", ID: 7, Emoji: "a b"}))
		}},
		{"pin id", func() (any, error) { return d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42"})) }},
		{"unpin id", func() (any, error) { return d.messageUnpin(ctx, params(t, rpc.UnpinParams{Chat: "42"})) }},
		{"question 256", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: strings.Repeat("Я", 256), Options: []string{"A", "B"}}))
		}},
		{"option 101", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{strings.Repeat("Я", 101), "B"}}))
		}},
		{"one option", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"A"}}))
		}},
		{"eleven options", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}}))
		}},
		{"explanation 201", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"A", "B"}, Quiz: true, Correct: 1, Explanation: strings.Repeat("Я", 201)}))
		}},
		{"three line feeds", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"A", "B"}, Quiz: true, Correct: 1, Explanation: "a\nb\nc\nd"}))
		}},
		{"quiz no correct", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"A", "B"}, Quiz: true}))
		}},
		{"quiz multiple", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"A", "B"}, Quiz: true, Correct: 1, Multiple: true}))
		}},
		{"duplicate", func() (any, error) {
			return d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "Q", Options: []string{"A", "A"}}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.run()
			if code := writeCode(t, err); code != rpc.CodeInvalidParams {
				t.Errorf("code=%d err=%v", code, err)
			}
		})
	}
	if len(actor.calls) != 0 {
		t.Fatalf("invalid request wrote: %v", actor.calls)
	}
}

func TestReactPinPollSuccessStorageAndErrors(t *testing.T) {
	d, actor, db, path, log := actionDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	ctx := context.Background()
	storedMessage(t, db, 42, 7, "saved")
	for _, remove := range []bool{false, true} {
		result, err := d.messageReact(ctx, params(t, rpc.ReactParams{Chat: "42", ID: 7, Emoji: "👍", Remove: remove}))
		if err != nil || result.(rpc.ReactResult).Removed != remove || result.(rpc.ReactResult).ChatID != 42 {
			t.Fatalf("react: %+v %v", result, err)
		}
	}
	for _, silent := range []bool{false, true} {
		result, err := d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42", ID: 7, Silent: silent}))
		if err != nil || !result.(rpc.PinResult).Pinned || result.(rpc.PinResult).ChatID != 42 {
			t.Fatalf("pin: %+v %v", result, err)
		}
		got, err := db.GetMessage(ctx, 42, 7)
		if err != nil || got.Pinned == nil || !*got.Pinned {
			t.Fatalf("stored pin: %+v %v", got, err)
		}
	}
	result, err := d.messageUnpin(ctx, params(t, rpc.UnpinParams{Chat: "42", ID: 7}))
	if err != nil || result.(rpc.PinResult).Pinned {
		t.Fatalf("unpin: %+v %v", result, err)
	}
	got, err := db.GetMessage(ctx, 42, 7)
	if err != nil || got.Pinned == nil || *got.Pinned {
		t.Fatalf("stored unpin: %+v %v", got, err)
	}
	if _, err := d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42", ID: 999})); err != nil {
		t.Fatal(err)
	}
	if _, err := d.messageUnpin(ctx, params(t, rpc.UnpinParams{Chat: "42", ID: 999})); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMessage(ctx, 42, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pin or unpin created a missing message: %v", err)
	}
	for _, p := range []rpc.PollParams{
		{Chat: "42", Question: "Question?", Options: []string{"A", "B"}, ReplyTo: 7, Topic: 6},
		{Chat: "42", Question: "Multiple?", Options: []string{"A", "B"}, Multiple: true},
		{Chat: "42", Question: "Public?", Options: []string{"A", "B"}, Public: true},
		{Chat: "42", Question: "Quiz?", Options: []string{"A", "B"}, Quiz: true, Correct: 2, Explanation: "Because"},
	} {
		result, err := d.messagePoll(ctx, params(t, p))
		if err != nil || result.(rpc.SendResult).ID != 11 || result.(rpc.SendResult).ChatID != 42 {
			t.Fatalf("poll: %+v %v", result, err)
		}
		got, err := db.GetMessage(ctx, 42, 11)
		if err != nil || got.Text != p.Question || got.MediaType == nil || *got.MediaType != "poll" || got.Out == nil || !*got.Out {
			t.Fatalf("saved poll: %+v %v", got, err)
		}
		if p.ReplyTo != 0 && (got.ReplyTo == nil || *got.ReplyTo != int64(p.ReplyTo) || got.TopicID == nil || *got.TopicID != int64(p.Topic)) {
			t.Fatalf("saved poll reply: %+v", got)
		}
	}
	if len(actor.calls) != 11 || len(actor.reactions) != 2 || len(actor.pins) != 3 || len(actor.unpins) != 2 || len(actor.polls) != 4 || !actor.pins[1].Silent || actor.pins[0].Silent {
		t.Fatalf("calls=%v reactions=%+v pins=%+v polls=%+v", actor.calls, actor.reactions, actor.pins, actor.polls)
	}
	if !actor.polls[1].Multiple || !actor.polls[2].Public || !actor.polls[3].Quiz || actor.polls[3].Correct != 2 || actor.polls[3].Explanation != "Because" {
		t.Fatalf("poll parameters not forwarded: %+v", actor.polls)
	}
	actor.callErr = &rpc.Error{Code: rpc.CodeFloodWait, Message: "FLOOD_WAIT", Data: map[string]any{"retry_after_seconds": 12}}
	_, err = d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "No save", Options: []string{"A", "B"}}))
	if code := writeCode(t, err); code != rpc.CodeFloodWait {
		t.Fatalf("flood code=%d", code)
	}
	var flood *rpc.Error
	if !errors.As(err, &flood) || flood.Data.(map[string]any)["retry_after_seconds"] != 12 {
		t.Fatalf("flood delay: %v", err)
	}
	actor.callErr = rpc.NewError(rpc.CodeInvalidParams, "reaction is not available")
	_, err = d.messageReact(ctx, params(t, rpc.ReactParams{Chat: "42", ID: 7, Emoji: "👍"}))
	if code := writeCode(t, err); code != rpc.CodeInvalidParams {
		t.Fatalf("reaction code=%d", code)
	}
	actor.callErr = rpc.NewError(rpc.CodeTelegram, "chat admin rights are required")
	_, err = d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42", ID: 7}))
	if code := writeCode(t, err); code != rpc.CodeTelegram {
		t.Fatalf("pin rights code=%d", code)
	}
	if saved, err := db.GetMessage(ctx, 42, 7); err != nil || saved.Pinned == nil || *saved.Pinned {
		t.Fatalf("failed pin changed storage: %+v %v", saved, err)
	}
	if _, err := db.GetMessage(ctx, 42, 11); err != nil {
		t.Fatalf("failed poll changed saved row: %v", err)
	}
	actor.callErr = nil
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.messagePin(ctx, params(t, rpc.PinParams{Chat: "42", ID: 7})); err != nil {
		t.Fatalf("pin should survive storage failure: %v", err)
	}
	if _, err := d.messagePoll(ctx, params(t, rpc.PollParams{Chat: "42", Question: "private question", Options: []string{"secret A", "secret B"}})); err != nil {
		t.Fatalf("poll should survive storage failure: %v", err)
	}
	if !strings.Contains(log.String(), "storage failed") {
		t.Fatalf("storage failure missing from journal: %s", log.String())
	}
	if strings.Contains(log.String(), "Question?") || strings.Contains(log.String(), "👍") || strings.Contains(log.String(), "Because") || strings.Contains(log.String(), "Multiple?") {
		t.Fatalf("journal leaked request text: %s", log.String())
	}
	if strings.Contains(log.String(), "private question") || strings.Contains(log.String(), "secret A") || strings.Contains(log.String(), "secret B") {
		t.Fatalf("journal leaked failed request text: %s", log.String())
	}
}
