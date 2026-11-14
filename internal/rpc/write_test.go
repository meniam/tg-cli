package rpc

import (
	"encoding/json"
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
	var got SendParams
	if unmarshalErr := json.Unmarshal(raw, &got); unmarshalErr != nil || got != input {
		t.Fatalf("send params: %+v %v", got, unmarshalErr)
	}
}
