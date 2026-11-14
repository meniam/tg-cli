package tgclient

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

type forumItem struct {
	id     int
	pinned bool
}

func forumResponse(items []forumItem, count int) *tg.MessagesForumTopics {
	response := &tg.MessagesForumTopics{Count: count}
	for _, item := range items {
		messageID := item.id + 1000
		response.Topics = append(response.Topics, &tg.ForumTopic{
			ID: item.id, Date: 1700000000 + item.id, Pinned: item.pinned,
			Peer: &tg.PeerChannel{ChannelID: 9}, Title: "Topic", TopMessage: messageID,
			FromID: &tg.PeerUser{UserID: 1}, NotifySettings: tg.PeerNotifySettings{},
		})
		response.Messages = append(response.Messages, &tg.Message{
			ID: messageID, Date: 1700000000 + item.id,
			PeerID: &tg.PeerChannel{ChannelID: 9},
		})
	}
	return response
}

func forumIDs(topics []rpc.Topic) []int {
	ids := make([]int, 0, len(topics))
	for _, topic := range topics {
		ids = append(ids, topic.ID)
	}
	return ids
}

func TestTopicsPinnedDoNotRepeatAcrossPages(t *testing.T) {
	responses := map[int]*tg.MessagesForumTopics{
		0:   forumResponse([]forumItem{{101, true}, {102, true}, {103, true}, {201, false}, {202, false}, {203, false}}, 20),
		202: forumResponse([]forumItem{{203, false}, {102, true}, {204, false}, {101, true}, {205, false}, {206, false}, {207, false}, {208, false}}, 20),
		207: forumResponse([]forumItem{{103, true}, {208, false}}, 2),
	}
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.MessagesGetForumTopicsRequest)
		calls++
		response := responses[req.OffsetTopic]
		if response == nil {
			t.Fatalf("unexpected offset_topic %d", req.OffsetTopic)
		}
		if req.OffsetTopic > 0 && (req.OffsetID != req.OffsetTopic+1000 || req.OffsetDate != 1700000000+req.OffsetTopic) {
			t.Errorf("offset tuple: date=%d id=%d topic=%d", req.OffsetDate, req.OffsetID, req.OffsetTopic)
		}
		return respond(output, response)
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	first, err := topicsAPI(context.Background(), api, peer, "", 5)
	if err != nil || !reflect.DeepEqual(forumIDs(first.Topics), []int{101, 102, 103, 201, 202}) || first.NextCursor == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	firstCursor, err := decodeTopicsCursor(first.NextCursor, int64(peer.TDLibPeerID()))
	if err != nil || !reflect.DeepEqual(firstCursor.Pinned, []int{101, 102, 103}) || firstCursor.Topic != 202 {
		t.Fatalf("first cursor: %+v %v", firstCursor, err)
	}
	second, err := topicsAPI(context.Background(), api, peer, first.NextCursor, 5)
	if err != nil || !reflect.DeepEqual(forumIDs(second.Topics), []int{203, 204, 205, 206, 207}) || second.NextCursor == "" {
		t.Fatalf("second: %+v %v", second, err)
	}
	secondCursor, err := decodeTopicsCursor(second.NextCursor, int64(peer.TDLibPeerID()))
	if err != nil || !reflect.DeepEqual(secondCursor.Pinned, []int{101, 102, 103}) || secondCursor.Topic != 207 {
		t.Fatalf("second cursor: %+v %v", secondCursor, err)
	}
	third, err := topicsAPI(context.Background(), api, peer, second.NextCursor, 5)
	if err != nil || !reflect.DeepEqual(forumIDs(third.Topics), []int{208}) || third.NextCursor != "" || calls != 3 {
		t.Fatalf("third: %+v %v; calls=%d", third, err, calls)
	}
}

func TestTopicsEmptyDuplicatePageAdvancesByRawLast(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.MessagesGetForumTopicsRequest)
		calls++
		switch calls {
		case 1:
			if req.OffsetTopic != 50 {
				t.Errorf("first offset %d", req.OffsetTopic)
			}
			return respond(output, forumResponse([]forumItem{{101, true}, {102, true}}, 10))
		case 2:
			if req.OffsetTopic != 0 || req.OffsetID != 0 || req.OffsetDate != 0 {
				t.Errorf("next offset %+v", req)
			}
			return respond(output, forumResponse([]forumItem{{201, false}}, 1))
		default:
			t.Fatalf("too many calls: %d", calls)
			return nil
		}
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	old := encodeOpaqueCursor(topicsCursor{V: cursorVersion, Chat: int64(peer.TDLibPeerID()), Date: 1700000050, ID: 1050, Topic: 50, Pinned: []int{101, 102}})
	got, err := topicsAPI(context.Background(), api, peer, old, 2)
	if err != nil || !reflect.DeepEqual(forumIDs(got.Topics), []int{201}) || got.NextCursor != "" || calls != 2 {
		t.Fatalf("got %+v err=%v calls=%d", got, err, calls)
	}
}

