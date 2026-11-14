package tgclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
)

func mediaFixture(t *testing.T, name string, size int64) rpc.MediaFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return rpc.MediaFile{Name: name, Size: size, File: f}
}

func uploadOK(output bin.Decoder) error { return respond(output, &tg.BoolTrue{}) }

func sentUpdate(id int) *tg.UpdateShortSentMessage {
	return &tg.UpdateShortSentMessage{ID: id, Date: 1700000000}
}

func TestSendMediaTypesAndCaption(t *testing.T) {
	for _, tc := range []struct {
		as, name, mime          string
		duration, width, height int
	}{
		{"photo", "pic.jpg", "", 0, 0, 0},
		{"document", "raw.bin", "application/octet-stream", 0, 0, 0},
		{"video", "clip.mp4", "video/mp4", 4, 640, 360},
		{"audio", "song.mp3", "audio/mpeg", 5, 0, 0},
		{"voice", "voice.ogg", "audio/ogg", 6, 0, 0},
	} {
		t.Run(tc.as, func(t *testing.T) {
			file := mediaFixture(t, tc.name, 10)
			file.MIME, file.Duration, file.Width, file.Height = tc.mime, 2, 10, 10
			parts, sends := 0, 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch req := input.(type) {
				case *tg.UploadSaveFilePartRequest:
					parts++
					if len(req.Bytes) != 10 {
						t.Errorf("part bytes = %d", len(req.Bytes))
					}
					return uploadOK(output)
				case *tg.MessagesSendMediaRequest:
					sends++
					if req.Message != "caption" || !req.Silent || req.RandomID == 0 {
						t.Errorf("send envelope: %+v", req)
					}
					if tc.as == "photo" {
						if _, ok := req.Media.(*tg.InputMediaUploadedPhoto); !ok {
							t.Errorf("photo media: %T", req.Media)
						}
					} else {
						doc, ok := req.Media.(*tg.InputMediaUploadedDocument)
						if !ok {
							t.Fatalf("document media: %T", req.Media)
						}
						if doc.MimeType != tc.mime || len(doc.Attributes) < 1 {
							t.Errorf("document MIME/attributes: %+v", doc)
						}
						if name, ok := doc.Attributes[0].(*tg.DocumentAttributeFilename); !ok || name.FileName != tc.name {
							t.Errorf("filename: %+v", doc.Attributes)
						}
						switch tc.as {
						case "document":
							if !doc.ForceFile {
								t.Error("document was not forced as file")
							}
						case "video":
							attr, ok := doc.Attributes[1].(*tg.DocumentAttributeVideo)
							if !ok || !attr.SupportsStreaming || attr.Duration != 4 || attr.W != 640 || attr.H != 360 {
								t.Errorf("video attributes: %+v", doc.Attributes)
							}
						case "audio", "voice":
							attr, ok := doc.Attributes[1].(*tg.DocumentAttributeAudio)
							if !ok || attr.Voice != (tc.as == "voice") || attr.Duration != tc.duration {
								t.Errorf("audio attributes: %+v", doc.Attributes)
							}
						}
					}
					return respond(output, sentUpdate(17))
				default:
					t.Errorf("unexpected request %T", input)
					return errors.New("unexpected request")
				}
			}))
			p := rpc.SendMediaParams{As: tc.as, Caption: "caption", Silent: true, Duration: tc.duration, Width: tc.width, Height: tc.height}
			result, err := sendMedia(context.Background(), api, &tg.InputPeerUser{UserID: 42, AccessHash: 99}, 42, p, []rpc.MediaFile{file})
			if err != nil || parts != 1 || sends != 1 || result.ChatID != 42 || len(result.Messages) != 1 || result.Messages[0].ID != 17 || result.Messages[0].Date != time.Unix(1700000000, 0).UTC().Format(time.RFC3339) {
				t.Fatalf("result=%+v parts=%d sends=%d err=%v", result, parts, sends, err)
			}
		})
	}
}

func TestSendMediaValidation(t *testing.T) {
	one := mediaFixture(t, "a.jpg", 1)
	for _, tc := range []struct {
		name  string
		p     rpc.SendMediaParams
		files []rpc.MediaFile
	}{
		{"caption 1025", rpc.SendMediaParams{As: "photo", Caption: strings.Repeat("я", 1025)}, []rpc.MediaFile{one}},
		{"voice album", rpc.SendMediaParams{As: "voice"}, []rpc.MediaFile{one, one}},
		{"eleven", rpc.SendMediaParams{As: "photo"}, make([]rpc.MediaFile, 11)},
		{"missing video metadata", rpc.SendMediaParams{As: "video"}, []rpc.MediaFile{one}},
		{"dry run", rpc.SendMediaParams{As: "photo", DryRun: true}, []rpc.MediaFile{one}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateMedia(tc.p, tc.files); err == nil {
				t.Fatal("invalid media accepted")
			}
		})
	}
	if err := validateMedia(rpc.SendMediaParams{As: "photo", Caption: strings.Repeat("я", 1024)}, []rpc.MediaFile{one}); err != nil {
		t.Fatalf("1024-character caption: %v", err)
	}
	if _, err := sendAlbum(context.Background(), message.NewSender(tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { t.Fatal("unexpected network call"); return nil }))).To(&tg.InputPeerSelf{}), []mediaSpec{{}}); err == nil {
		t.Fatal("one-item album accepted")
	}
}

