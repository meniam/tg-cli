package rpc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWriteContractsAndDisabledError(t *testing.T) {
	if MethodMessageSend != "message.send" || MethodMessageEdit != "message.edit" || MethodMessageDelete != "message.delete" || MethodMessageForward != "message.forward" {
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
