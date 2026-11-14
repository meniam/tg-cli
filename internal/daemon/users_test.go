package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type usersFake struct {
	*fakeTelegram
	contacts []rpc.Contact
	hits     []rpc.ContactHit
	writes   int
	reads    int
	fail     bool
}

func (f *usersFake) UserProfile(_ context.Context, ref chatref.Ref) (rpc.UserProfile, error) {
	f.reads++
	return rpc.UserProfile{ID: ref.ID, Type: "user", FirstName: "Name"}, nil
}
func (f *usersFake) Contacts(context.Context) ([]rpc.Contact, error) {
	f.reads++
	if f.fail {
		return nil, errors.New("failure")
	}
	return f.contacts, nil
}
func (f *usersFake) SearchContacts(context.Context, string, int) ([]rpc.ContactHit, error) {
	f.reads++
	return f.hits, nil
}
func (f *usersFake) AddContact(_ context.Context, ref chatref.Ref, p rpc.ContactAddParams) (rpc.ContactAddResult, error) {
	f.writes++
	return rpc.ContactAddResult{UserID: ref.ID, FirstName: p.FirstName, LastName: p.LastName, IsContact: true}, nil
}
func (f *usersFake) DeleteContact(_ context.Context, ref chatref.Ref) (rpc.ContactDeleteResult, error) {
	f.writes++
	return rpc.ContactDeleteResult{UserID: ref.ID, Deleted: true}, nil
}
func (f *usersFake) Block(_ context.Context, ref chatref.Ref) (rpc.BlockResult, error) {
	f.writes++
	return rpc.BlockResult{UserID: ref.ID, Blocked: true}, nil
}
func (f *usersFake) Unblock(_ context.Context, ref chatref.Ref) (rpc.BlockResult, error) {
	f.writes++
	return rpc.BlockResult{UserID: ref.ID}, nil
}
func TestUsersWriteAccessAndDryRun(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &usersFake{fakeTelegram: base}
	d.opts.Telegram = f
	base.resolved = &rpc.Chat{ID: 77, Type: "user", Title: "Name"}
	ctx := context.Background()
	calls := []func(bool) error{
		func(dry bool) error {
			_, e := d.userBlock(ctx, params(t, rpc.BlockParams{User: "77", DryRun: dry}))
			return e
		},
		func(dry bool) error {
			_, e := d.userUnblock(ctx, params(t, rpc.BlockParams{User: "77", DryRun: dry}))
			return e
		},
		func(dry bool) error {
			_, e := d.contactAdd(ctx, params(t, rpc.ContactAddParams{User: "77", FirstName: "Name", DryRun: dry}))
			return e
		},
		func(dry bool) error {
			_, e := d.contactDelete(ctx, params(t, rpc.ContactDeleteParams{User: "77", Yes: true, DryRun: dry}))
			return e
		},
	}
	for _, call := range calls {
		sameRPCError(t, call(false), rpc.ErrWriteDisabled())
	}
	if f.writes != 0 || base.resolveCalls != 0 {
		t.Fatalf("writes=%d resolves=%d", f.writes, base.resolveCalls)
	}
	writeAccess(t, path, `{"write":[77]}`)
	for _, call := range calls {
		if err := call(true); err != nil {
			t.Fatal(err)
		}
	}
	if f.writes != 0 {
		t.Fatal(f.writes)
	}
	writeAccess(t, path, `{"block":[77],"write":[77]}`)
	before := base.resolveCalls
	for _, call := range calls {
		sameRPCError(t, call(false), rpc.ErrUnknownChatID())
	}
	if base.resolveCalls != before || f.writes != 0 {
		t.Fatal("closed user resolved or written")
	}
	writeAccess(t, path, `{"write":[77]}`)
	base.resolved.Type = "group"
	_, err := d.userBlock(ctx, params(t, rpc.BlockParams{User: "77"}))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
}
func TestContactsSyncLocalAndDeleted(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &usersFake{fakeTelegram: base, contacts: []rpc.Contact{{ID: 1, FirstName: "Ёлка", Deleted: true}, {ID: 2, FirstName: "Secret"}}}
	d.opts.Telegram = f
	ctx := context.Background()
	writeAccess(t, path, `{"block":[2]}`)
	_, err := d.contactList(ctx, params(t, rpc.ContactListParams{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.GetPerson(ctx, 2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden person: %v", err)
	}
	base.user = nil
	got, err := d.contactList(ctx, params(t, rpc.ContactListParams{Local: true}))
	if err != nil {
		t.Fatal(err)
	}
	items := got.(rpc.ContactListResult).Contacts
	if len(items) != 1 || !items[0].Deleted || items[0].Mutual != nil {
		t.Fatalf("local %+v", items)
	}
	got, err = d.contactSearch(ctx, params(t, rpc.ContactSearchParams{Query: "елка", Local: true}))
	if err != nil || len(got.(rpc.ContactSearchResult).Contacts) != 1 {
		t.Fatalf("search %+v %v", got, err)
	}
	if f.reads != 1 {
		t.Fatal(f.reads)
	}
	base.user = &rpc.User{ID: 42}
	f.contacts = nil
	_, err = d.contactList(ctx, params(t, rpc.ContactListParams{}))
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.GetPerson(ctx, 1)
	if err != nil || *p.IsContact {
		t.Fatalf("removed contact %+v %v", p, err)
	}
}
func TestContactBackgroundAfterLogin(t *testing.T) {
	d, base, _, _ := accessTestDaemon(t)
	base.user = nil
	f := &usersFake{fakeTelegram: base, fail: true}
	d.opts.Telegram = f
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	auth := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { defer close(done); d.syncContactsAfterLogin(ctx, auth) }()
	auth <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background did not finish")
	}
	if f.reads != 1 {
		t.Fatal(f.reads)
	}
}

func TestContactsAllowedWritesAndSearch(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &usersFake{fakeTelegram: base, hits: []rpc.ContactHit{{Contact: rpc.Contact{ID: 77, FirstName: "Visible"}, Source: "global"}, {Contact: rpc.Contact{ID: 88, FirstName: "Hidden"}, Source: "contacts"}}}
	d.opts.Telegram = f
	base.resolved = &rpc.Chat{ID: 77, Type: "user", Title: "Name"}
	writeAccess(t, path, `{"write":[77],"block":[88]}`)
	ctx := context.Background()
	_, err := d.contactAdd(ctx, params(t, rpc.ContactAddParams{User: "@validuser", FirstName: "Name"}))
	if err != nil || f.writes != 1 {
		t.Fatalf("add: writes=%d err=%v", f.writes, err)
	}
	p, err := db.GetPerson(ctx, 77)
	if err != nil || p.IsContact == nil || !*p.IsContact {
		t.Fatalf("stored: %+v %v", p, err)
	}
	result, err := d.contactSearch(ctx, params(t, rpc.ContactSearchParams{Query: "Name"}))
	if err != nil || len(result.(rpc.ContactSearchResult).Contacts) != 1 {
		t.Fatalf("search: %+v %v", result, err)
	}
	if _, err := db.GetPerson(ctx, 88); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden stored: %v", err)
	}
	_, err = d.contactDelete(ctx, params(t, rpc.ContactDeleteParams{User: "77", Yes: true}))
	if err != nil || f.writes != 2 {
		t.Fatalf("delete: writes=%d err=%v", f.writes, err)
	}
	p, err = db.GetPerson(ctx, 77)
	if err != nil || *p.IsContact {
		t.Fatalf("deleted: %+v %v", p, err)
	}
}
