package rpc

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRawIsNeverEncoded(t *testing.T) {
	values := []any{
		&Message{}, &Chat{}, &ChatInfo{}, &Contact{}, &UserProfile{}, &Member{}, &CommonChat{}, &ChatJoinResult{}, &ChatCreateResult{}, &ContactAddResult{}, &UnreadChat{},
		&SendResult{}, &ForwardedMessage{}, &Folder{}, &ChatState{}, &Session{}, &BlockedPeer{}, &NotifyDefault{},
		&Event{Data: json.RawMessage(`{"content":"preserved"}`)}, &Transcription{},
	}
	for _, value := range values {
		t.Run(reflect.TypeOf(value).Elem().Name(), func(t *testing.T) {
			v := reflect.ValueOf(value).Elem()
			field, ok := v.Type().FieldByName("Raw")
			if !ok {
				t.Fatal("Raw field is missing")
			}
			if tag := field.Tag.Get("json"); tag != "-" {
				t.Fatalf("Raw json tag = %q, want -", tag)
			}
			if field.Type != reflect.TypeOf(json.RawMessage(nil)) {
				t.Fatalf("Raw type = %v", field.Type)
			}
			before, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			v.FieldByIndex(field.Index).SetBytes([]byte(`{"marker":"RAW-MARK-1"}`))
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, before) {
				t.Fatalf("Raw changed JSON: before %s, after %s", before, got)
			}
			if bytes.Contains(got, []byte("RAW-MARK-1")) {
				t.Fatalf("Raw marker leaked: %s", got)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(got, &fields); err != nil {
				t.Fatal(err)
			}
			for key := range fields {
				if strings.EqualFold(key, "raw") {
					t.Fatalf("Raw key leaked: %s", got)
				}
			}
			if event, ok := value.(*Event); ok && (!bytes.Equal(event.Data, json.RawMessage(`{"content":"preserved"}`)) || !bytes.Contains(got, []byte(`"data":{"content":"preserved"}`))) {
				t.Fatalf("Event.Data changed: %s", got)
			}
		})
	}
}
