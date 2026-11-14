package cli

import (
	"context"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (*fakeTelegram) UserProfile(_ context.Context, ref chatref.Ref) (rpc.UserProfile, error) {
	return rpc.UserProfile{ID: ref.ID, Type: "user", FirstName: "Ann", LastName: "Lee", Username: "aliceuser", Usernames: []string{"alice_alt"}, Phone: "123456789", Bio: "Hello", IsContact: true, MutualContact: true, Status: "offline", LastSeen: "2026-01-01T00:00:00Z", CommonChatsCount: 2}, nil
}
func (f *fakeTelegram) Contacts(context.Context) ([]rpc.Contact, error) {
	f.mu.Lock()
	enabled := f.peopleFixture
	f.mu.Unlock()
	if !enabled {
		return nil, nil
	}
	mutual := true
	return []rpc.Contact{{ID: 5, FirstName: "Ann", LastName: "Lee", Username: "aliceuser", Phone: "123456789", Mutual: &mutual}}, nil
}
func (*fakeTelegram) SearchContacts(context.Context, string, int) ([]rpc.ContactHit, error) {
	mutual := true
	return []rpc.ContactHit{{Contact: rpc.Contact{ID: 5, FirstName: "Ann", LastName: "Lee", Username: "aliceuser", Phone: "123456789", Mutual: &mutual}, Source: "contacts"}}, nil
}
func (*fakeTelegram) AddContact(_ context.Context, ref chatref.Ref, p rpc.ContactAddParams) (rpc.ContactAddResult, error) {
	return rpc.ContactAddResult{UserID: ref.ID, FirstName: p.FirstName, LastName: p.LastName, IsContact: true, Mutual: true}, nil
}
func (*fakeTelegram) DeleteContact(_ context.Context, ref chatref.Ref) (rpc.ContactDeleteResult, error) {
	return rpc.ContactDeleteResult{UserID: ref.ID, Deleted: true}, nil
}
func (*fakeTelegram) Block(_ context.Context, ref chatref.Ref) (rpc.BlockResult, error) {
	return rpc.BlockResult{UserID: ref.ID, Blocked: true}, nil
}
func (*fakeTelegram) Unblock(_ context.Context, ref chatref.Ref) (rpc.BlockResult, error) {
	return rpc.BlockResult{UserID: ref.ID, Blocked: false}, nil
}
