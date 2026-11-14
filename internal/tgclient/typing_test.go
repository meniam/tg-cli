package tgclient

import (
	"context"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"reflect"
	"testing"
	"tg-cli/internal/rpc"
)

func TestTypingRequest(t *testing.T) {
	cases := []struct {
		name   string
		action tg.SendMessageActionClass
	}{
		{"typing", &tg.SendMessageTypingAction{}}, {"cancel", &tg.SendMessageCancelAction{}}, {"record_voice", &tg.SendMessageRecordAudioAction{}}, {"upload_voice", &tg.SendMessageUploadAudioAction{}}, {"record_video", &tg.SendMessageRecordVideoAction{}}, {"upload_video", &tg.SendMessageUploadVideoAction{}}, {"upload_photo", &tg.SendMessageUploadPhotoAction{}}, {"upload_document", &tg.SendMessageUploadDocumentAction{}}, {"choose_sticker", &tg.SendMessageChooseStickerAction{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				req, ok := input.(*tg.MessagesSetTypingRequest)
				if !ok || reflect.TypeOf(req.Action) != reflect.TypeOf(tc.action) {
					t.Fatalf("request: %#v", input)
				}
				topic, ok := req.GetTopMsgID()
				if !ok || topic != 12 {
					t.Fatalf("topic: %d %v", topic, ok)
				}
				return respond(output, &tg.BoolTrue{})
			}))
			result, err := setTyping(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.TypingParams{Action: tc.name, Topic: 12})
			if err != nil || calls != 1 || result.Action != tc.name {
				t.Fatalf("result: %+v %v", result, err)
			}
		})
	}
}
