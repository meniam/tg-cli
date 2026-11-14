package tgclient

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func listLimit(value, maximum int) (int, error) {
	if value == 0 {
		return 50, nil
	}
	if value < 0 || value > maximum {
		return 0, rpc.NewError(rpc.CodeInvalidParams, "limit must be between 1 and "+strconv.Itoa(maximum))
	}
	return value, nil
}

func memberFilter(value, search string) (tg.ChannelParticipantsFilterClass, error) {
	switch value {
	case "", "recent":
		if value == "" && search != "" {
			return &tg.ChannelParticipantsSearch{Q: search}, nil
		}
		return &tg.ChannelParticipantsRecent{}, nil
	case "admins":
		return &tg.ChannelParticipantsAdmins{}, nil
	case "bots":
		return &tg.ChannelParticipantsBots{}, nil
	default:
		return nil, rpc.NewError(rpc.CodeInvalidParams, "filter must be recent, admins, or bots")
	}
}

func memberMatches(m rpc.Member, search string) bool {
	if search == "" {
		return true
	}
	needle := strings.ToLower(search)
	return strings.Contains(strings.ToLower(m.Name), needle) || strings.Contains(strings.ToLower(m.Username), needle)
}

func joinedDate(value int) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(int64(value), 0).UTC().Format(time.RFC3339)
}

func memberOfChannel(raw tg.ChannelParticipantClass, users map[int64]*tg.User) (rpc.Member, bool) {
	var id int64
	var date int
	role := "member"
	switch p := raw.(type) {
	case *tg.ChannelParticipant:
		id, date = p.UserID, p.Date
	case *tg.ChannelParticipantSelf:
		id, date = p.UserID, p.Date
	case *tg.ChannelParticipantCreator:
		id, role = p.UserID, "creator"
	case *tg.ChannelParticipantAdmin:
		id, date, role = p.UserID, p.Date, "admin"
	case *tg.ChannelParticipantBanned:
		if user, ok := p.Peer.(*tg.PeerUser); ok {
			id = user.UserID
		}
		date, role = p.Date, "restricted"
		if p.BannedRights.ViewMessages {
			role = "banned"
		}
	case *tg.ChannelParticipantLeft:
		if user, ok := p.Peer.(*tg.PeerUser); ok {
			id = user.UserID
		}
		role = "left"
	default:
		return rpc.Member{}, false
	}
	if id <= 0 {
		return rpc.Member{}, false
	}
	m := rpc.Member{ID: id, Role: role, Joined: joinedDate(date)}
	if user := users[id]; user != nil {
		m.Name = displayName(user)
		m.Username, _ = user.GetUsername()
		m.IsBot = user.Bot
	}
	return m, true
}

func memberOfGroup(raw tg.ChatParticipantClass, users map[int64]*tg.User) (rpc.Member, bool) {
	var id int64
	var date int
	role := "member"
	switch p := raw.(type) {
	case *tg.ChatParticipant:
		id, date = p.UserID, p.Date
	case *tg.ChatParticipantAdmin:
		id, date, role = p.UserID, p.Date, "admin"
	case *tg.ChatParticipantCreator:
		id, role = p.UserID, "creator"
	default:
		return rpc.Member{}, false
	}
	if id <= 0 {
		return rpc.Member{}, false
	}
	m := rpc.Member{ID: id, Role: role, Joined: joinedDate(date)}
	if user := users[id]; user != nil {
		m.Name = displayName(user)
		m.Username, _ = user.GetUsername()
		m.IsBot = user.Bot
	}
	return m, true
}

func membersAPI(ctx context.Context, api *tg.Client, peer peers.Peer, p rpc.MembersParams, limit int) (rpc.MembersResult, error) {
	switch v := peer.(type) {
	case peers.Channel:
		filter, _ := memberFilter(p.Filter, p.Search)
		response, err := api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{Channel: v.InputChannel(), Filter: filter, Offset: p.Offset, Limit: limit})
		if err != nil {
			if tgerr.Is(err, "CHAT_ADMIN_REQUIRED") {
				return rpc.MembersResult{}, rpc.NewError(rpc.CodeTelegram, "CHAT_ADMIN_REQUIRED: administrator access is required to list channel members")
			}
			return rpc.MembersResult{}, mapError(err)
		}
		list, ok := response.(*tg.ChannelsChannelParticipants)
		if !ok {
			return rpc.MembersResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return a participant list")
		}
		users := make(map[int64]*tg.User, len(list.Users))
		for _, raw := range list.Users {
			if user, ok := raw.(*tg.User); ok {
				users[user.ID] = user
			}
		}
		result := rpc.MembersResult{Members: []rpc.Member{}}
		page := list.Participants[:min(len(list.Participants), limit)]
		for _, raw := range page {
			if m, ok := memberOfChannel(raw, users); ok && (p.Filter == "" || p.Search == "" || memberMatches(m, p.Search)) {
				result.Members = append(result.Members, m)
			}
		}
		if p.Offset < list.Count && len(page) > 0 && len(page) < list.Count-p.Offset {
			result.NextOffset = p.Offset + len(page)
		}
		return result, nil
	case peers.Chat:
		full, err := api.MessagesGetFullChat(ctx, v.ID())
		if err != nil {
			return rpc.MembersResult{}, mapError(err)
		}
		chatFull, ok := full.FullChat.(*tg.ChatFull)
		if !ok {
			return rpc.MembersResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return full group information")
		}
		participants, ok := chatFull.Participants.(*tg.ChatParticipants)
		if !ok {
			return rpc.MembersResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram does not allow viewing this group's participants")
		}
		users := make(map[int64]*tg.User, len(full.Users))
		for _, raw := range full.Users {
			if user, ok := raw.(*tg.User); ok {
				users[user.ID] = user
			}
		}
		all := make([]rpc.Member, 0, len(participants.Participants))
		for _, raw := range participants.Participants {
			m, ok := memberOfGroup(raw, users)
			if !ok {
				continue
			}
			if p.Filter == "admins" && m.Role != "admin" && m.Role != "creator" {
				continue
			}
			if p.Filter == "bots" && !m.IsBot {
				continue
			}
			if !memberMatches(m, p.Search) {
				continue
			}
			all = append(all, m)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
		result := rpc.MembersResult{Members: []rpc.Member{}}
		if p.Offset < len(all) {
			end := p.Offset + min(limit, len(all)-p.Offset)
			result.Members = all[p.Offset:end]
			if end < len(all) {
				result.NextOffset = end
			}
		}
		return result, nil
	default:
		return rpc.MembersResult{}, rpc.NewError(rpc.CodeInvalidParams, "members require a group, supergroup, or channel")
	}
}

func (c *Client) Members(ctx context.Context, ref chatref.Ref, p rpc.MembersParams) (rpc.MembersResult, error) {
	limit, err := listLimit(p.Limit, 200)
	if err != nil {
		return rpc.MembersResult{}, err
	}
	if p.Offset < 0 {
		return rpc.MembersResult{}, rpc.NewError(rpc.CodeInvalidParams, "offset must not be negative")
	}
	if _, err := memberFilter(p.Filter, p.Search); err != nil {
		return rpc.MembersResult{}, err
	}
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MembersResult{}, err
	}
	return membersAPI(ctx, c.tg.API(), peer, p, limit)
}
