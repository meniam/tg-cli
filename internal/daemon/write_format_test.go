package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestFormattedDryRunAndAccess(t *testing.T) {
	for _, mode := range []string{"html", "markdown", "rich-html", "rich-markdown", "entities"} {
		for _, edit := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: " send", true: " edit"}[edit], func(t *testing.T) {
				d, fake, _, path := writeDaemon(t)
				p := rpc.SendParams{Chat: "42", Text: "formatted", Parse: mode, DryRun: true}
				if mode == "entities" {
					p.Parse = ""
					p.Entities = []rpc.Entity{{Type: "bold", Offset: 0, Length: 5}}
				}
				fake.check = rpc.TextCheck{Mode: mode, PlainLength: 5, Entities: 1, Rich: strings.HasPrefix(mode, "rich")}
				invoke := func() (any, error) {
					if edit {
						return d.messageEdit(context.Background(), params(t, rpc.EditParams{Chat: p.Chat, ID: 11, Text: p.Text, Parse: p.Parse, Entities: p.Entities, DryRun: true}))
					}
					return d.messageSend(context.Background(), params(t, p))
				}
				_, err := invoke()
				if writeCode(t, err) != rpc.CodeWriteDisabled || fake.checkCalls != 0 || len(fake.calls) != 0 {
					t.Fatalf("closed: err=%v check=%d calls=%v", err, fake.checkCalls, fake.calls)
				}
				if edit {
					_, err = d.messageEdit(context.Background(), params(t, rpc.EditParams{Chat: p.Chat, ID: 11, Text: p.Text, Parse: p.Parse, Entities: p.Entities}))
				} else {
					p.DryRun = false
					_, err = d.messageSend(context.Background(), params(t, p))
					p.DryRun = true
				}
				if writeCode(t, err) != rpc.CodeWriteDisabled || fake.checkCalls != 0 || len(fake.calls) != 0 {
					t.Fatalf("closed write: err=%v check=%d calls=%v", err, fake.checkCalls, fake.calls)
				}
				writeAccess(t, path, `{"write":[42]}`)
				result, err := invoke()
				if err != nil || fake.checkCalls != 1 || len(fake.calls) != 0 {
					t.Fatalf("dry: result=%+v err=%v check=%d calls=%v", result, err, fake.checkCalls, fake.calls)
				}
				raw, _ := json.Marshal(result)
				for _, field := range []string{`"mode":"` + mode + `"`, `"plain_length":5`, `"entities":1`, `"rich":` + map[bool]string{true: "true", false: "false"}[fake.check.Rich]} {
					if !bytes.Contains(raw, []byte(field)) {
						t.Errorf("missing %s: %s", field, raw)
					}
				}
				fake.checkErr = rpc.NewError(rpc.CodeInvalidParams, "invalid formatting")
				_, err = invoke()
				if writeCode(t, err) != rpc.CodeInvalidParams || len(fake.calls) != 0 {
					t.Fatalf("invalid: %v calls=%v", err, fake.calls)
				}
				if edit {
					_, err = d.messageEdit(context.Background(), params(t, rpc.EditParams{Chat: p.Chat, ID: 11, Text: p.Text, Parse: p.Parse, Entities: p.Entities}))
				} else {
					p.DryRun = false
					_, err = d.messageSend(context.Background(), params(t, p))
				}
				if writeCode(t, err) != rpc.CodeInvalidParams || len(fake.calls) != 0 {
					t.Fatalf("invalid write: %v calls=%v", err, fake.calls)
				}
			})
		}
	}
}

