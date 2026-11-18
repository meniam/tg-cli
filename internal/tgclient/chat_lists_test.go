package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func testPeers(api *tg.Client) *peers.Manager {
	return (peers.Options{Storage: &peers.InmemoryStorage{}}).Build(api)
}

func rpcErrorCode(t *testing.T, err error) int {
	t.Helper()
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	return rpcErr.Code
}

func TestMembersChannelFiltersAndPage(t *testing.T) {
	for _, tc := range []struct {
		filter, search, wantFilter, wantQ string
	}{
		{"", "ann", "channelParticipantsSearch", "ann"},
		{"recent", "", "channelParticipantsRecent", ""},
		{"admins", "", "channelParticipantsAdmins", ""},
		{"bots", "", "channelParticipantsBots", ""},
		{"admins", "ann", "channelParticipantsAdmins", ""},
	} {
		t.Run(tc.filter+"/"+tc.search, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				req, ok := input.(*tg.ChannelsGetParticipantsRequest)
				if !ok {
					t.Errorf("request %T", input)
					return errors.New("wrong request")
				}
				if req.Filter.TypeName() != tc.wantFilter || req.Offset != 3 || req.Limit != 2 {
					t.Errorf("request %+v", req)
				}
				if f, ok := req.Filter.(*tg.ChannelParticipantsSearch); ok && f.Q != tc.wantQ {
					t.Errorf("query %q", f.Q)
				}
				return respond(output, &tg.ChannelsChannelParticipants{Count: 8,
					Participants: []tg.ChannelParticipantClass{&tg.ChannelParticipantAdmin{UserID: 1, Date: 1700000000}, &tg.ChannelParticipant{UserID: 2, Date: 1700000001}},
					Users:        []tg.UserClass{user(1, "Ann", "Lee", "ann", false), user(2, "Bot", "", "robot", true)},
				})
			}))
			ch := testPeers(api).Channel(channel(9, "Group", "group", false, true))
			got, err := membersAPI(context.Background(), api, ch, rpc.MembersParams{Filter: tc.filter, Search: tc.search, Offset: 3}, 2)
			if err != nil || got.NextOffset != 5 {
				t.Fatalf("got %+v err %v", got, err)
			}
			wantLen := 2
			if tc.filter == "admins" && tc.search != "" {
				wantLen = 1
			}
			if len(got.Members) != wantLen {
				t.Fatalf("members %+v", got.Members)
			}
			for _, member := range got.Members {
				var keys map[string]json.RawMessage
				if err := json.Unmarshal(member.Raw, &keys); err != nil || len(keys[rawChannelMembers]) == 0 {
					t.Fatalf("member raw=%s err=%v", member.Raw, err)
				}
			}
			if got.Members[0].Name != "Ann Lee" || got.Members[0].Role != "admin" {
				t.Errorf("member %+v", got.Members[0])
			}
		})
	}
}