func TestSendMediaAlbumAndUploadFailure(t *testing.T) {
	files := []rpc.MediaFile{mediaFixture(t, "a.jpg", 1), mediaFixture(t, "b.jpg", 1), mediaFixture(t, "c.jpg", 1)}
	peer := &tg.InputPeerUser{UserID: 42, AccessHash: 99}
	for _, failSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "second upload fails"}[failSecond], func(t *testing.T) {
			parts, preparations, sends := 0, 0, 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch req := input.(type) {
				case *tg.UploadSaveFilePartRequest:
					parts++
					if failSecond && parts == 2 {
						return errors.New("upload interrupted")
					}
					return uploadOK(output)
				case *tg.MessagesUploadMediaRequest:
					preparations++
					if _, ok := req.Media.(*tg.InputMediaUploadedPhoto); !ok {
						t.Errorf("preparation: %T", req.Media)
					}
					return respond(output, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(preparations), AccessHash: 5, FileReference: []byte{}, Sizes: []tg.PhotoSizeClass{}}})
				case *tg.MessagesSendMultiMediaRequest:
					sends++
					if len(req.MultiMedia) != 3 || req.MultiMedia[0].Message != "album caption" || req.MultiMedia[1].Message != "" || req.MultiMedia[2].Message != "" {
						t.Errorf("album captions: %+v", req.MultiMedia)
					}
					return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{
						&tg.UpdateMessageID{ID: 10}, &tg.UpdateMessageID{ID: 11}, &tg.UpdateMessageID{ID: 12},
					}})
				default:
					t.Errorf("unexpected request %T", input)
					return errors.New("unexpected request")
				}
			}))
			result, err := sendMedia(context.Background(), api, peer, 42, rpc.SendMediaParams{As: "photo", Caption: "album caption"}, files)
			if failSecond {
				if err == nil || parts != 2 || preparations != 0 || sends != 0 {
					t.Fatalf("failed album: result=%+v parts=%d prep=%d sends=%d err=%v", result, parts, preparations, sends, err)
				}
			} else if err != nil || parts != 3 || preparations != 3 || sends != 1 || len(result.Messages) != 3 {
				t.Fatalf("album: result=%+v parts=%d prep=%d sends=%d err=%v", result, parts, preparations, sends, err)
			}
		})
	}
}

func TestSendMediaAlbumReplyInsideTopic(t *testing.T) {
	files := []rpc.MediaFile{mediaFixture(t, "a.jpg", 1), mediaFixture(t, "b.jpg", 1)}
	preparations, sends := 0, 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.UploadSaveFilePartRequest:
			return uploadOK(output)
		case *tg.MessagesUploadMediaRequest:
			preparations++
			if _, ok := req.Media.(*tg.InputMediaUploadedPhoto); !ok {
				t.Errorf("album preparation: %T", req.Media)
			}
			return respond(output, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(preparations), AccessHash: 5, FileReference: []byte{}, Sizes: []tg.PhotoSizeClass{}}})
		case *tg.MessagesSendMultiMediaRequest:
			sends++
			reply, ok := req.ReplyTo.(*tg.InputReplyToMessage)
			if !ok || reply.ReplyToMsgID != 502 {
				t.Errorf("album reply: %+v", req.ReplyTo)
			} else if top, _ := reply.GetTopMsgID(); top != 501 {
				t.Errorf("album top=%d", top)
			}
			if len(req.MultiMedia) != 2 || req.MultiMedia[0].Message != "first" || req.MultiMedia[1].Message != "" {
				t.Errorf("album captions: %+v", req.MultiMedia)
			}
			return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 10}, &tg.UpdateMessageID{ID: 11}}})
		default:
			return errors.New("unexpected request")
		}
	}))
	result, err := sendMedia(context.Background(), api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "photo", Caption: "first", ReplyTo: 502, Topic: 501}, files)
	if err != nil || preparations != 2 || sends != 1 || len(result.Messages) != 2 {
		t.Fatalf("topic album: result=%+v err=%v prep=%d sends=%d", result, err, preparations, sends)
	}
}

