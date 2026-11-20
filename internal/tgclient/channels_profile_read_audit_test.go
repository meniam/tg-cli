package tgclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func TestAudit08_MessageReadersContent(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.MessagesGetMessageReadParticipantsRequest:
			return respond(output, &tg.ReadParticipantDateVector{Elems: []tg.ReadParticipantDate{{UserID: 7, Date: 1700000000}, {UserID: 8, Date: 1700000060}}})
		case *tg.MessagesGetOutboxReadDateRequest:
			return respond(output, &tg.OutboxReadDate{Date: 1700000000})
		}
		t.Errorf("unexpected request %T", input)
		return respond(output, &tg.Updates{})
	}))
	m := testPeers(api)
	group, err := messageReadersResolved(context.Background(), api, m.Chat(&tg.Chat{ID: 8, Title: "group"}), 11)
	if err != nil {
		t.Fatal(err)
	}
	wantGroup := []rpc.MessageReader{{UserID: 7, Date: "2023-11-14T22:13:20Z"}, {UserID: 8, Date: "2023-11-14T22:14:20Z"}}
	if !reflect.DeepEqual(group.Readers, wantGroup) {
		t.Errorf("group readers=%+v, want %+v", group.Readers, wantGroup)
	}
	direct, err := messageReadersResolved(context.Background(), api, m.User(user(9, "A", "", "", false)), 11)
	if err != nil {
		t.Fatal(err)
	}
	wantDirect := []rpc.MessageReader{{UserID: 9, Date: "2023-11-14T22:13:20Z"}}
	if !reflect.DeepEqual(direct.Readers, wantDirect) || direct.ID != 11 {
		t.Errorf("direct readers=%+v id=%d, want %+v id 11", direct.Readers, direct.ID, wantDirect)
	}
}

func TestAudit08_AccountProfileFields(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
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
	want := rpc.AccountProfile{
		ID: 7, FirstName: "Ann", LastName: "Lee", Username: "ann", Bio: "bio", Premium: true,
		Birthday:          &rpc.Birthday{Day: 29, Month: 2, Year: 2000},
		PersonalChannelID: -1000000000044,
		EmojiStatus:       &rpc.EmojiStatus{DocumentID: 123},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("profile=%+v, want %+v", got, want)
	}
}

func TestAudit08_ChatRequestsSearchAndContent(t *testing.T) {
	var queries []string
	var calls int
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetChatInviteImportersRequest)
		if !ok {
			t.Errorf("unexpected request %T", input)
			return respond(output, &tg.Updates{})
		}
		queries = append(queries, req.Q)
		calls++
		id := int64(7)
		if calls == 2 {
			id = 8
		}
		u := user(id, "Ann", "Lee", "ann", false)
		u.AccessHash = 42
		v := tg.ChatInviteImporter{UserID: id, Date: 1700000000, Requested: true}
		v.SetAbout("hello")
		return respond(output, &tg.MessagesChatInviteImporters{Count: 2, Importers: []tg.ChatInviteImporter{v}, Users: []tg.UserClass{u}})
	}))
	ch := testPeers(api).Channel(channel(9, "Group", "group", false, true))
	p := rpc.JoinRequestsParams{Limit: 1, Search: "ann"}
	first, err := chatRequestsAPI(context.Background(), api, ch, p)
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := []rpc.JoinRequest{{AccessHash: 42, UserID: 7, Name: "Ann Lee", Username: "ann", Date: "2023-11-14T22:13:20Z", About: "hello"}}
	if !reflect.DeepEqual(first.Requests, wantFirst) {
		t.Errorf("first page=%+v, want %+v", first.Requests, wantFirst)
	}
	p.Cursor = first.NextCursor
	second, err := chatRequestsAPI(context.Background(), api, ch, p)
	if err != nil {
		t.Fatal(err)
	}
	wantSecond := []rpc.JoinRequest{{AccessHash: 42, UserID: 8, Name: "Ann Lee", Username: "ann", Date: "2023-11-14T22:13:20Z", About: "hello"}}
	if !reflect.DeepEqual(second.Requests, wantSecond) {
		t.Errorf("second page=%+v, want %+v", second.Requests, wantSecond)
	}
	if second.NextCursor == "" {
		t.Error("second full page must carry a next cursor")
	}
	if !reflect.DeepEqual(queries, []string{"ann", "ann"}) {
		t.Errorf("search sent to Telegram = %q, want [ann ann]", queries)
	}
}