func TestMembersRolesAndGroup(t *testing.T) {
	users := map[int64]*tg.User{}
	for i := int64(1); i <= 6; i++ {
		users[i] = user(i, "Name", "", "", i == 2)
	}
	channelMembers := []tg.ChannelParticipantClass{
		&tg.ChannelParticipant{UserID: 1, Date: 1700000000}, &tg.ChannelParticipantSelf{UserID: 2, Date: 1700000000},
		&tg.ChannelParticipantCreator{UserID: 3}, &tg.ChannelParticipantAdmin{UserID: 4, Date: 1700000000},
		&tg.ChannelParticipantBanned{Peer: &tg.PeerUser{UserID: 5}, BannedRights: tg.ChatBannedRights{SendMessages: true}},
		&tg.ChannelParticipantBanned{Peer: &tg.PeerUser{UserID: 6}, BannedRights: tg.ChatBannedRights{ViewMessages: true}},
		&tg.ChannelParticipantLeft{Peer: &tg.PeerUser{UserID: 7}},
	}
	roles := []string{"member", "member", "creator", "admin", "restricted", "banned", "left"}
	for i, raw := range channelMembers {
		m, ok := memberOfChannel(raw, users)
		if !ok || m.Role != roles[i] {
			t.Errorf("%d: %+v ok=%v", i, m, ok)
		}
		if i == 0 && m.Joined != "2023-11-14T22:13:20Z" {
			t.Errorf("joined %s", m.Joined)
		}
		if i == 2 && m.Joined != "" {
			t.Errorf("creator joined %s", m.Joined)
		}
	}
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		if _, ok := input.(*tg.MessagesGetFullChatRequest); !ok {
			t.Errorf("request %T", input)
		}
		return respond(output, &tg.MessagesChatFull{FullChat: &tg.ChatFull{Participants: &tg.ChatParticipants{Participants: []tg.ChatParticipantClass{
			&tg.ChatParticipant{UserID: 3, Date: 1700000000}, &tg.ChatParticipantCreator{UserID: 1}, &tg.ChatParticipantAdmin{UserID: 2, Date: 1700000000},
		}}}, Users: []tg.UserClass{user(1, "Alice", "", "alice", false), user(2, "Bob", "", "bob", false), user(3, "Robot", "", "robot", true)}})
	}))
	group := testPeers(api).Chat(&tg.Chat{ID: 4, Title: "Group"})
	got, err := membersAPI(context.Background(), api, group, rpc.MembersParams{Filter: "admins", Search: "bo"}, 1)
	if err != nil || len(got.Members) != 1 || got.Members[0].ID != 2 || got.NextOffset != 0 {
		t.Fatalf("group %+v %v", got, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(got.Members[0].Raw, &keys); err != nil || len(keys[rawFullChat]) == 0 {
		t.Fatalf("group member raw=%s err=%v", got.Members[0].Raw, err)
	}
	got, err = membersAPI(context.Background(), api, group, rpc.MembersParams{Offset: 1}, 1)
	if err != nil || len(got.Members) != 1 || got.Members[0].ID != 2 || got.NextOffset != 2 {
		t.Fatalf("page %+v %v", got, err)
	}
}

func TestMembersForbiddenAndErrors(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.MessagesGetFullChatRequest:
			return respond(output, &tg.MessagesChatFull{FullChat: &tg.ChatFull{Participants: &tg.ChatParticipantsForbidden{ChatID: 4}}})
		case *tg.ChannelsGetParticipantsRequest:
			return tgerr.New(400, "CHAT_ADMIN_REQUIRED")
		}
		return errors.New("wrong request")
	}))
	mgr := testPeers(api)
	if _, err := membersAPI(context.Background(), api, mgr.Chat(&tg.Chat{ID: 4}), rpc.MembersParams{}, 1); rpcErrorCode(t, err) != rpc.CodeTelegram {
		t.Fatal(err)
	}
	if _, err := membersAPI(context.Background(), api, mgr.Channel(channel(9, "News", "news", true, false)), rpc.MembersParams{}, 1); rpcErrorCode(t, err) != rpc.CodeTelegram {
		t.Fatal(err)
	}
	if _, err := membersAPI(context.Background(), api, mgr.User(user(1, "Ann", "", "", false)), rpc.MembersParams{}, 1); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := membersAPI(context.Background(), api, mgr.User(user(2, "Bot", "", "", true)), rpc.MembersParams{}, 1); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if rpcErrorCode(t, mapError(tgerr.New(400, "USER_ID_INVALID"))) != rpc.CodeNotFound {
		t.Fatal("wrong user error")
	}
}

