package tgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

func testRawMessage(chat, id int) tg.MessageClass {
	return &tg.Message{ID: id, Date: 1700000000 + id, PeerID: &tg.PeerChat{ChatID: int64(chat)}, Message: "result"}
}

func TestDiscoverySearchFilterAndCursor(t *testing.T) {
	for _, media := range []string{"", "photo", "video", "document", "voice", "audio", "link", "gif", "pinned"} {
		if _, err := searchMediaFilter(media); err != nil {
			t.Errorf("media %s: %v", media, err)
		}
	}
	if _, err := searchMediaFilter("unknown"); err == nil {
		t.Fatal("unknown media accepted")
	}
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesSearchRequest)
		if !ok {
			t.Errorf("request %T", input)
			return errors.New("wrong request")
		}
		if req.Q != "needle" || req.Peer == nil || req.Limit != 2 || req.Filter.TypeName() != "inputMessagesFilterPhotos" {
			t.Errorf("search: %+v", req)
		}
		if calls == 1 && req.OffsetID != 11 {
			t.Errorf("cursor offset %d", req.OffsetID)
		}
		calls++
		return respond(output, &tg.MessagesMessages{Messages: []tg.MessageClass{testRawMessage(42, 11), testRawMessage(42, 10)}})
	}))
	chat := rpc.Chat{ID: -42, Type: "group", Title: "Group"}
	filter, _ := searchMediaFilter("photo")
	first, err := searchMessagesAPI(context.Background(), api, &tg.InputPeerChat{ChatID: 42}, chat, "needle", filter, 1, 0)
	if err != nil || len(first.Messages) != 1 || first.NextCursor == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	offset, err := decodeCursor(first.NextCursor, -42)
	if err != nil || offset != 11 {
		t.Fatalf("cursor: %d %v", offset, err)
	}
	_, err = searchMessagesAPI(context.Background(), api, &tg.InputPeerChat{ChatID: 42}, chat, "needle", filter, 1, offset)
	if err != nil || calls != 2 {
		t.Fatalf("second: %v calls=%d", err, calls)
	}
}

func TestDiscoveryPublicSearchCursor(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesSearchGlobalRequest)
		if !ok || req.Q != "#topic" || req.Limit != 1 {
			t.Errorf("global: %+v", input)
		}
		if calls == 1 {
			if req.OffsetID != 11 {
				t.Errorf("offset id=%d", req.OffsetID)
			}
			if _, ok := req.OffsetPeer.(*tg.InputPeerChat); !ok {
				t.Errorf("offset peer=%T", req.OffsetPeer)
			}
		}
		calls++
		return respond(output, &tg.MessagesMessagesSlice{Count: 3, Messages: []tg.MessageClass{testRawMessage(42, 11), testRawMessage(42, 10)}})
	}))
	first, err := searchPublicAPI(context.Background(), api, "#topic", 1, publicCursor{})
	if err != nil || len(first.Hits) != 1 || first.Hits[0].ChatID != -42 || first.NextCursor == "" {
		t.Fatalf("global first: %+v %v", first, err)
	}
	cursor, err := decodePublicCursor(first.NextCursor, "#topic")
	if err != nil || cursor.ID != 11 || cursor.PeerID != 42 {
		t.Fatalf("global cursor: %+v %v", cursor, err)
	}
	_, err = searchPublicAPI(context.Background(), api, "#topic", 1, cursor)
	if err != nil || calls != 2 {
		t.Fatalf("global second: %v calls=%d", err, calls)
	}
	if _, err := decodePublicCursor(first.NextCursor, "different"); err == nil {
		t.Fatal("cursor from another query accepted")
	}
}

func TestDiscoveryContextCommentsAndReadRequests(t *testing.T) {
	group := &tg.InputPeerChat{ChatID: 42}
	chat := rpc.Chat{ID: -42, Type: "group"}
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetHistoryRequest)
		if !ok || req.OffsetID != 9 || req.AddOffset != -2 || req.Limit != 3 {
			t.Errorf("context request: %+v", input)
		}
		return respond(output, &tg.MessagesMessages{Messages: []tg.MessageClass{testRawMessage(42, 10), testRawMessage(42, 9), testRawMessage(42, 8)}})
	}))
	result, err := contextAPI(context.Background(), api, group, chat, rpc.ContextParams{ID: 9, Before: 1, After: 1})
	if err != nil || len(result.Messages) != 3 || result.Messages[0].ID != 8 || result.Messages[2].ID != 10 {
		t.Fatalf("context: %+v %v", result, err)
	}
	api = tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetRepliesRequest)
		if !ok || req.MsgID != 20 || req.OffsetID != 5 || req.Limit != 2 {
			t.Errorf("replies: %+v", input)
		}
		return respond(output, &tg.MessagesMessages{Messages: []tg.MessageClass{testRawMessage(42, 7), testRawMessage(42, 6)}})
	}))
	result, err = commentsAPI(context.Background(), api, group, chat, 20, 1, 5)
	if err != nil || len(result.Messages) != 1 || result.Messages[0].ChatID != -42 || result.NextCursor == "" {
		t.Fatalf("comments: %+v %v", result, err)
	}
	post := &tg.Message{ID: 20}
	if postHasDiscussion(post) {
		t.Fatal("post without replies has discussion")
	}
	post.SetReplies(tg.MessageReplies{Comments: true, Replies: 1})
	if !postHasDiscussion(post) {
		t.Fatal("commentable post rejected")
	}
	for _, tc := range []struct {
		name    string
		peer    tg.InputPeerClass
		channel bool
	}{
		{"chat", group, false}, {"channel", &tg.InputPeerChannel{ChannelID: 9, AccessHash: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				if tc.channel {
					req, ok := input.(*tg.ChannelsReadHistoryRequest)
					if !ok || req.MaxID <= 0 {
						t.Errorf("channel read: %+v", input)
					}
					return respond(output, &tg.BoolTrue{})
				}
				req, ok := input.(*tg.MessagesReadHistoryRequest)
				if !ok || req.MaxID <= 0 {
					t.Errorf("chat read: %+v", input)
				}
				return respond(output, &tg.MessagesAffectedMessages{Pts: 1, PtsCount: 1})
			}))
			if err := markReadAPI(context.Background(), api, tc.peer); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDiscoveryLinks(t *testing.T) {
	link, err := linkForChat(rpc.Chat{ID: -1000000012345, Type: "channel", Username: "newsfeed"}, 7, 0)
	if err != nil || link.Link != "https://t.me/newsfeed/7" {
		t.Fatalf("public link: %+v %v", link, err)
	}
	link, err = linkForChat(rpc.Chat{ID: -1000000012345, Type: "supergroup"}, 7, 4)
	if err != nil || link.Link != "https://t.me/c/12345/4/7" {
		t.Fatalf("private link: %+v %v", link, err)
	}
	if _, err := linkForChat(rpc.Chat{ID: 42, Type: "user"}, 7, 0); err == nil {
		t.Fatal("user link accepted")
	}
}
