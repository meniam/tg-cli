package rpc

import (
	"encoding/json"
	"reflect"
	"testing"

	"tg-cli/internal/schema"
)

func TestExportMessageJSONIsFlatAndSchemaKeepsUntrustedText(t *testing.T) {
	message := ExportMessage{Message: Message{
		ID: 7, ChatID: 42, Date: "2026-11-14T10:00:00Z", Text: "foreign text",
		Sender: &Sender{ID: 9, Name: "Ann", Username: "ann"},
	}, Deleted: true}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id", "chat_id", "date", "sender", "text", "deleted"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("flat export is missing %q: %s", name, raw)
		}
	}
	if _, nested := fields["Message"]; nested {
		t.Fatalf("embedded message was nested: %s", raw)
	}
	message.Deleted = false
	raw, err = json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(raw) && jsonHasField(raw, "deleted") {
		t.Fatalf("live message has deleted marker: %s", raw)
	}
	marked := map[string]bool{}
	for _, field := range schema.Fields(reflect.TypeOf(ExportPage{})) {
		if field.Untrusted {
			marked[field.Path] = true
		}
	}
	for _, path := range []string{"messages.text", "messages.sender.name", "messages.sender.username"} {
		if !marked[path] {
			t.Errorf("schema lost untrusted field %s", path)
		}
	}
}

func jsonHasField(raw []byte, name string) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && fields[name] != nil
}
