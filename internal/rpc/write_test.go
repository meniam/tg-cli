package rpc

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestWriteContractsAndDisabledError(t *testing.T) {
	if MethodMessageSend != "message.send" || MethodMessageEdit != "message.edit" || MethodMessageDelete != "message.delete" || MethodMessageForward != "message.forward" ||
		MethodMessageReact != "message.react" || MethodMessagePin != "message.pin" || MethodMessageUnpin != "message.unpin" || MethodMessagePoll != "message.poll" {
		t.Fatal("write method names changed")
	}
	err := ErrWriteDisabled()
	if err.Code != CodeWriteDisabled || !strings.Contains(err.Message, "write") || !strings.Contains(err.Message, "access.json") {
		t.Fatalf("disabled error: %+v", err)
	}
	input := SendParams{Chat: "42", Text: "agent text", ReplyTo: 7, Topic: 8, Silent: true, DryRun: true}
	raw, marshalErr := json.Marshal(input)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if string(raw) != `{"chat":"42","text":"agent text","reply_to":7,"topic":8,"silent":true,"dry_run":true}` {
		t.Fatalf("old send JSON changed: %s", raw)
	}
	var got SendParams
	if unmarshalErr := json.Unmarshal(raw, &got); unmarshalErr != nil || !reflect.DeepEqual(got, input) {
		t.Fatalf("send params: %+v %v", got, unmarshalErr)
	}
	editRaw, marshalErr := json.Marshal(EditParams{Chat: "42", ID: 7, Text: "edited", DryRun: true})
	if marshalErr != nil || string(editRaw) != `{"chat":"42","id":7,"text":"edited","dry_run":true}` {
		t.Fatalf("old edit JSON changed: %s %v", editRaw, marshalErr)
	}
	resultRaw, marshalErr := json.Marshal(SendResult{ChatID: 42, ID: 7, PlainText: "secret", Rich: true})
	if marshalErr != nil || string(resultRaw) != `{"chat_id":42,"id":7}` {
		t.Fatalf("internal fields leaked: %s %v", resultRaw, marshalErr)
	}
}

func TestSendMediaWireTypesDoNotSerializeOpenFiles(t *testing.T) {
	input := SendMediaParams{Chat: "42", As: "photo", Files: []string{"/tmp/photo.jpg"}, Caption: "caption", ReplyTo: 7, Topic: 8, Silent: true, Duration: 2, Width: 4, Height: 5}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var got SendMediaParams
	if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got, input) {
		t.Fatalf("media params: %+v %v", got, err)
	}
	f, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	data, err := json.Marshal(MediaFile{Name: "secret.bin", Size: 7, File: f, MIME: "text/plain"})
	if err != nil || string(data) != "{}" {
		t.Fatalf("open file serialized: %s %v", data, err)
	}
	result, err := json.Marshal(SendMediaResult{ChatID: 42, Messages: []MessageRef{{ID: 3, Date: "2026-11-14T10:00:00Z"}}})
	if err != nil || !strings.Contains(string(result), `"messages":[{"id":3,"date":"2026-11-14T10:00:00Z"}]`) {
		t.Fatalf("media result: %s %v", result, err)
	}
}
