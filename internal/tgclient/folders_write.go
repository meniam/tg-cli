package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"strings"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"unicode/utf8"

	"github.com/gotd/td/telegram/peers"
)

func (c *Client) OrderFolders(ctx context.Context, ids []int, dry bool) error {
	return orderFoldersAPI(ctx, c.tg.API(), ids, dry)
}

func orderFoldersAPI(ctx context.Context, api *tg.Client, ids []int, dry bool) error {
	f, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return mapError(err)
	}
	existing := map[int]bool{}
	for _, raw := range f.Filters {
		switch v := raw.(type) {
		case *tg.DialogFilter:
			existing[v.ID] = true
		case *tg.DialogFilterChatlist:
			existing[v.ID] = true
		}
	}
	if len(ids) != len(existing) {
		return rpc.NewError(rpc.CodeInvalidParams, "folder order must contain each folder exactly once")
	}
	for _, id := range ids {
		if !existing[id] {
			return rpc.NewError(rpc.CodeInvalidParams, "folder order must contain each folder exactly once")
		}
		delete(existing, id)
	}
	if dry {
		return nil
	}
	ok, err := api.MessagesUpdateDialogFiltersOrder(ctx, ids)
	if err != nil {
		return mapError(err)
	}
	if !ok {
		return rpc.NewError(rpc.CodeTelegram, "Telegram rejected folder order")
	}
	return nil
}

func (c *Client) WriteFolder(ctx context.Context, p rpc.FolderWriteParams, action string) (rpc.FolderWriteResult, error) {
	return writeFolderAPI(ctx, c.tg.API(), c.resolve, c.Self, p, action)
}

func writeFolderAPI(ctx context.Context, api *tg.Client, resolve func(context.Context, chatref.Ref) (peers.Peer, error), self func(context.Context) (*rpc.User, error), p rpc.FolderWriteParams, action string) (rpc.FolderWriteResult, error) {
	response, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return rpc.FolderWriteResult{}, mapError(err)
	}
	used := map[int]bool{}
	var filter *tg.DialogFilter
	for _, raw := range response.Filters {
		switch f := raw.(type) {
		case *tg.DialogFilter:
			used[f.ID] = true
			if f.ID == p.ID {
				copy := *f
				filter = &copy
			}
		case *tg.DialogFilterChatlist:
			used[f.ID] = true
			if f.ID == p.ID {
				return rpc.FolderWriteResult{}, rpc.NewError(rpc.CodeInvalidParams, "shared folders cannot be edited or deleted")
			}
		}
	}
	if action == "create" {
		id := 2
		for used[id] {
			id++
		}
		filter = &tg.DialogFilter{ID: id}
	} else if filter == nil {
		return rpc.FolderWriteResult{}, rpc.NewError(rpc.CodeInvalidParams, "folder does not exist or cannot be edited")
	}
	if action != "delete" {
		var selfID int64
		if folderNeedsSelf([]tg.DialogFilterClass{filter}) {
			owner, e := self(ctx)
			if e != nil {
				return rpc.FolderWriteResult{}, e
			}
			if owner == nil {
				return rpc.FolderWriteResult{}, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
			}
			selfID = owner.ID
		}
		if p.Title != nil {
			filter.Title = tg.TextWithEntities{Text: *p.Title}
		}
		if p.Emoticon != nil {
			filter.SetEmoticon(*p.Emoticon)
		}
		if p.Color != nil {
			filter.SetColor(*p.Color)
		}
		if p.Rules != nil {
			rules := map[string]bool{}
			for _, v := range strings.Split(*p.Rules, ",") {
				if v != "" {
					rules[v] = true
				}
			}
			filter.SetContacts(rules["contacts"])
			filter.SetNonContacts(rules["non-contacts"])
			filter.SetGroups(rules["groups"])
			filter.SetBroadcasts(rules["channels"])
			filter.SetBots(rules["bots"])
			filter.SetExcludeMuted(rules["exclude-muted"])
			filter.SetExcludeRead(rules["exclude-read"])
			filter.SetExcludeArchived(rules["exclude-archived"])
		}
		apply := func(list []tg.InputPeerClass, values []string, remove bool) ([]tg.InputPeerClass, error) {
			for _, value := range values {
				ref, e := chatref.Parse(value)
				if e != nil {
					return nil, e
				}
				peer, e := resolve(ctx, ref)
				if e != nil {
					return nil, e
				}
				id := int64(peer.TDLibPeerID())
				next := make([]tg.InputPeerClass, 0, len(list)+1)
				for _, item := range list {
					existing, e := inputFolderID(item, selfID)
					if e != nil || existing != id {
						next = append(next, item)
					}
				}
				list = next
				if !remove {
					list = append(list, peer.InputPeer())
				}
			}
			return list, nil
		}
		for _, op := range []struct {
			list   *[]tg.InputPeerClass
			values []string
			remove bool
		}{{&filter.IncludePeers, p.Include, false}, {&filter.IncludePeers, p.Add, false}, {&filter.IncludePeers, p.Remove, true}, {&filter.ExcludePeers, p.Exclude, false}, {&filter.ExcludePeers, p.Unexclude, true}, {&filter.PinnedPeers, p.Pin, false}, {&filter.PinnedPeers, p.Unpin, true}} {
			if *op.list, err = apply(*op.list, op.values, op.remove); err != nil {
				return rpc.FolderWriteResult{}, err
			}
		}
		if strings.TrimSpace(filter.Title.Text) == "" || (len(filter.IncludePeers) == 0 && len(filter.PinnedPeers) == 0 && !filter.Contacts && !filter.NonContacts && !filter.Groups && !filter.Broadcasts && !filter.Bots) {
			return rpc.FolderWriteResult{}, rpc.NewError(rpc.CodeInvalidParams, "folder title and an inclusion source are required")
		}
	}
	out := rpc.FolderWriteResult{ID: filter.ID, TitleLength: utf8.RuneCountInString(filter.Title.Text), Include: len(filter.IncludePeers), Exclude: len(filter.ExcludePeers), Pinned: len(filter.PinnedPeers), Deleted: action == "delete", DryRun: p.DryRun}
	if p.DryRun {
		return out, nil
	}
	req := &tg.MessagesUpdateDialogFilterRequest{ID: filter.ID}
	if action != "delete" {
		req.SetFilter(filter)
	}
	ok, err := api.MessagesUpdateDialogFilter(ctx, req)
	if err != nil {
		return rpc.FolderWriteResult{}, mapError(err)
	}
	if !ok {
		return rpc.FolderWriteResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram rejected folder update")
	}
	return out, nil
}
