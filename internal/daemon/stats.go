package daemon

import (
	"context"
	"encoding/json"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type statsTelegram interface {
	ChatStats(context.Context, chatref.Ref, string) (rpc.ChatStats, error)
	ChatBoosts(context.Context, chatref.Ref) (rpc.ChatBoosts, error)
}

func (d *daemon) statsChat(ctx context.Context, value string) (chatref.Ref, rpc.Chat, statsTelegram, error) {
	_, chat, err := d.listChat(ctx, value)
	if err != nil {
		return chatref.Ref{}, chat, nil, err
	}
	if chat.Type != "channel" && chat.Type != "supergroup" {
		return chatref.Ref{}, chat, nil, rpc.NewError(rpc.CodeInvalidParams, "statistics require a channel or supergroup")
	}
	client, ok := d.telegram(ctx).(statsTelegram)
	if !ok {
		return chatref.Ref{}, chat, nil, rpc.NewError(rpc.CodeInternal, "statistics unavailable")
	}
	return chatref.Ref{Kind: chatref.ID, ID: chat.ID}, chat, client, nil
}
func (d *daemon) chatStats(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatStatsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chat, client, err := d.statsChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	v, err := client.ChatStats(ctx, ref, p.Graph)
	if err != nil {
		return nil, err
	}
	if v.ChatID != chat.ID || !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	hide, _, blocked := d.policy().Filter()
	deny := map[int64]bool{}
	for _, id := range blocked {
		deny[id] = true
	}
	members := []rpc.Member{}
	filter := func(in []rpc.StatsPerson) []rpc.StatsPerson {
		out := make([]rpc.StatsPerson, 0, len(in))
		for _, person := range in {
			if !hide && !deny[person.UserID] {
				out = append(out, person)
				members = append(members, rpc.Member{ID: person.UserID, Name: person.Name})
			}
		}
		return out
	}
	if v.TopPosters != nil {
		v.TopPosters = filter(v.TopPosters)
	}
	if v.TopAdmins != nil {
		v.TopAdmins = filter(v.TopAdmins)
	}
	if v.TopInviters != nil {
		v.TopInviters = filter(v.TopInviters)
	}
	d.saveMembers(ctx, members)
	return v, nil
}
func (d *daemon) chatBoosts(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatRefParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chat, client, err := d.statsChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	v, err := client.ChatBoosts(ctx, ref)
	if err != nil {
		return nil, err
	}
	if v.ChatID != chat.ID || !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	return v, nil
}