func TestTopicsCursorAndForum(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetForumTopicsRequest)
		if !ok {
			t.Errorf("request %T", input)
			return errors.New("wrong request")
		}
		if calls == 1 && (req.OffsetTopic != 10 || req.OffsetID != 100 || req.OffsetDate != 1700000000) {
			t.Errorf("cursor %+v", req)
		}
		calls++
		if calls == 1 {
			return respond(output, &tg.MessagesForumTopics{Count: 2, Topics: []tg.ForumTopicClass{
				&tg.ForumTopic{ID: 10, Date: 1699990000, Peer: &tg.PeerChannel{ChannelID: 9}, Title: "Topic", TopMessage: 100, UnreadCount: 2, FromID: &tg.PeerUser{UserID: 1}, NotifySettings: tg.PeerNotifySettings{}},
				&tg.ForumTopic{ID: 9, Date: 1699980000, Peer: &tg.PeerChannel{ChannelID: 9}, Title: "Other", TopMessage: 90, FromID: &tg.PeerUser{UserID: 1}, NotifySettings: tg.PeerNotifySettings{}},
			}, Messages: []tg.MessageClass{&tg.Message{ID: 100, Date: 1700000000, PeerID: &tg.PeerChannel{ChannelID: 9}}, &tg.Message{ID: 90, Date: 1699999000, PeerID: &tg.PeerChannel{ChannelID: 9}}}})
		}
		return respond(output, &tg.MessagesForumTopics{Count: 2, Topics: []tg.ForumTopicClass{
			&tg.ForumTopic{ID: 9, Date: 1699980000, Peer: &tg.PeerChannel{ChannelID: 9}, Title: "Other", TopMessage: 90, FromID: &tg.PeerUser{UserID: 1}, NotifySettings: tg.PeerNotifySettings{}},
		}, Messages: []tg.MessageClass{&tg.Message{ID: 90, Date: 1699999000, PeerID: &tg.PeerChannel{ChannelID: 9}}}})
	}))
	mgr := testPeers(api)
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := mgr.Channel(forum)
	first, err := topicsAPI(context.Background(), api, peer, "", 1)
	if err != nil || len(first.Topics) != 1 || first.NextCursor == "" || first.Topics[0].Unread != 2 {
		t.Fatalf("topics %+v %v", first, err)
	}
	_, err = topicsAPI(context.Background(), api, peer, first.NextCursor, 1)
	if err != nil || calls != 2 {
		t.Fatalf("second %v calls %d", err, calls)
	}
	if _, err := topicsAPI(context.Background(), api, mgr.Channel(channel(8, "Plain", "", false, true)), "", 1); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := decodeTopicsCursor(first.NextCursor, -channelIDBase-8); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := decodeTopicsCursor(encodeOpaqueCursor(topicsCursor{V: 2, Chat: -channelIDBase - 9, Date: 1, ID: 1, Topic: 1}), -channelIDBase-9); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
}

func TestCommonChatsRawMaxIDAndCursor(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetCommonChatsRequest)
		if !ok {
			t.Errorf("request %T", input)
			return errors.New("wrong request")
		}
		if calls == 1 && req.MaxID != 7 {
			t.Errorf("raw max id %d", req.MaxID)
		}
		calls++
		ch := channel(6, "News", "news", true, false)
		ch.Photo = &tg.ChatPhotoEmpty{}
		return respond(output, &tg.MessagesChatsSlice{Count: 3, Chats: []tg.ChatClass{&tg.Chat{ID: 7, Title: "Family", Photo: &tg.ChatPhotoEmpty{}}, ch}})
	}))
	mgr := testPeers(api)
	userPeer := mgr.User(user(1, "Ann", "", "", false))
	first, err := commonChatsAPI(context.Background(), api, userPeer, "", 1)
	if err != nil || len(first.Chats) != 1 || first.Chats[0].ID != -7 || first.NextCursor == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	second, err := commonChatsAPI(context.Background(), api, userPeer, first.NextCursor, 1)
	if err != nil || len(second.Chats) != 1 || calls != 2 {
		t.Fatalf("second %+v %v", second, err)
	}
	if _, err := commonChatsAPI(context.Background(), api, mgr.Chat(&tg.Chat{ID: 7}), "", 1); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := decodeCommonChatsCursor(first.NextCursor, 2); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := decodeCommonChatsCursor(encodeOpaqueCursor(commonChatsCursor{V: 2, User: 1, MaxID: 7}), 1); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
}

func TestChatListLimits(t *testing.T) {
	for _, maximum := range []int{100, 200} {
		for _, bad := range []int{-1, maximum + 1} {
			if _, err := listLimit(bad, maximum); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
				t.Fatal(err)
			}
		}
		if got, err := listLimit(0, maximum); err != nil || got != 50 {
			t.Fatalf("default %d %v", got, err)
		}
	}
}

