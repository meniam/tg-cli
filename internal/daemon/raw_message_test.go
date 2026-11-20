package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

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
	messages := result.(rpc.ContextResult).Messages
	if len(messages) != 2 || !messages[0].Edited || !messages[1].Edited || messages[0].Text != "old" || messages[1].Text != "new" {
		t.Fatalf("context = %+v", messages)
	}
	page := exportPage(t, d, rpc.ExportParams{Chat: "42"})
	if len(page.Messages) != 2 || !page.Messages[0].Edited || !page.Messages[1].Edited || page.Messages[0].Text != "old" || page.Messages[1].Text != "new" {
		t.Fatalf("export = %+v", page)
	}
}

func TestMessageSenderDoesNotReceiveRaw(t *testing.T) {
	_, people, _, err := convertStoredMessages([]rpc.Message{{ID: 1, ChatID: 1, Date: time.Unix(1, 0).UTC().Format(time.RFC3339), Sender: &rpc.Sender{ID: 7, Name: "person"}, Raw: json.RawMessage(`{"_":"message","from_id":{"_":"peerUser","user_id":7}}`)}}, false)
	if err != nil || len(people) != 1 || len(people[0].Raw) != 0 {
		t.Fatalf("people=%+v err=%v", people, err)
	}
}

func TestPersonRawFromContactsProfileAndMembers(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	ctx := context.Background()
	contact := rpc.Contact{ID: 7, FirstName: "user", Raw: json.RawMessage(`{"contacts.getContacts":{"_":"user","id":7}}`)}
	if err := d.saveContactPeople(ctx, []rpc.Contact{contact}); err != nil {
		t.Fatal(err)
	}
	profile := rpc.Contact{ID: 7, FirstName: "user", Raw: json.RawMessage(`{"users.getFullUser":{"_":"users.userFull","users":[{"_":"user","id":7}]}}`)}
	if err := db.UpsertPeople(ctx, []store.Person{d.personFromContact(ctx, profile)}); err != nil {
		t.Fatal(err)
	}
	d.saveMembers(ctx, []rpc.Member{{ID: 7, Name: "user", Raw: json.RawMessage(`{"channels.getParticipants":{"_":"user","id":7}}`)}})
	person, err := db.GetPerson(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err = json.Unmarshal(person.Raw, &keys); err != nil || len(keys) != 3 {
		t.Fatalf("person raw=%s err=%v", person.Raw, err)
	}
}

func TestChatRawFromDialogsAndFullInfo(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	ctx := context.Background()
	d.saveChats(ctx, "test", []rpc.Chat{{ID: -42, Type: "channel", Title: "News", Raw: json.RawMessage(`{"messages.getDialogs":{"_":"channel","id":42}}`)}}, false)
	d.saveChatInfo(ctx, rpc.ChatInfo{ID: -42, Type: "channel", Title: "News", Raw: json.RawMessage(`{"channels.getFullChannel":{"_":"messages.chatFull","chats":[{"_":"channel","id":42}],"users":[{"_":"user","id":7}]}}`)})
	chat, err := db.GetChat(ctx, -42)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err = json.Unmarshal(chat.Raw, &keys); err != nil || len(keys["messages.getDialogs"]) == 0 || len(keys["channels.getFullChannel"]) == 0 {
		t.Fatalf("chat raw=%s err=%v", chat.Raw, err)
	}
}