func TestSendMediaReplyTopicFloodAndCount(t *testing.T) {
	file := mediaFixture(t, "a.jpg", 1)
	for _, tc := range []struct {
		name         string
		reply, topic int
		wantTop      int
		result       error
		missing      bool
	}{
		{"topic reply", 502, 501, 501, nil, false},
		{"topic only", 0, 501, 0, nil, false},
		{"flood", 0, 0, 0, tgerr.New(420, "FLOOD_WAIT_2"), false},
		{"Telegram error", 0, 0, 0, tgerr.New(400, "MEDIA_INVALID"), false},
		{"missing id", 0, 0, 0, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sends := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch req := input.(type) {
				case *tg.UploadSaveFilePartRequest:
					return uploadOK(output)
				case *tg.MessagesSendMediaRequest:
					sends++
					if tc.reply != 0 || tc.topic != 0 {
						reply, ok := req.ReplyTo.(*tg.InputReplyToMessage)
						wantReply := tc.reply
						if wantReply == 0 {
							wantReply = tc.topic
						}
						if !ok || reply.ReplyToMsgID != wantReply {
							t.Errorf("reply: %+v", req.ReplyTo)
						}
						if ok {
							if top, _ := reply.GetTopMsgID(); top != tc.wantTop {
								t.Errorf("top=%d", top)
							}
						}
					}
					if tc.result != nil {
						return tc.result
					}
					if tc.missing {
						return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 1}, &tg.UpdateMessageID{ID: 2}}})
					}
					return respond(output, sentUpdate(17))
				default:
					return errors.New("unexpected request")
				}
			}))
			_, err := sendMedia(context.Background(), api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "photo", ReplyTo: tc.reply, Topic: tc.topic}, []rpc.MediaFile{file})
			if tc.name == "topic reply" || tc.name == "topic only" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var rpcErr *rpc.Error
				if !errors.As(err, &rpcErr) || rpcErr.Code != map[string]int{"flood": rpc.CodeFloodWait, "Telegram error": rpc.CodeTelegram, "missing id": rpc.CodeTelegram}[tc.name] {
					t.Fatalf("error=%v", err)
				}
			}
			if sends != 1 {
				t.Fatalf("send calls=%d", sends)
			}
		})
	}
}

func TestSendMediaLargeFileAndCancellation(t *testing.T) {
	file := mediaFixture(t, "big.bin", 10<<20+1)
	parts, bigParts, active, maxActive := 0, 0, 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.UploadSaveBigFilePartRequest:
			active++
			if active > maxActive {
				maxActive = active
			}
			bigParts++
			active--
			if bigParts == 2 {
				cancel()
			}
			return uploadOK(output)
		case *tg.MessagesSendMediaRequest:
			parts++
			return respond(output, sentUpdate(17))
		default:
			return errors.New("unexpected request")
		}
	}))
	_, err := sendMedia(ctx, api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "document"}, []rpc.MediaFile{file})
	if !errors.Is(err, context.Canceled) || bigParts < 2 || maxActive > 1 || parts != 0 {
		t.Fatalf("cancellation: err=%v big parts=%d max active=%d sends=%d", err, bigParts, maxActive, parts)
	}
}

func TestSendMediaLargeFileUsesSequentialBigParts(t *testing.T) {
	file := mediaFixture(t, "large.bin", 10<<20+1)
	parts, active, maxActive, sends := 0, 0, 0, 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.UploadSaveBigFilePartRequest:
			active++
			if active > maxActive {
				maxActive = active
			}
			parts++
			active--
			return uploadOK(output)
		case *tg.MessagesSendMediaRequest:
			sends++
			return respond(output, sentUpdate(18))
		default:
			return errors.New("unexpected request")
		}
	}))
	result, err := sendMedia(context.Background(), api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "document"}, []rpc.MediaFile{file})
	if err != nil || parts != 81 || maxActive != 1 || sends != 1 || len(result.Messages) != 1 {
		t.Fatalf("large file: result=%+v err=%v parts=%d max=%d sends=%d", result, err, parts, maxActive, sends)
	}
}

func TestSendMediaClosedDuringUploadDoesNotSend(t *testing.T) {
	file := mediaFixture(t, "closing.bin", 10<<20+1)
	parts, sends := 0, 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.UploadSaveBigFilePartRequest:
			parts++
			if parts == 1 {
				_ = file.File.Close()
			}
			return uploadOK(output)
		case *tg.MessagesSendMediaRequest:
			sends++
			return respond(output, sentUpdate(18))
		default:
			return errors.New("unexpected request")
		}
	}))
	_, err := sendMedia(context.Background(), api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "document"}, []rpc.MediaFile{file})
	if err == nil || parts < 1 || sends != 0 {
		t.Fatalf("closed file: err=%v parts=%d sends=%d", err, parts, sends)
	}
}
