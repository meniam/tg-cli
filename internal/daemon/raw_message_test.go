package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestStoredTelegramMessageRaw(t *testing.T) {
	raw := json.RawMessage(`{"_":"message","edit_date":1,"text":"private","sender":{"username":"private"}}`)
	stored, _, _, err := convertStoredMessages([]rpc.Message{{ChatID: 1, ID: 2, Date: "2026-01-01T00:00:00Z", Text: "column", Raw: raw}}, false)
	if err != nil || len(stored) != 1 || string(stored[0].Raw) != string(raw) {
		t.Fatalf("stored = %+v, err = %v", stored, err)
	}
	got := rpcStoredMessage(stored[0])
	if !got.Edited || got.Text != "column" || got.Sender != nil {
		t.Fatalf("message = %+v", got)
	}
	legacy := rpcStoredMessage(store.Message{ID: 2, ChatID: 1, Date: 1, Text: "column", Raw: json.RawMessage(`{"edited":true,"sender":{"username":"alice"}}`)})
	if !legacy.Edited || legacy.Sender == nil || legacy.Sender.Username != "alice" {
		t.Fatalf("legacy = %+v", legacy)
	}
}

func TestWriteRawAndVersions(t *testing.T) {
	d, tg, db, path := writeDaemon(t)
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	ctx := context.Background()
	original := json.RawMessage(`{"_":"message","id":11,"message":"before"}`)
	edited := json.RawMessage(`{"_":"message","id":11,"message":"after","edit_date":2}`)
	tg.sendResult = rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z", Raw: original}
	if _, err := d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "before"})); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetMessage(ctx, 42, 11)
	if err != nil || !bytes.Equal(saved.Raw, original) {
		t.Fatalf("sent raw = %s, err = %v", saved.Raw, err)
	}
	tg.editResult = rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z", Raw: edited}
	if _, err := d.messageEdit(ctx, params(t, rpc.EditParams{Chat: "42", ID: 11, Text: "after"})); err != nil {
		t.Fatal(err)
	}
	saved, err = db.GetMessage(ctx, 42, 11)
	if err != nil || !bytes.Equal(saved.Raw, edited) || !rpcStoredMessage(saved).Edited {
		t.Fatalf("edited raw = %s, err = %v", saved.Raw, err)
	}
	versions, err := db.MessageVersions(ctx, 42, 11)
	if err != nil || len(versions) != 1 || !bytes.Equal(versions[0].Raw, original) {
		t.Fatalf("versions = %+v, err = %v", versions, err)
	}
	tg.sendResult = rpc.SendResult{ID: 12, Date: "2026-11-14T10:00:00Z"}
	if _, err := d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "short"})); err != nil {
		t.Fatal(err)
	}
	saved, err = db.GetMessage(ctx, 42, 12)
	var old rpc.Message
	if err != nil || json.Unmarshal(saved.Raw, &old) != nil || old.Text != "short" {
		t.Fatalf("short raw = %s, err = %v", saved.Raw, err)
	}
}

func TestRawFormatsInLocalContextAndExport(t *testing.T) {
	d, db, _ := exportTestDaemon(t)
	ctx := context.Background()
	records := []store.Message{
		{ChatID: 42, ID: 1, Date: 1700000001, Text: "old", Raw: json.RawMessage(`{"edited":true}`)},
		{ChatID: 42, ID: 2, Date: 1700000002, Text: "new", Raw: json.RawMessage(`{"_":"message","edit_date":1,"message":"hidden"}`)},
	}
	if err := db.UpsertMessages(ctx, records); err != nil {
		t.Fatal(err)
	}
	result, err := d.localContext(ctx, rpc.ContextParams{Chat: "42", ID: 1, After: 1})
	if err != nil {
		t.Fatal(err)
	}
	messages := result.(rpc.MessagesResult).Messages
	if len(messages) != 2 || !messages[0].Edited || !messages[1].Edited || messages[0].Text != "old" || messages[1].Text != "new" {
		t.Fatalf("context = %+v", messages)
	}
	page := exportPage(t, d, rpc.ExportParams{Chat: "42"})
	if len(page.Messages) != 2 || !page.Messages[0].Edited || !page.Messages[1].Edited || page.Messages[0].Text != "old" || page.Messages[1].Text != "new" {
		t.Fatalf("export = %+v", page)
	}
}