func TestTopicsPinnedLastResetsOffset(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		return respond(output, forumResponse([]forumItem{{201, false}, {102, true}}, 10))
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	token := encodeTopicsCursor(topicsCursor{Chat: int64(peer.TDLibPeerID()), Date: 1700000050, ID: 1050, Topic: 50, Pinned: []int{102}})
	got, err := topicsAPI(context.Background(), api, peer, token, 2)
	if err != nil || !reflect.DeepEqual(forumIDs(got.Topics), []int{201}) || got.NextCursor == "" {
		t.Fatalf("got %+v %v", got, err)
	}
	cursor, err := decodeTopicsCursor(got.NextCursor, int64(peer.TDLibPeerID()))
	if err != nil || cursor.Topic != 0 || cursor.ID != 0 || cursor.Date != 0 {
		t.Fatalf("raw cursor %+v %v", cursor, err)
	}
}

func TestTopicsDuplicatePagesStopWithoutProgress(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		return respond(output, forumResponse([]forumItem{{101, true}}, 10))
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	token := encodeTopicsCursor(topicsCursor{Chat: int64(peer.TDLibPeerID()), Date: 1700000050, ID: 1050, Topic: 50, Pinned: []int{101}})
	got, err := topicsAPI(context.Background(), api, peer, token, 1)
	if err != nil || len(got.Topics) != 0 || got.NextCursor != "" || calls != 2 {
		t.Fatalf("got %+v err=%v calls=%d", got, err, calls)
	}
}

func TestTopicsPinnedCursorCompatibilityAndBound(t *testing.T) {
	chatID := int64(-channelIDBase - 9)
	old := encodeOpaqueCursor(topicsCursor{V: cursorVersion, Chat: chatID, Date: 1, ID: 2, Topic: 3})
	decoded, err := decodeTopicsCursor(old, chatID)
	if err != nil || len(decoded.Pinned) != 0 {
		t.Fatalf("old cursor: %+v %v", decoded, err)
	}
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.MessagesGetForumTopicsRequest)
		if req.OffsetDate != 1 || req.OffsetID != 2 || req.OffsetTopic != 3 {
			t.Errorf("old cursor offsets: %+v", req)
		}
		return respond(output, &tg.MessagesForumTopics{})
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	result, err := topicsAPI(context.Background(), api, testPeers(api).Channel(forum), old, 5)
	if err != nil || len(result.Topics) != 0 || result.NextCursor != "" {
		t.Fatalf("old cursor request: %+v %v", result, err)
	}
	ids := make([]int, 33)
	for i := range ids {
		ids[i] = i + 1
	}
	encoded := encodeTopicsCursor(topicsCursor{Chat: chatID, Date: 1, ID: 2, Topic: 3, Pinned: ids})
	decoded, err = decodeTopicsCursor(encoded, chatID)
	if err != nil || len(decoded.Pinned) != maxPinnedTopics {
		t.Fatalf("bounded cursor: %+v %v", decoded, err)
	}
	if _, err := decodeTopicsCursor(encodeOpaqueCursor(topicsCursor{V: cursorVersion, Chat: chatID, Date: 1, ID: 2, Topic: 3, Pinned: ids}), chatID); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	for _, invalid := range []string{"%%%", encodeOpaqueCursor(topicsCursor{V: 2, Chat: chatID, Date: 1, ID: 2, Topic: 3}), encodeOpaqueCursor(topicsCursor{V: cursorVersion, Chat: chatID + 1, Date: 1, ID: 2, Topic: 3})} {
		if _, err := decodeTopicsCursor(invalid, chatID); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("invalid cursor %q: %v", invalid, err)
		}
	}
	for _, invalid := range []topicsCursor{
		{V: cursorVersion, Chat: chatID},
		{V: cursorVersion, Chat: chatID, Date: 0, ID: 2, Topic: 3, Pinned: []int{1}},
	} {
		if _, err := decodeTopicsCursor(encodeOpaqueCursor(invalid), chatID); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("invalid reset cursor %+v: %v", invalid, err)
		}
	}
}

