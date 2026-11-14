package daemon

import (
	"context"
	"errors"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (*fakeTelegram) UserProfile(context.Context, chatref.Ref) (rpc.UserProfile, error) {
	return rpc.UserProfile{}, errors.New("not implemented")
}
func (*fakeTelegram) Contacts(context.Context) ([]rpc.Contact, error) {
	return nil, errors.New("not implemented")
}
func (*fakeTelegram) SearchContacts(context.Context, string, int) ([]rpc.ContactHit, error) {
	return nil, errors.New("not implemented")
}
func (*fakeTelegram) AddContact(context.Context, chatref.Ref, rpc.ContactAddParams) (rpc.ContactAddResult, error) {
	return rpc.ContactAddResult{}, errors.New("not implemented")
}
func (*fakeTelegram) DeleteContact(context.Context, chatref.Ref) (rpc.ContactDeleteResult, error) {
	return rpc.ContactDeleteResult{}, errors.New("not implemented")
}
func (*fakeTelegram) Block(context.Context, chatref.Ref) (rpc.BlockResult, error) {
	return rpc.BlockResult{}, errors.New("not implemented")
}
func (*fakeTelegram) Unblock(context.Context, chatref.Ref) (rpc.BlockResult, error) {
	return rpc.BlockResult{}, errors.New("not implemented")
}