func TestClientRejectsInvalidListParamsBeforeTelegram(t *testing.T) {
	var c Client
	for _, p := range []rpc.MembersParams{{Limit: -1}, {Limit: 201}, {Offset: -1}, {Filter: "unknown"}} {
		if _, err := c.Members(context.Background(), chatref.Ref{}, p); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
	for _, p := range []rpc.TopicsParams{{Limit: -1}, {Limit: 101}} {
		if _, err := c.Topics(context.Background(), chatref.Ref{}, p); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
	for _, p := range []rpc.CommonChatsParams{{Limit: -1}, {Limit: 101}} {
		if _, err := c.CommonChats(context.Background(), chatref.Ref{}, p); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
}

func TestListsEmptyAndCreationDateCursor(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.ChannelsGetParticipantsRequest:
			return respond(output, &tg.ChannelsChannelParticipants{})
		case *tg.MessagesGetForumTopicsRequest:
			return respond(output, &tg.MessagesForumTopics{OrderByCreateDate: true, Count: 2, Topics: []tg.ForumTopicClass{
				&tg.ForumTopic{ID: 12, Date: 1700000000, Peer: &tg.PeerChannel{ChannelID: 9}, Title: "Created", TopMessage: 120, FromID: &tg.PeerUser{UserID: 1}, NotifySettings: tg.PeerNotifySettings{}},
				&tg.ForumTopic{ID: 11, Date: 1699990000, Peer: &tg.PeerChannel{ChannelID: 9}, Title: "Older", TopMessage: 110, FromID: &tg.PeerUser{UserID: 1}, NotifySettings: tg.PeerNotifySettings{}},
			}})
		case *tg.MessagesGetCommonChatsRequest:
			return respond(output, &tg.MessagesChats{})
		}
		return errors.New("wrong request")
	}))
	mgr := testPeers(api)
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	peer := mgr.Channel(forum)
	members, err := membersAPI(context.Background(), api, peer, rpc.MembersParams{}, 1)
	if err != nil || len(members.Members) != 0 || members.NextOffset != 0 {
		t.Fatalf("empty members %+v %v", members, err)
	}
	topics, err := topicsAPI(context.Background(), api, peer, "", 1)
	if err != nil || topics.NextCursor == "" {
		t.Fatalf("creation cursor %+v %v", topics, err)
	}
	cursor, err := decodeTopicsCursor(topics.NextCursor, -channelIDBase-9)
	if err != nil || cursor.Date != 1700000000 || cursor.ID != 120 || cursor.Topic != 12 {
		t.Fatalf("creation cursor %+v %v", cursor, err)
	}
	common, err := commonChatsAPI(context.Background(), api, mgr.User(user(1, "Ann", "", "", false)), "", 1)
	if err != nil || len(common.Chats) != 0 || common.NextCursor != "" {
		t.Fatalf("empty common %+v %v", common, err)
	}
}

func TestMembersChannelSuccessAndValidation(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		if _, ok := input.(*tg.ChannelsGetParticipantsRequest); !ok {
			return errors.New("wrong request")
		}
		return respond(output, &tg.ChannelsChannelParticipants{Count: 1, Participants: []tg.ChannelParticipantClass{&tg.ChannelParticipant{UserID: 1}}, Users: []tg.UserClass{user(1, "Ann", "", "ann", false)}})
	}))
	mgr := testPeers(api)
	got, err := membersAPI(context.Background(), api, mgr.Channel(channel(9, "News", "news", true, false)), rpc.MembersParams{}, 1)
	if err != nil || len(got.Members) != 1 || got.Members[0].ID != 1 || got.NextOffset != 0 {
		t.Fatalf("channel %+v %v", got, err)
	}
	if _, err := memberFilter("invalid", ""); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
}

func TestCommonChatsInvalidUserAndEmptyTopics(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.MessagesGetCommonChatsRequest:
			return tgerr.New(400, "USER_ID_INVALID")
		case *tg.MessagesGetForumTopicsRequest:
			return respond(output, &tg.MessagesForumTopics{})
		}
		return errors.New("wrong request")
	}))
	mgr := testPeers(api)
	if _, err := commonChatsAPI(context.Background(), api, mgr.User(user(1, "Ann", "", "", false)), "", 1); rpcErrorCode(t, err) != rpc.CodeNotFound {
		t.Fatal(err)
	}
	forum := channel(9, "Forum", "forum", false, true)
	forum.Forum = true
	result, err := topicsAPI(context.Background(), api, mgr.Channel(forum), "", 1)
	if err != nil || len(result.Topics) != 0 || result.NextCursor != "" {
		t.Fatalf("empty topics %+v %v", result, err)
	}
}
