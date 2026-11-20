package tgclient

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestUserStatuses(t *testing.T) {
	for _, tc := range []struct {
		s    tg.UserStatusClass
		want string
	}{{nil, "hidden"}, {&tg.UserStatusEmpty{}, "hidden"}, {&tg.UserStatusOnline{}, "online"}, {&tg.UserStatusOffline{WasOnline: 1}, "offline"}, {&tg.UserStatusRecently{}, "recently"}, {&tg.UserStatusLastWeek{}, "last_week"}, {&tg.UserStatusLastMonth{}, "last_month"}} {
		got, last := userStatus(tc.s)
		if got != tc.want || (got == "offline" && last != "1970-01-01T00:00:01Z") {
			t.Fatalf("%T %q %q", tc.s, got, last)
		}
	}
}
func TestContactRequests(t *testing.T) {
	ctx := context.Background()
	u := user(7, "Ivan", "Petrov", "ivan7", false)
	var calls int
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		switch r := in.(type) {
		case *tg.UsersGetFullUserRequest:
			return respond(out, &tg.UsersUserFull{FullUser: tg.UserFull{ID: 7, About: "bio", Blocked: true}, Users: []tg.UserClass{u}})
		case *tg.ContactsGetContactsRequest:
			if r.Hash != 0 {
				t.Error("nonzero hash")
			}
			return respond(out, &tg.ContactsContacts{Contacts: []tg.Contact{{UserID: 7, Mutual: true}}, Users: []tg.UserClass{u}})
		case *tg.ContactsSearchRequest:
			if r.Q != "ivan" || r.Limit != 10 {
				t.Errorf("search %+v", r)
			}
			return respond(out, &tg.ContactsFound{MyResults: []tg.PeerClass{&tg.PeerChat{ChatID: 3}, &tg.PeerUser{UserID: 7}}, Results: []tg.PeerClass{&tg.PeerUser{UserID: 8}}, Users: []tg.UserClass{u, user(8, "Other", "", "other", true)}})
		case *tg.ContactsAddContactRequest:
			if r.AddPhonePrivacyException || r.FirstName != "Ivan" || r.LastName != "Petrov" || r.Phone != "123" {
				t.Errorf("add %+v", r)
			}
			return respond(out, &tg.Updates{Users: []tg.UserClass{u}})
		case *tg.ContactsDeleteContactsRequest:
			if len(r.ID) != 1 {
				t.Errorf("delete %+v", r)
			}
			return respond(out, &tg.Updates{})
		case *tg.ContactsBlockRequest:
			if r.MyStoriesFrom {
				t.Error("story block")
			}
			return respond(out, &tg.BoolTrue{})
		case *tg.ContactsUnblockRequest:
			if r.MyStoriesFrom {
				t.Error("story unblock")
			}
			return respond(out, &tg.BoolTrue{})
		default:
			t.Fatalf("unexpected request %T", in)
			return nil
		}
	}))
	mgr := testPeers(api)
	peer := mgr.User(u)
	profile, err := userProfileAPI(ctx, api, mgr, peer)
	if err != nil || profile.ID != 7 || profile.Bio != "bio" || !profile.Blocked {
		t.Fatalf("profile %+v %v", profile, err)
	}
	var profileRaw map[string]json.RawMessage
	if err := json.Unmarshal(profile.Raw, &profileRaw); err != nil || len(profileRaw[rawFullUser]) == 0 {
		t.Fatalf("profile raw=%s err=%v", profile.Raw, err)
	}
	contacts, err := contactsAPI(ctx, api, mgr)
	if err != nil || len(contacts) != 1 || !*contacts[0].Mutual {
		t.Fatalf("contacts %+v %v", contacts, err)
	}
	var contactsRaw map[string]json.RawMessage
	if err := json.Unmarshal(contacts[0].Raw, &contactsRaw); err != nil || len(contactsRaw[rawContacts]) == 0 {
		t.Fatalf("contact raw=%s err=%v", contacts[0].Raw, err)
	}
	hits, err := searchContactsAPI(ctx, api, mgr, "ivan", 10)
	if err != nil || len(hits) != 2 || hits[0].Source != "contacts" || hits[1].Source != "global" {
		t.Fatalf("hits %+v %v", hits, err)
	}
	if _, err = addContactAPI(ctx, api, mgr, peer, rpc.ContactAddParams{Phone: "123"}); err != nil {
		t.Fatal(err)
	}
	if _, err = deleteContactAPI(ctx, api, mgr, peer); err != nil {
		t.Fatal(err)
	}
	for _, block := range []bool{true, false} {
		if _, err = blockAPI(ctx, api, peer, block); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 7 {
		t.Fatalf("calls %d", calls)
	}
}
func TestContactWritesDoNotRetryFlood(t *testing.T) {
	for _, request := range []bin.Encoder{&tg.ContactsAddContactRequest{}, &tg.ContactsDeleteContactsRequest{}, &tg.ContactsBlockRequest{}, &tg.ContactsUnblockRequest{}} {
		t.Run(request.(interface{ TypeName() string }).TypeName(), func(t *testing.T) {
			clock := newThrottleClock()
			l := limiterWithClock(t, clock, nil)
			calls := 0
			invoke := throttleMiddleware(l).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return tgerr.New(420, "FLOOD_WAIT_1") }))
			err := invoke(context.Background(), request, nil)
			if calls != 1 || rpcErrorCode(t, mapUserError(err)) != rpc.CodeFloodWait {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestContactFailures(t *testing.T) {
	ctx := context.Background()
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.ContactsBlockRequest:
			return respond(out, &tg.BoolFalse{})
		case *tg.ContactsAddContactRequest:
			t.Fatal("empty name sent to Telegram")
		}
		return nil
	}))
	mgr := testPeers(api)
	peer := mgr.User(user(7, "", "", "valid_name", false))
	if _, err := addContactAPI(ctx, api, mgr, peer, rpc.ContactAddParams{}); rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := blockAPI(ctx, api, peer, true); rpcErrorCode(t, err) != rpc.CodeTelegram {
		t.Fatal(err)
	}
	for _, code := range []string{"CONTACT_NAME_EMPTY", "QUERY_TOO_SHORT", "SEARCH_QUERY_EMPTY"} {
		if rpcErrorCode(t, mapUserError(tgerr.New(400, code))) != rpc.CodeInvalidParams {
			t.Fatal(code)
		}
	}
}
