package tgclient

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func inputFolderID(p tg.InputPeerClass, self int64) (int64, error) {
	switch v := p.(type) {
	case *tg.InputPeerSelf:
		if self == 0 {
			return 0, fmt.Errorf("self id is unavailable")
		}
		return self, nil
	case *tg.InputPeerUser:
		return v.UserID, nil
	case *tg.InputPeerUserFromMessage:
		return v.UserID, nil
	case *tg.InputPeerChat:
		return -v.ChatID, nil
	case *tg.InputPeerChannel:
		return -channelIDBase - v.ChannelID, nil
	case *tg.InputPeerChannelFromMessage:
		return -channelIDBase - v.ChannelID, nil
	default:
		return 0, fmt.Errorf("unsupported folder peer %T", p)
	}
}
func folderPeers(in []tg.InputPeerClass, self int64) ([]rpc.FolderPeer, error) {
	out := make([]rpc.FolderPeer, 0, len(in))
	for _, p := range in {
		id, err := inputFolderID(p, self)
		if err != nil {
			return nil, err
		}
		out = append(out, rpc.FolderPeer{ID: id})
	}
	return out, nil
}
func optString(v string, ok bool) *string {
	if !ok {
		return nil
	}
	return &v
}
func optInt(v int, ok bool) *int {
	if !ok {
		return nil
	}
	return &v
}
func optBool(v bool, ok bool) *bool {
	if !ok {
		return nil
	}
	return &v
}
func folderNeedsSelf(filters []tg.DialogFilterClass) bool {
	for _, raw := range filters {
		var lists [][]tg.InputPeerClass
		switch f := raw.(type) {
		case *tg.DialogFilter:
			lists = [][]tg.InputPeerClass{f.PinnedPeers, f.IncludePeers, f.ExcludePeers}
		case *tg.DialogFilterChatlist:
			lists = [][]tg.InputPeerClass{f.PinnedPeers, f.IncludePeers}
		}
		for _, list := range lists {
			for _, p := range list {
				if _, ok := p.(*tg.InputPeerSelf); ok {
					return true
				}
			}
		}
	}
	return false
}
func convertFolders(filters []tg.DialogFilterClass, self int64) (rpc.FoldersResult, error) {
	out := rpc.FoldersResult{Folders: make([]rpc.Folder, 0, len(filters))}
	for i, raw := range filters {
		f := rpc.Folder{Position: i, Pinned: []rpc.FolderPeer{}, Include: []rpc.FolderPeer{}, Exclude: []rpc.FolderPeer{}}
		var pinned, include, exclude []tg.InputPeerClass
		switch v := raw.(type) {
		case *tg.DialogFilterDefault:
			f.Raw = messageRaw(v)
			f.Kind = "all"
		case *tg.DialogFilter:
			f.Raw = messageRaw(v)
			f.ID = v.ID
			f.Kind = "filter"
			f.Title = v.Title.Text
			f.Emoticon = optString(v.GetEmoticon())
			f.Color = optInt(v.GetColor())
			f.Rules = &rpc.FolderRules{Contacts: v.Contacts, NonContacts: v.NonContacts, Groups: v.Groups, Broadcasts: v.Broadcasts, Bots: v.Bots, ExcludeMuted: v.ExcludeMuted, ExcludeRead: v.ExcludeRead, ExcludeArchived: v.ExcludeArchived}
			pinned, include, exclude = v.PinnedPeers, v.IncludePeers, v.ExcludePeers
		case *tg.DialogFilterChatlist:
			f.Raw = messageRaw(v)
			f.ID = v.ID
			f.Kind = "chatlist"
			f.Title = v.Title.Text
			f.Emoticon = optString(v.GetEmoticon())
			f.Color = optInt(v.GetColor())
			pinned, include = v.PinnedPeers, v.IncludePeers
		default:
			return rpc.FoldersResult{}, fmt.Errorf("unsupported folder type %T", raw)
		}
		var err error
		if f.Pinned, err = folderPeers(pinned, self); err != nil {
			return rpc.FoldersResult{}, err
		}
		if f.Include, err = folderPeers(include, self); err != nil {
			return rpc.FoldersResult{}, err
		}
		if f.Exclude, err = folderPeers(exclude, self); err != nil {
			return rpc.FoldersResult{}, err
		}
		out.Folders = append(out.Folders, f)
	}
	return out, nil
}
func (c *Client) Folders(ctx context.Context) (rpc.FoldersResult, error) {
	return foldersAPI(ctx, c.tg.API(), c.Self)
}

func foldersAPI(ctx context.Context, api *tg.Client, self func(context.Context) (*rpc.User, error)) (rpc.FoldersResult, error) {
	response, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return rpc.FoldersResult{}, mapError(err)
	}
	var id int64
	if folderNeedsSelf(response.Filters) {
		owner, e := self(ctx)
		if e != nil {
			return rpc.FoldersResult{}, e
		}
		if owner == nil {
			return rpc.FoldersResult{}, rpc.NewError(rpc.CodeNotAuthorized, "not logged in: run `auth login` first")
		}
		id = owner.ID
	}
	return convertFolders(response.Filters, id)
}
