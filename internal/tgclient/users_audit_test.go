package tgclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestAudit10_ContactWriteResults(t *testing.T) {
	ctx := context.Background()
	u := user(7, "Ivan", "Petrov", "ivan7", false)
	u.MutualContact = true
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.ContactsAddContactRequest:
			return respond(out, &tg.Updates{Users: []tg.UserClass{u}})
		case *tg.ContactsDeleteContactsRequest:
			return respond(out, &tg.Updates{})
		case *tg.ContactsBlockRequest, *tg.ContactsUnblockRequest:
			return respond(out, &tg.BoolTrue{})
		}
		t.Errorf("unexpected request %T", in)
		return nil
	}))
	mgr := testPeers(api)
	peer := mgr.User(u)

	added, err := addContactAPI(ctx, api, mgr, peer, rpc.ContactAddParams{Phone: "123"})
	if len(added.Raw) == 0 {
		t.Errorf("added raw is empty")
	}
	added.Raw = nil
	if want := (rpc.ContactAddResult{UserID: 7, FirstName: "Ivan", LastName: "Petrov", IsContact: true, Mutual: true}); err != nil || !reflect.DeepEqual(added, want) {
		t.Fatalf("added=%+v err=%v, want %+v", added, err, want)
	}
	renamed, err := addContactAPI(ctx, api, mgr, peer, rpc.ContactAddParams{FirstName: "Vanya", LastName: "P"})
	if err != nil || renamed.FirstName != "Vanya" || renamed.LastName != "P" {
		t.Fatalf("renamed=%+v err=%v", renamed, err)
	}
	deleted, err := deleteContactAPI(ctx, api, mgr, peer)
	if want := (rpc.ContactDeleteResult{UserID: 7, Deleted: true}); err != nil || deleted != want {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	for _, block := range []bool{true, false} {
		got, err := blockAPI(ctx, api, peer, block)
		if want := (rpc.BlockResult{UserID: 7, Blocked: block}); err != nil || got != want {
			t.Fatalf("block=%v: %+v err=%v, want %+v", block, got, err, want)
		}
	}
}

func TestAudit10_UserReadErrorsAndSearchLimits(t *testing.T) {
	ctx := context.Background()
	u := user(7, "Ivan", "Petrov", "ivan7", false)
	other := user(8, "Other", "", "other", false)
	third := user(9, "Third", "", "third", false)

	t.Run("not modified contact list", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			return respond(out, &tg.ContactsContactsNotModified{})
		}))
		_, err := contactsAPI(ctx, api, testPeers(api))
		if e := audit10RPCError(err); e == nil || e.Code != rpc.CodeTelegram || e.Message != "Telegram returned an incomplete contact list" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("contact without a user", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			return respond(out, &tg.ContactsContacts{Contacts: []tg.Contact{{UserID: 99}}, Users: []tg.UserClass{u}})
		}))
		_, err := contactsAPI(ctx, api, testPeers(api))
		if e := audit10RPCError(err); e == nil || e.Code != rpc.CodeTelegram || e.Message != "Telegram returned a contact without a user" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("profile without the requested user", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			return respond(out, &tg.UsersUserFull{FullUser: tg.UserFull{ID: 7}, Users: []tg.UserClass{other}})
		}))
		mgr := testPeers(api)
		_, err := userProfileAPI(ctx, api, mgr, mgr.User(u))
		if e := audit10RPCError(err); e == nil || e.Code != rpc.CodeTelegram || e.Message != "Telegram returned no user" {
			t.Fatalf("err=%v", err)
		}
	})
	searchAPI := func() *tg.Client {
		return tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			return respond(out, &tg.ContactsFound{
				MyResults: []tg.PeerClass{&tg.PeerUser{UserID: 7}, &tg.PeerUser{UserID: 8}},
				Results:   []tg.PeerClass{&tg.PeerUser{UserID: 7}, &tg.PeerUser{UserID: 9}, &tg.PeerUser{UserID: 404}},
				Users:     []tg.UserClass{u, other, third},
			})
		}))
	}
	ids := func(hits []rpc.ContactHit) []int64 {
		var out []int64
		for _, h := range hits {
			out = append(out, h.ID)
		}
		return out
	}
	t.Run("search deduplicates and skips unknown users", func(t *testing.T) {
		api := searchAPI()
		hits, err := searchContactsAPI(ctx, api, testPeers(api), "iv", 10)
		if err != nil || len(hits) != 3 || ids(hits)[0] != 7 || ids(hits)[1] != 8 || ids(hits)[2] != 9 || hits[0].Source != "contacts" || hits[2].Source != "global" {
			t.Fatalf("hits=%+v err=%v", hits, err)
		}
	})
	t.Run("search stops at the limit", func(t *testing.T) {
		api := searchAPI()
		hits, err := searchContactsAPI(ctx, api, testPeers(api), "iv", 2)
		if err != nil || len(hits) != 2 || ids(hits)[0] != 7 || ids(hits)[1] != 8 {
			t.Fatalf("hits=%+v err=%v", hits, err)
		}
	})
}

func TestAudit10_ContactWritesMapUnknownContact(t *testing.T) {
	ctx := context.Background()
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		return tgerr.New(400, "CONTACT_ID_INVALID")
	}))
	mgr := testPeers(api)
	peer := mgr.User(user(7, "Ivan", "", "ivan7", false))
	for name, call := range map[string]func() error{
		"add":    func() error { _, err := addContactAPI(ctx, api, mgr, peer, rpc.ContactAddParams{}); return err },
		"delete": func() error { _, err := deleteContactAPI(ctx, api, mgr, peer); return err },
		"block":  func() error { _, err := blockAPI(ctx, api, peer, true); return err },
	} {
		if err := call(); rpcErrorCode(t, err) != rpc.CodeNotFound {
			t.Errorf("%s: %v, want not_found", name, err)
		}
	}
}