func TestPlainDryRunJSONUnchanged(t *testing.T) {
	d, fake, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	for _, tc := range []struct {
		name string
		edit bool
		want string
	}{
		{"send", false, `{"chat_id":42,"dry_run":true,"action":"send","count":1,"text_length":5}`},
		{"edit", true, `{"chat_id":42,"dry_run":true,"action":"edit","count":1,"text_length":5}`},
	} {
		var result any
		var err error
		if tc.edit {
			result, err = d.messageEdit(context.Background(), params(t, rpc.EditParams{Chat: "42", ID: 11, Text: "plain", DryRun: true}))
		} else {
			result, err = d.messageSend(context.Background(), params(t, rpc.SendParams{Chat: "42", Text: "plain", DryRun: true}))
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		if string(raw) != tc.want {
			t.Errorf("%s: %s", tc.name, raw)
		}
	}
	if fake.checkCalls != 2 || len(fake.calls) != 0 {
		t.Fatalf("check=%d writes=%v", fake.checkCalls, fake.calls)
	}
}

func TestFormattedMessageStorage(t *testing.T) {
	for _, tc := range []struct {
		name, parse, source, plain string
		entities                   []rpc.Entity
		rich                       bool
	}{
		{"plain", "", "plain", "plain", nil, false},
		{"html", "html", "<b>hello</b>", "hello", nil, false},
		{"markdown", "markdown", "**hello**", "hello", nil, false},
		{"entities", "", "hello", "hello", []rpc.Entity{{Type: "bold", Length: 5}}, false},
		{"rich with reply", "rich-html", "<h1>hello</h1>", "hello", nil, true},
		{"rich without reply", "rich-markdown", "# hello", "", nil, true},
	} {
		for _, edit := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " edit", false: " send"}[edit], func(t *testing.T) {
				d, fake, db, path := writeDaemon(t)
				writeAccess(t, path, `{"write":[42]}`)
				if edit {
					storedMessage(t, db, 42, 11, "prior")
				}
				var journal bytes.Buffer
				d.log = slog.New(slog.NewTextHandler(&journal, nil))
				fake.check = rpc.TextCheck{Mode: tc.parse, PlainLength: len(tc.plain), Rich: tc.rich}
				fake.sendResult = rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z", PlainText: tc.plain, Rich: tc.rich}
				fake.editResult = fake.sendResult
				var err error
				if edit {
					_, err = d.messageEdit(context.Background(), params(t, rpc.EditParams{Chat: "42", ID: 11, Text: tc.source, Parse: tc.parse, Entities: tc.entities}))
				} else {
					_, err = d.messageSend(context.Background(), params(t, rpc.SendParams{Chat: "42", Text: tc.source, Parse: tc.parse, Entities: tc.entities}))
				}
				if err != nil || len(fake.calls) != 1 {
					t.Fatalf("write=%v calls=%v", err, fake.calls)
				}
				if tc.parse != "" || tc.entities != nil {
					if fake.checkCalls != 1 {
						t.Fatalf("check=%d", fake.checkCalls)
					}
				} else if fake.checkCalls != 0 {
					t.Fatalf("plain checked=%d", fake.checkCalls)
				}
				if edit {
					if fake.lastEdit.Parse != tc.parse || len(fake.lastEdit.Entities) != len(tc.entities) {
						t.Fatal("edit format lost")
					}
				} else if fake.lastSend.Parse != tc.parse || len(fake.lastSend.Entities) != len(tc.entities) {
					t.Fatal("send format lost")
				}
				got, getErr := db.GetMessage(context.Background(), 42, 11)
				if tc.rich && tc.plain == "" {
					if edit {
						if getErr != nil || got.Text != "prior" {
							t.Fatalf("rich empty edit changed row: %+v %v", got, getErr)
						}
					} else if !errors.Is(getErr, store.ErrNotFound) {
						t.Fatalf("rich empty send saved row: %+v %v", got, getErr)
					}
				} else if getErr != nil || got.Text != tc.plain {
					t.Fatalf("stored=%+v err=%v want=%q", got, getErr, tc.plain)
				}
				if strings.Contains(journal.String(), tc.source) && tc.source != "" {
					t.Fatalf("journal leaked text: %s", journal.String())
				}
			})
		}
	}
}
