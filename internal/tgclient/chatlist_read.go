package tgclient

import (
	"context"
	"fmt"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func (c *Client) FolderInviteInfo(ctx context.Context, slug string) (rpc.FolderInviteResult, error) {
	raw, err := c.tg.API().ChatlistsCheckChatlistInvite(ctx, slug)
	if err != nil {
		return rpc.FolderInviteResult{}, mapError(err)
	}
	out := rpc.FolderInviteResult{Chats: []rpc.FolderInviteChat{}}
	var chats []tg.ChatClass
	var users []tg.UserClass
	var peers []tg.PeerClass
	joined := map[int64]bool{}
	switch v := raw.(type) {
	case *tg.ChatlistsChatlistInvite:
		out.Title = v.Title.Text
		out.Emoticon, _ = v.GetEmoticon()
		chats = v.Chats
		users = v.Users
		peers = v.Peers
	case *tg.ChatlistsChatlistInviteAlready:
		out.Already = true
		id := v.FilterID
		out.FolderID = &id
		chats = v.Chats
		users = v.Users
		peers = append(append([]tg.PeerClass{}, v.AlreadyPeers...), v.MissingPeers...)
		for _, p := range v.AlreadyPeers {
			if id, ok := peerID(p); ok {
				joined[id] = true
			}
		}
		folders, e := c.Folders(ctx)
		if e != nil {
			return out, e
		}
		for _, f := range folders.Folders {
			if f.ID == id {
				out.Title = f.Title
				if f.Emoticon != nil {
					out.Emoticon = *f.Emoticon
				}
				break
			}
		}
	default:
		return out, fmt.Errorf("unsupported chat folder invite response %T", raw)
	}
	details := map[int64]rpc.FolderInviteChat{}
	for _, raw := range chats {
		switch v := raw.(type) {
		case *tg.Chat:
			id := -v.ID
			details[id] = rpc.FolderInviteChat{ID: id, Type: "group", Title: v.Title, Members: v.ParticipantsCount, Joined: !v.Left}
		case *tg.Channel:
			id := -channelIDBase - v.ID
			kind := "channel"
			if v.Megagroup {
				kind = "supergroup"
			}
			details[id] = rpc.FolderInviteChat{ID: id, Type: kind, Title: v.Title, Members: v.ParticipantsCount, Joined: !v.Left}
		}
	}
	for _, raw := range users {
		if v, ok := raw.(*tg.User); ok {
			kind := "user"
			if v.Bot {
				kind = "bot"
			}
			details[v.ID] = rpc.FolderInviteChat{ID: v.ID, Type: kind, Title: v.FirstName + " " + v.LastName}
		}
	}
	for _, p := range peers {
		id, ok := peerID(p)
		if !ok {
			continue
		}
		item, ok := details[id]
		if !ok {
			continue
		}
		if out.Already {
			item.Joined = joined[id]
		}
		out.Chats = append(out.Chats, item)
	}
	return out, nil
}
