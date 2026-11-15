package tgclient

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func TestMessageReadersRequests(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.MessagesGetMessageReadParticipantsRequest:
			if req.MsgID != 11 {
				t.Fatalf("message id %d", req.MsgID)
			}
			return respond(output, &tg.ReadParticipantDateVector{Elems: []tg.ReadParticipantDate{{UserID: 7, Date: 1700000000}}})
		case *tg.MessagesGetOutboxReadDateRequest:
			if req.MsgID != 11 {
				t.Fatalf("message id %d", req.MsgID)
			}
			return respond(output, &tg.OutboxReadDate{Date: 1700000000})
		default:
			t.Fatalf("unexpected request %T", input)
			return nil
		}
	}))
	m := testPeers(api)
	for _, tc := range []struct {
		name     string
		peerKind string
		userID   int64
	}{{"group", "group", 7}, {"user", "user", 9}} {
		t.Run(tc.name, func(t *testing.T) {
			var resultID int64
			if tc.peerKind == "group" {
				r, err := messageReadersResolved(context.Background(), api, m.Chat(&tg.Chat{ID: 8, Title: "group"}), 11)
				if err != nil {
					t.Fatal(err)
				}
				resultID = r.Readers[0].UserID
			} else {
				r, err := messageReadersResolved(context.Background(), api, m.User(user(9, "A", "", "", false)), 11)
				if err != nil {
					t.Fatal(err)
				}
				resultID = r.Readers[0].UserID
			}
			if resultID != tc.userID {
				t.Fatalf("user ID %d", resultID)
			}
		})
	}
}

func TestAccountProfileRequest(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.UsersGetFullUserRequest)
		if !ok {
			t.Fatalf("unexpected request %T", input)
		}
		if _, ok := req.ID.(*tg.InputUserSelf); !ok {
			t.Fatalf("user %T", req.ID)
		}
		full := tg.UserFull{ID: 7, About: "bio"}
		birthday := tg.Birthday{Day: 29, Month: 2}
		birthday.SetYear(2000)
		full.SetBirthday(birthday)
		full.SetPersonalChannelID(44)
		u := user(7, "Ann", "Lee", "ann", false)
		u.Premium = true
		u.SetEmojiStatus(&tg.EmojiStatus{DocumentID: 123})
		return respond(output, &tg.UsersUserFull{FullUser: full, Users: []tg.UserClass{u}})
	}))
	got, err := accountProfileAPI(context.Background(), api)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || got.FirstName != "Ann" || got.Birthday == nil || got.Birthday.Year != 2000 || got.PersonalChannelID != -1000000000044 || got.EmojiStatus == nil || got.EmojiStatus.DocumentID != 123 || !got.Premium {
		t.Fatalf("profile %+v", got)
	}
}

func TestAccountEmojiStatusesRequests(t *testing.T) {
	for _, recent := range []bool{false, true} {
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			switch req := input.(type) {
			case *tg.AccountGetDefaultEmojiStatusesRequest:
				if recent || req.Hash != 0 {
					t.Fatal("unexpected default request")
				}
			case *tg.AccountGetRecentEmojiStatusesRequest:
				if !recent || req.Hash != 0 {
					t.Fatal("unexpected recent request")
				}
			default:
				t.Fatalf("unexpected request %T", input)
			}
			return respond(output, &tg.AccountEmojiStatuses{Statuses: []tg.EmojiStatusClass{&tg.EmojiStatus{DocumentID: 123}, &tg.EmojiStatusCollectible{DocumentID: 456, Title: "Gift", Slug: "gift"}}})
		}))
		got, err := accountEmojiStatusesAPI(context.Background(), api, recent)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Statuses) != 2 || got.Statuses[0].DocumentID != 123 || !got.Statuses[1].Collectible || got.Statuses[1].Title != "Gift" {
			t.Fatalf("statuses %+v", got)
		}
	}
}

func TestHandleJoinRequestRequest(t *testing.T) {
	for _, approve := range []bool{true, false} {
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			req, ok := input.(*tg.MessagesHideChatJoinRequestRequest)
			if !ok || req.Approved != approve {
				t.Fatalf("request %#v", input)
			}
			if _, ok := req.Peer.(*tg.InputPeerChannel); !ok {
				t.Fatalf("peer %T", req.Peer)
			}
			if _, ok := req.UserID.(*tg.InputUser); !ok {
				t.Fatalf("user %T", req.UserID)
			}
			return respond(output, &tg.Updates{})
		}))
		m := testPeers(api)
		if err := handleJoinRequestAPI(context.Background(), api, m.Channel(channel(9, "Group", "group", false, true)), m.User(user(7, "Ann", "", "", false)), approve); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChatRequestsPagination(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetChatInviteImportersRequest)
		if !ok || !req.Requested || req.Limit != 1 {
			t.Fatalf("request %#v", input)
		}
		calls++
		if calls == 2 {
			u, ok := req.OffsetUser.(*tg.InputUser)
			if !ok || u.UserID != 7 || u.AccessHash != 42 || req.OffsetDate != 1700000000 {
				t.Fatalf("offset %#v", req)
			}
		}
		u := user(7, "Ann", "", "ann", false)
		u.AccessHash = 42
		v := tg.ChatInviteImporter{UserID: 7, Date: 1700000000, Requested: true}
		v.SetAbout("hello")
		return respond(output, &tg.MessagesChatInviteImporters{Count: 2, Importers: []tg.ChatInviteImporter{v}, Users: []tg.UserClass{u}})
	}))
	ch := testPeers(api).Channel(channel(9, "Group", "group", false, true))
	p := rpc.JoinRequestsParams{Limit: 1, Search: "ann"}
	result, err := chatRequestsAPI(context.Background(), api, ch, p)
	if err != nil || len(result.Requests) != 1 || result.NextCursor == "" {
		t.Fatalf("result %+v err %v", result, err)
	}
	p.Cursor = result.NextCursor
	_, err = chatRequestsAPI(context.Background(), api, ch, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Search = "other"
	if _, err = chatRequestsAPI(context.Background(), api, ch, p); err == nil {
		t.Fatal("cursor accepted for different query")
	}
}