func TestTopicsPinnedCursorCapsAccumulationAt32(t *testing.T) {
	items := make([]forumItem, 33)
	for i := range items {
		items[i] = forumItem{id: i + 1, pinned: true}
	}
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		return respond(output, forumResponse(items, 100))
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	result, err := topicsAPI(context.Background(), api, peer, "", 33)
	if err != nil || len(result.Topics) != 33 || result.NextCursor == "" {
		t.Fatalf("topics %+v %v", result, err)
	}
	cursor, err := decodeTopicsCursor(result.NextCursor, int64(peer.TDLibPeerID()))
	if err != nil || len(cursor.Pinned) != 32 || cursor.Pinned[0] != 1 || cursor.Pinned[31] != 32 {
		t.Fatalf("bounded pinned cursor %+v %v", cursor, err)
	}
}

func TestTopicsOverLimitPinnedKeepsUnprocessedTopic(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.MessagesGetForumTopicsRequest)
		calls++
		if req.Limit != 100 {
			t.Errorf("request limit %d", req.Limit)
		}
		if calls == 1 {
			if req.OffsetTopic != 0 {
				t.Errorf("first offset %d", req.OffsetTopic)
			}
			return respond(output, forumResponse([]forumItem{{1, true}, {2, true}, {3, true}, {4, true}, {5, true}, {6, true}}, 6))
		}
		if req.OffsetTopic != 0 || req.OffsetID != 0 || req.OffsetDate != 0 {
			t.Errorf("second offset %+v", req)
		}
		return respond(output, forumResponse([]forumItem{{6, true}}, 1))
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	first, err := topicsAPI(context.Background(), api, peer, "", 5)
	if err != nil || !reflect.DeepEqual(forumIDs(first.Topics), []int{1, 2, 3, 4, 5}) || first.NextCursor == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	cursor, err := decodeTopicsCursor(first.NextCursor, int64(peer.TDLibPeerID()))
	if err != nil || cursor.Topic != 0 || cursor.ID != 0 || cursor.Date != 0 || !reflect.DeepEqual(cursor.Pinned, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("first cursor %+v %v", cursor, err)
	}
	second, err := topicsAPI(context.Background(), api, peer, first.NextCursor, 5)
	if err != nil || !reflect.DeepEqual(forumIDs(second.Topics), []int{6}) || calls != 2 {
		t.Fatalf("second %+v %v calls=%d", second, err, calls)
	}
}

func TestTopicsOverLimitWithPinnedRepeatsStopsAtShownLimit(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.MessagesGetForumTopicsRequest)
		calls++
		if calls == 1 {
			return respond(output, forumResponse([]forumItem{{1, true}, {11, false}, {2, true}, {3, true}, {12, false}, {13, false}, {14, false}}, 10))
		}
		if req.OffsetTopic != 12 || req.OffsetID != 1012 || req.OffsetDate != 1700000012 {
			t.Errorf("second offset %+v", req)
		}
		return respond(output, forumResponse([]forumItem{{13, false}, {14, false}}, 2))
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	token := encodeTopicsCursor(topicsCursor{Chat: int64(peer.TDLibPeerID()), Date: 1700000050, ID: 1050, Topic: 50, Pinned: []int{1, 2}})
	first, err := topicsAPI(context.Background(), api, peer, token, 3)
	if err != nil || !reflect.DeepEqual(forumIDs(first.Topics), []int{11, 3, 12}) || first.NextCursor == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	cursor, err := decodeTopicsCursor(first.NextCursor, int64(peer.TDLibPeerID()))
	if err != nil || cursor.Topic != 12 || !reflect.DeepEqual(cursor.Pinned, []int{1, 2, 3}) {
		t.Fatalf("first cursor %+v %v", cursor, err)
	}
	second, err := topicsAPI(context.Background(), api, peer, first.NextCursor, 3)
	if err != nil || !reflect.DeepEqual(forumIDs(second.Topics), []int{13, 14}) || calls != 2 {
		t.Fatalf("second %+v %v calls=%d", second, err, calls)
	}
}

func TestTopicsThreePagesKeepAllPinnedAndOrdinary(t *testing.T) {
	responses := map[int]*tg.MessagesForumTopics{
		0:   forumResponse([]forumItem{{1, true}, {2, true}, {3, true}, {4, true}, {5, true}, {6, true}, {7, true}, {101, false}, {102, false}, {103, false}, {104, false}, {105, false}, {106, false}, {107, false}, {108, false}, {109, false}, {110, false}}, 17),
		105: forumResponse([]forumItem{{1, true}, {106, false}, {107, false}, {4, true}, {108, false}, {109, false}, {110, false}}, 17),
	}
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.MessagesGetForumTopicsRequest)
		response := responses[req.OffsetTopic]
		if response == nil {
			t.Errorf("unexpected offset topic %d", req.OffsetTopic)
			return respond(output, &tg.MessagesForumTopics{})
		}
		return respond(output, response)
	}))
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	var all []int
	var token string
	for page := 0; page < 3; page++ {
		result, err := topicsAPI(context.Background(), api, peer, token, 6)
		if err != nil || (page < 2 && result.NextCursor == "") || (page == 2 && result.NextCursor != "") {
			t.Fatalf("page %d %+v %v", page+1, result, err)
		}
		all = append(all, forumIDs(result.Topics)...)
		token = result.NextCursor
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("all topics %v, want %v", all, want)
	}
}

