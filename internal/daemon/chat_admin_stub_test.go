package daemon

import (
	"context"
	"errors"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type noChatAdmin struct{}

var errNoChatAdmin = errors.New("chat admin unavailable in test")

func (noChatAdmin) Join(context.Context, chatref.Ref) (rpc.ChatJoinResult, error) {
	return rpc.ChatJoinResult{}, errNoChatAdmin
}
func (noChatAdmin) Leave(context.Context, chatref.Ref) (rpc.ChatLeaveResult, error) {
	return rpc.ChatLeaveResult{}, errNoChatAdmin
}
func (noChatAdmin) CreateChat(context.Context, rpc.ChatCreateParams) (rpc.ChatCreateResult, error) {
	return rpc.ChatCreateResult{}, errNoChatAdmin
}
func (noChatAdmin) EditChat(context.Context, chatref.Ref, rpc.ChatEditParams) (rpc.ChatEditResult, error) {
	return rpc.ChatEditResult{}, errNoChatAdmin
}
func (noChatAdmin) Kick(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error) {
	return rpc.ChatAdminResult{}, errNoChatAdmin
}
func (noChatAdmin) Ban(context.Context, chatref.Ref, chatref.Ref, string) (rpc.ChatAdminResult, error) {
	return rpc.ChatAdminResult{}, errNoChatAdmin
}
func (noChatAdmin) Unban(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error) {
	return rpc.ChatAdminResult{}, errNoChatAdmin
}
func (noChatAdmin) Restrict(context.Context, chatref.Ref, chatref.Ref, []string, bool, string) (rpc.ChatAdminResult, error) {
	return rpc.ChatAdminResult{}, errNoChatAdmin
}
func (noChatAdmin) SetDefaultRights(context.Context, chatref.Ref, []string, bool) (rpc.ChatAdminResult, error) {
	return rpc.ChatAdminResult{}, errNoChatAdmin
}
func (noChatAdmin) SetSlowMode(context.Context, chatref.Ref, int) (rpc.ChatAdminResult, error) {
	return rpc.ChatAdminResult{}, errNoChatAdmin
}
func (noChatAdmin) OwnAdminRights(context.Context, chatref.Ref) (rpc.ChatOwnAdminRights, error) {
	return rpc.ChatOwnAdminRights{}, errNoChatAdmin
}
func (noChatAdmin) AdminInviteChat(context.Context, chatref.Ref) (rpc.Chat, bool, error) {
	return rpc.Chat{}, false, errNoChatAdmin
}
