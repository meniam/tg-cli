package tgclient

import (
	"bytes"
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"io"
	"testing"
	"tg-cli/internal/rpc"
)

func TestDownloadLocation(t *testing.T) {
	photo := &tg.Photo{ID: 1, Sizes: []tg.PhotoSizeClass{&tg.PhotoStrippedSize{Type: "i"}, &tg.PhotoSize{Type: "s", Size: 12}, &tg.PhotoSizeProgressive{Type: "x", Sizes: []int{20, 40}}}}
	f, loc, err := downloadLocation(&tg.Message{ID: 7, Media: &tg.MessageMediaPhoto{Photo: photo}})
	if err != nil || f.Size != 40 || f.MIME != "image/jpeg" || loc.(*tg.InputPhotoFileLocation).ThumbSize != "x" {
		t.Fatalf("photo: %+v %v %v", f, loc, err)
	}
	doc := &tg.Document{ID: 2, Size: 11, MimeType: "application/pdf", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "foreign.pdf"}}}
	f, _, err = downloadLocation(&tg.Message{ID: 8, Media: &tg.MessageMediaDocument{Document: doc}})
	if err != nil || f.Size != 11 || f.FileName != "foreign.pdf" {
		t.Fatalf("document: %+v %v", f, err)
	}
	if _, _, err = downloadLocation(&tg.Message{ID: 9, Media: &tg.MessageMediaWebPage{}}); err == nil {
		t.Fatal("web page accepted")
	}
}

func TestDownloadProtection(t *testing.T) {
	for _, tc := range []struct {
		protected        bool
		messageProtected bool
		missing          bool
		want             bool
	}{{false, false, false, false}, {true, false, false, true}, {false, true, false, true}, {false, false, true, true}} {
		chats := map[int64]*tg.Chat{}
		if !tc.missing {
			chats[12] = &tg.Chat{ID: 12, Noforwards: tc.protected}
		}
		m := &tg.Message{ID: 1, PeerID: &tg.PeerChat{ChatID: 12}, Noforwards: tc.messageProtected}
		elem := messages.Elem{Entities: peer.NewEntities(nil, chats, nil)}
		match, protected := downloadProtection(nil, -12, elem, m)
		if !match || protected != tc.want {
			t.Fatalf("%+v: match=%v protected=%v", tc, match, protected)
		}
		if match, _ := downloadProtection(nil, -13, elem, m); match {
			t.Fatal("accepted another chat")
		}
	}
}

func TestDownloadFloodIsNotRetried(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_30")
	}))
	_, err := downloader.NewDownloader().Download(downloadClient{api}, &tg.InputDocumentFileLocation{ID: 1}).Stream(context.Background(), io.Discard)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeFloodWait || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestDownloadResolvedRefresh(t *testing.T) {
	var requests, gets int
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.MessagesGetMessagesRequest:
			requests++
			ref := []byte("old")
			if requests > 1 {
				ref = []byte("new")
			}
			m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 9, AccessHash: 2, FileReference: ref, Size: 4, MimeType: "text/plain"}}}
			return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
		case *tg.UploadGetFileRequest:
			if req.CDNSupported {
				t.Fatal("CDN unexpectedly enabled")
			}
			gets++
			loc, ok := req.Location.(*tg.InputDocumentFileLocation)
			if !ok {
				t.Fatalf("location %T", req.Location)
			}
			if gets == 1 {
				if string(loc.FileReference) != "old" {
					t.Fatalf("initial ref %q", loc.FileReference)
				}
				return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			if string(loc.FileReference) != "new" {
				t.Fatalf("refreshed ref %q", loc.FileReference)
			}
			return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte("data")})
		default:
			t.Fatalf("unexpected request %T", in)
			return nil
		}
	}))
	var result bytes.Buffer
	id, failures, err := downloadResolved(context.Background(), api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(&result) })
	if err != nil || id != 42 || len(failures) != 0 || requests != 2 || gets != 2 || result.String() != "data" {
		t.Fatalf("id=%d errors=%+v err=%v requests=%d gets=%d data=%q", id, failures, err, requests, gets, result.String())
	}
}

func TestDownloadResolvedProtectedNoFileRequest(t *testing.T) {
	for _, protectedChat := range []bool{false, true} {
		gets := 0
		api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			switch in.(type) {
			case *tg.MessagesGetMessagesRequest:
				m := &tg.Message{ID: 7, PeerID: &tg.PeerChat{ChatID: 12}, Date: 1, Noforwards: !protectedChat, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 9, Size: 4}}}
				return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}, Chats: []tg.ChatClass{&tg.Chat{ID: 12, Title: "test", Photo: &tg.ChatPhotoEmpty{}, Noforwards: protectedChat}}})
			case *tg.UploadGetFileRequest:
				gets++
				return nil
			default:
				t.Fatalf("unexpected request %T", in)
				return nil
			}
		}))
		_, failures, err := downloadResolved(context.Background(), api, nil, &tg.InputPeerChat{ChatID: 12}, -12, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(io.Discard) })
		if err != nil || len(failures) != 1 || failures[0].Code != "telegram_error" || gets != 0 {
			t.Fatalf("failures=%+v err=%v gets=%d", failures, err, gets)
		}
	}
}

func TestDownloadResolvedMediaAndVisitErrors(t *testing.T) {
	photo := &tg.Photo{ID: 5, AccessHash: 6, FileReference: []byte("ref"), Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "s", Size: 1}, &tg.PhotoSizeProgressive{Type: "x", Sizes: []int{3, 4}}}}
	cases := []struct {
		name     string
		media    tg.MessageMediaClass
		allowed  bool
		wantType string
	}{
		{"photo", &tg.MessageMediaPhoto{Photo: photo}, true, "photo"},
		{"document", &tg.MessageMediaDocument{Document: &tg.Document{ID: 8, Size: 4, MimeType: "text/plain"}}, true, "document"},
		{"none", nil, false, ""}, {"paid", &tg.MessageMediaPaidMedia{StarsAmount: 1}, false, ""},
		{"visit error", &tg.MessageMediaDocument{Document: &tg.Document{ID: 9, Size: 4}}, false, "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gets := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch in.(type) {
				case *tg.MessagesGetMessagesRequest:
					return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: tc.media}}})
				case *tg.UploadGetFileRequest:
					gets++
					return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte("data")})
				default:
					t.Fatalf("unexpected request %T", in)
					return nil
				}
			}))
			_, failures, err := downloadResolved(context.Background(), api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(f rpc.DownloadedFile, fetch func(io.Writer) error) error {
				if tc.name == "visit error" {
					return rpc.NewError(rpc.CodeInvalidParams, "rejected by visitor")
				}
				if f.MediaType != tc.wantType {
					t.Fatalf("type=%s", f.MediaType)
				}
				return fetch(io.Discard)
			})
			if err != nil || (len(failures) == 0) != tc.allowed || (gets > 0) != tc.allowed {
				t.Fatalf("failures=%+v err=%v get=%d", failures, err, gets)
			}
		})
	}
}
