package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func TestTextLimitsBeforeTelegram(t *testing.T) {
	text := strings.Repeat("😀", 4097)
	caption := strings.Repeat("😀", 1025)
	for _, dry := range []bool{false, true} {
		cases := []struct {
			name   string
			run    func(*daemon, context.Context, json.RawMessage) (any, error)
			params any
		}{
			{"send", (*daemon).messageSend, rpc.SendParams{Chat: "42", Text: text, DryRun: dry}},
			{"edit", (*daemon).messageEdit, rpc.EditParams{Chat: "42", ID: 1, Text: text, DryRun: dry}},
			{"draft", (*daemon).draftSet, rpc.DraftParams{Chat: "42", Text: text, DryRun: dry}},
			{"media", (*daemon).messageSendMedia, rpc.SendMediaParams{Chat: "42", As: "photo", Files: []string{"/tmp/a"}, Caption: caption, DryRun: dry}},
			{"edit media", (*daemon).messageEditMedia, rpc.EditMediaParams{Chat: "42", ID: 1, As: "photo", File: "/tmp/a", Caption: &caption, DryRun: dry}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := tc.run(&daemon{}, context.Background(), params(t, tc.params))
				if err == nil {
					t.Fatal("expected limit error")
				}
				var want string
				if strings.Contains(tc.name, "media") {
					want = "1024"
				} else {
					want = "4096"
				}
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error: %v", err)
				}
			})
		}
	}
	if err := validWriteText(strings.Repeat("😀", 4096)); err != nil {
		t.Fatal(err)
	}
}

// audit06LocalCheckSentinel is what the fake local check returns. Comparing to
// it proves that the daemon called the local check, and that it did so before
// any chat lookup, without repeating the 4096 limit in the fake.
var audit06LocalCheckSentinel = rpc.NewError(rpc.CodeInvalidParams, "local text check ran")

type textLimitFake struct{ *fakeTelegram }

func (*textLimitFake) CheckTextLocal(rpc.SendParams) error { return audit06LocalCheckSentinel }

// TestFormattedLocalCheckRunsBeforeChatLookup: a formatted write asks Telegram's
// local check about the text before it resolves the chat. The daemon here has no
// store and no chat, so a lookup first would fail with another error.
func TestFormattedLocalCheckRunsBeforeChatLookup(t *testing.T) {
	d := &daemon{}
	d.opts.Telegram = &textLimitFake{}
	for _, dry := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			run   func(*daemon, context.Context, json.RawMessage) (any, error)
			value any
		}{
			{"send", (*daemon).messageSend, rpc.SendParams{Chat: "@example", Text: "x", Parse: "html", DryRun: dry}},
			{"edit", (*daemon).messageEdit, rpc.EditParams{Chat: "@example", ID: 1, Text: "x", Parse: "html", DryRun: dry}},
		} {
			_, err := tc.run(d, context.Background(), params(t, tc.value))
			if err != error(audit06LocalCheckSentinel) {
				t.Fatalf("%s dry=%v: error %v, want the local check sentinel", tc.name, dry, err)
			}
		}
	}
}

type noReadEditTelegram struct {
	*writeTelegram
	t *testing.T
}

func (f *noReadEditTelegram) Message(context.Context, chatref.Ref, int) (rpc.Message, error) {
	f.t.Fatal("edit must not read the message before sending")
	return rpc.Message{}, nil
}

func TestEditCaptionLengthDelegatesToTelegram(t *testing.T) {
	for _, n := range []int{1025, 4096} {
		d, fake, _, path := writeDaemon(t)
		writeAccess(t, path, `{"write":[42]}`)
		d.opts.Telegram = &noReadEditTelegram{writeTelegram: fake, t: t}
		expected := rpc.NewError(rpc.CodeTelegram, "MEDIA_CAPTION_TOO_LONG")
		fake.callErr = expected
		text := strings.Repeat("😀", n)
		_, err := d.messageEdit(context.Background(), params(t, rpc.EditParams{Chat: "42", ID: 5, Text: text}))
		if err != expected || len(fake.calls) != 1 || fake.calls[0] != "edit" || fake.lastEdit.Text != text {
			t.Fatalf("length %d: calls=%v error=%v", n, fake.calls, err)
		}
	}
}