type forumLiveItem struct {
	id, topMessage int
	pinned, closed bool
}

func forumLiveAPI(t *testing.T, items []forumLiveItem) *tg.Client {
	t.Helper()
	return tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetForumTopicsRequest)
		if !ok {
			t.Errorf("request %T", input)
			return fmt.Errorf("unexpected request %T", input)
		}
		if req.Limit != 100 {
			t.Errorf("request limit %d, want 100", req.Limit)
		}
		page := append([]forumLiveItem(nil), items...)
		if req.OffsetDate != 0 {
			if req.OffsetID == 0 || req.OffsetTopic == 0 {
				t.Errorf("incomplete offset %+v", req)
			}
			sort.Slice(page, func(i, j int) bool { return page[i].topMessage > page[j].topMessage })
			older := page[:0]
			for _, item := range page {
				if 1700000000+item.topMessage < req.OffsetDate {
					older = append(older, item)
				}
			}
			page = older
		} else if req.OffsetID != 0 || req.OffsetTopic != 0 {
			t.Errorf("incomplete reset offset %+v", req)
		}
		response := &tg.MessagesForumTopics{Count: len(items)}
		for _, item := range page {
			response.Topics = append(response.Topics, &tg.ForumTopic{
				ID: item.id, Pinned: item.pinned, Closed: item.closed,
				Date: 1690000000 + item.id, Title: "Topic", TopMessage: item.topMessage,
				Peer: &tg.PeerChannel{ChannelID: 9}, FromID: &tg.PeerUser{UserID: 1},
				NotifySettings: tg.PeerNotifySettings{},
			})
			response.Messages = append(response.Messages, &tg.Message{
				ID: item.topMessage, Date: 1700000000 + item.topMessage,
				PeerID: &tg.PeerChannel{ChannelID: 9},
			})
		}
		return respond(output, response)
	}))
}

func assertForumPages(t *testing.T, items []forumLiveItem, limit int, want []int) {
	t.Helper()
	api := forumLiveAPI(t, items)
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := testPeers(api).Channel(forum)
	var got []int
	var token string
	for page := 0; page < len(want)+5; page++ {
		result, err := topicsAPI(context.Background(), api, peer, token, limit)
		if err != nil {
			t.Fatalf("page %d: %v", page+1, err)
		}
		got = append(got, forumIDs(result.Topics)...)
		if result.NextCursor == "" {
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("limit %d: got %v, want %v", limit, got, want)
			}
			return
		}
		if result.NextCursor == token {
			t.Fatalf("page %d did not advance", page+1)
		}
		token = result.NextCursor
	}
	t.Fatalf("limit %d: pagination did not finish; got %v", limit, got)
}

func TestTopicsLiveOrderingAllLimits(t *testing.T) {
	items := []forumLiveItem{
		{93722, 93727, true, true}, {20233, 98496, true, true},
		{20203, 98885, true, false}, {40082, 95143, true, true},
		{38271, 98331, true, true}, {20280, 98913, false, false},
		{20212, 98894, false, false}, {63314, 98887, false, true},
		{81748, 98839, false, false}, {37810, 98817, false, false},
		{79248, 98771, false, false}, {37134, 98270, false, false},
		{22717, 96534, false, false}, {92228, 92235, false, true},
		{85856, 85859, false, true}, {1, 82948, false, true},
	}
	want := make([]int, 0, len(items))
	for _, item := range items {
		want = append(want, item.id)
	}
	for _, limit := range []int{5, 3, 1, 16, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) { assertForumPages(t, items, limit, want) })
	}
}

func TestTopicsOnlyPinnedAndOnlyOrdinary(t *testing.T) {
	assertForumPages(t, []forumLiveItem{{1, 500, true, false}, {2, 400, true, false}, {3, 300, true, false}}, 1, []int{1, 2, 3})
	assertForumPages(t, []forumLiveItem{{11, 500, false, false}, {12, 400, false, false}, {13, 300, false, false}}, 2, []int{11, 12, 13})
	assertForumPages(t, nil, 5, nil)
}
