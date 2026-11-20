package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMergeRawKeys(t *testing.T) {
	for _, tc := range []struct{ old, incoming, want string }{
		{`{"a.method":{"old":1},"b.method":{"keep":2}}`, `{"a.method":{"new":3}}`, `{"a.method":{"new":3},"b.method":{"keep":2}}`},
		{`{"deleted":true}`, `{"a.method":{"id":1}}`, `{"a.method":{"id":1}}`},
		{`[]`, `{"a.method":{}}`, `{"a.method":{}}`},
		{`{"a.method":{"id":1}}`, ``, `{"a.method":{"id":1}}`},
	} {
		got := mergeRawKeys([]byte(tc.old), []byte(tc.incoming))
		var a, b any
		if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(tc.want), &b) != nil {
			t.Fatalf("invalid JSON: %s %s", got, tc.want)
		}
		ga, _ := json.Marshal(a)
		gb, _ := json.Marshal(b)
		if string(ga) != string(gb) {
			t.Errorf("got %s want %s", ga, gb)
		}
	}
}

func TestRawKeysPersistAcrossUpdates(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	initial := json.RawMessage(`{"messages.getDialogs":{"_":"channel","title":"old"}}`)
	full := json.RawMessage(`{"channels.getFullChannel":{"_":"messages.chatFull","chats":[{"_":"channel"}],"users":[]}}`)
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "channel", Title: "first", Raw: initial}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFull(ctx, 1, ChatFull{Raw: full}); err != nil {
		t.Fatal(err)
	}
	chat, err := db.GetChat(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err = json.Unmarshal(chat.Raw, &m); err != nil || len(m) != 2 {
		t.Fatalf("chat raw=%s err=%v", chat.Raw, err)
	}
	beforeTitle := append(json.RawMessage(nil), chat.Raw...)
	if err = db.UpsertChats(ctx, []Chat{{ID: 1, Title: "second", Raw: json.RawMessage(`{"messages.getDialogs":{"_":"channel","title":"new"}}`)}}); err != nil {
		t.Fatal(err)
	}
	version, err := db.GetChatVersion(ctx, 1, 1)
	if err != nil || string(version.Raw) != string(beforeTitle) {
		t.Fatalf("version raw=%s want=%s err=%v", version.Raw, beforeTitle, err)
	}
	chat, err = db.GetChat(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(chat.Raw, &m); err != nil || len(m) != 2 || string(m["messages.getDialogs"]) != `{"_":"channel","title":"new"}` {
		t.Fatalf("updated=%s err=%v", chat.Raw, err)
	}
}

func TestTranscriptionRawKeys(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	t1 := Transcription{ChatID: 1, MessageID: 2, TranscriptionID: 3, Pending: true, Raw: json.RawMessage(`{"messages.transcribeAudio":{"_":"messages.transcribedAudio","text":"pending"}}`)}
	if err := db.PutTranscription(ctx, t1); err != nil {
		t.Fatal(err)
	}
	t1.Raw = json.RawMessage(`{"updateTranscribedAudio":{"_":"updateTranscribedAudio","text":"part"}}`)
	if ok, err := db.UpdateTranscription(ctx, t1); err != nil || !ok {
		t.Fatalf("update=%v err=%v", ok, err)
	}
	var before string
	if err := db.read.QueryRow(`SELECT raw FROM transcriptions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	foreign := t1
	foreign.TranscriptionID = 4
	foreign.Raw = json.RawMessage(`{"updateTranscribedAudio":{"_":"updateTranscribedAudio","text":"wrong"}}`)
	if ok, err := db.UpdateTranscription(ctx, foreign); err != nil || ok {
		t.Fatalf("foreign update=%v err=%v", ok, err)
	}
	var after string
	if err := db.read.QueryRow(`SELECT raw FROM transcriptions`).Scan(&after); err != nil || before != after {
		t.Fatalf("foreign changed raw: %s => %s, err=%v", before, after, err)
	}
	t1.Raw = json.RawMessage(`{"updateTranscribedAudio":{"_":"updateTranscribedAudio","text":"final"}}`)
	t1.Pending = false
	if ok, err := db.UpdateTranscription(ctx, t1); err != nil || !ok {
		t.Fatalf("update=%v err=%v", ok, err)
	}
	var raw string
	if err := db.read.QueryRow(`SELECT raw FROM transcriptions`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) != 2 || string(m["updateTranscribedAudio"]) != `{"_":"updateTranscribedAudio","text":"final"}` {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
	t1.TranscriptionID = 4
	t1.Raw = json.RawMessage(`{"updateTranscribedAudio":{"_":"updateTranscribedAudio","text":"wrong"}}`)
	if ok, err := db.UpdateTranscription(ctx, t1); err != nil || ok {
		t.Fatalf("wrong id=%v err=%v", ok, err)
	}
}

func TestPersonRawMergesSources(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, raw := range []string{
		`{"contacts.getContacts":{"_":"user","id":7}}`,
		`{"users.getFullUser":{"_":"users.userFull","users":[{"_":"user","id":7}],"chats":[]}}`,
		`{"channels.getParticipants":{"_":"user","id":7}}`,
	} {
		if err := db.UpsertPeople(ctx, []Person{{ID: 7, Raw: json.RawMessage(raw)}}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := db.GetPerson(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err = json.Unmarshal(p.Raw, &m); err != nil || len(m) != 3 || len(m["contacts.getContacts"]) == 0 || len(m["users.getFullUser"]) == 0 || len(m["channels.getParticipants"]) == 0 {
		t.Fatalf("person=%s err=%v", p.Raw, err)
	}
}
