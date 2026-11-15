package tgclient

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func statsValue(v tg.StatsAbsValueAndPrev) *rpc.StatsValue {
	return &rpc.StatsValue{Current: v.Current, Previous: v.Previous}
}
func statsPercent(v tg.StatsPercentValue) float64 {
	if v.Total == 0 {
		return 0
	}
	return 100 * v.Part / v.Total
}

func (c *Client) ChatStats(ctx context.Context, ref chatref.Ref, name string) (rpc.ChatStats, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatStats{}, err
	}
	return chatStatsResolved(ctx, c.tg.API(), p, name)
}

func chatStatsResolved(ctx context.Context, api *tg.Client, p peers.Peer, name string) (rpc.ChatStats, error) {
	chat := toChatOf(p)
	channel, ok := peer.ToInputChannel(p.InputPeer())
	if !ok {
		return rpc.ChatStats{}, rpc.NewError(rpc.CodeInvalidParams, "statistics require a channel or supergroup")
	}
	out := rpc.ChatStats{ChatID: chat.ID}
	graphs := map[string]tg.StatsGraphClass{}
	switch chat.Type {
	case "channel":
		v, err := api.StatsGetBroadcastStats(ctx, &tg.StatsGetBroadcastStatsRequest{Channel: channel})
		if err != nil {
			return out, mapError(err)
		}
		out.Kind = "broadcast"
		out.Period = &rpc.StatsPeriod{MinDate: formatDate(v.Period.MinDate), MaxDate: formatDate(v.Period.MaxDate)}
		out.Followers = statsValue(v.Followers)
		out.ViewsPerPost = statsValue(v.ViewsPerPost)
		out.SharesPerPost = statsValue(v.SharesPerPost)
		out.ReactionsPerPost = statsValue(v.ReactionsPerPost)
		out.ViewsPerStory = statsValue(v.ViewsPerStory)
		out.SharesPerStory = statsValue(v.SharesPerStory)
		out.ReactionsPerStory = statsValue(v.ReactionsPerStory)
		pct := statsPercent(v.EnabledNotifications)
		out.EnabledNotificationsPercent = &pct
		graphs = map[string]tg.StatsGraphClass{"growth": v.GrowthGraph, "followers": v.FollowersGraph, "mute": v.MuteGraph, "top_hours": v.TopHoursGraph, "interactions": v.InteractionsGraph, "iv_interactions": v.IvInteractionsGraph, "views_by_source": v.ViewsBySourceGraph, "new_followers_by_source": v.NewFollowersBySourceGraph, "languages": v.LanguagesGraph, "reactions_by_emotion": v.ReactionsByEmotionGraph, "story_interactions": v.StoryInteractionsGraph, "story_reactions_by_emotion": v.StoryReactionsByEmotionGraph}
		for _, item := range v.RecentPostsInteractions {
			switch v := item.(type) {
			case *tg.PostInteractionCountersMessage:
				out.RecentPosts = append(out.RecentPosts, rpc.StatsPost{Kind: "message", ID: v.MsgID, Views: v.Views, Forwards: v.Forwards, Reactions: v.Reactions})
			case *tg.PostInteractionCountersStory:
				out.RecentPosts = append(out.RecentPosts, rpc.StatsPost{Kind: "story", ID: v.StoryID, Views: v.Views, Forwards: v.Forwards, Reactions: v.Reactions})
			}
		}
	case "supergroup":
		v, err := api.StatsGetMegagroupStats(ctx, &tg.StatsGetMegagroupStatsRequest{Channel: channel})
		if err != nil {
			return out, mapError(err)
		}
		out.Kind = "megagroup"
		out.Period = &rpc.StatsPeriod{MinDate: formatDate(v.Period.MinDate), MaxDate: formatDate(v.Period.MaxDate)}
		out.Members = statsValue(v.Members)
		out.Messages = statsValue(v.Messages)
		out.Viewers = statsValue(v.Viewers)
		out.Posters = statsValue(v.Posters)
		graphs = map[string]tg.StatsGraphClass{"growth": v.GrowthGraph, "members": v.MembersGraph, "new_members_by_source": v.NewMembersBySourceGraph, "languages": v.LanguagesGraph, "messages": v.MessagesGraph, "actions": v.ActionsGraph, "top_hours": v.TopHoursGraph, "weekdays": v.WeekdaysGraph}
		names := map[int64]string{}
		for _, item := range v.Users {
			if u, ok := item.(*tg.User); ok {
				names[u.ID] = displayName(u)
			}
		}
		for _, p := range v.TopPosters {
			out.TopPosters = append(out.TopPosters, rpc.StatsPerson{UserID: p.UserID, Name: names[p.UserID], Messages: p.Messages, AvgChars: p.AvgChars})
		}
		for _, p := range v.TopAdmins {
			out.TopAdmins = append(out.TopAdmins, rpc.StatsPerson{UserID: p.UserID, Name: names[p.UserID], Deleted: p.Deleted, Kicked: p.Kicked, Banned: p.Banned})
		}
		for _, p := range v.TopInviters {
			out.TopInviters = append(out.TopInviters, rpc.StatsPerson{UserID: p.UserID, Name: names[p.UserID], Invitations: p.Invitations})
		}
	default:
		return out, rpc.NewError(rpc.CodeInvalidParams, "statistics require a channel or supergroup")
	}
	keys := make([]string, 0, len(graphs))
	for key := range graphs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if name != "" {
		graph, exists := graphs[name]
		if !exists {
			return out, rpc.NewError(rpc.CodeInvalidParams, "unknown graph; choose: "+strings.Join(keys, ", "))
		}
		if async, ok := graph.(*tg.StatsGraphAsync); ok {
			var err error
			graph, err = api.StatsLoadAsyncGraph(ctx, &tg.StatsLoadAsyncGraphRequest{Token: async.Token})
			if err != nil {
				return out, mapError(err)
			}
		}
		switch v := graph.(type) {
		case *tg.StatsGraph:
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(v.JSON.Data), &object) != nil || object == nil {
				return out, rpc.NewError(rpc.CodeTelegram, "invalid graph data")
			}
			return rpc.ChatStats{ChatID: chat.ID, Name: name, Data: json.RawMessage(v.JSON.Data)}, nil
		case *tg.StatsGraphError:
			return out, rpc.NewError(rpc.CodeTelegram, v.Error)
		default:
			return out, rpc.NewError(rpc.CodeTelegram, "graph is not ready")
		}
	}
	for _, key := range keys {
		state := "error"
		switch graphs[key].(type) {
		case *tg.StatsGraph:
			state = "ready"
		case *tg.StatsGraphAsync:
			state = "async"
		}
		out.Graphs = append(out.Graphs, rpc.StatsGraph{Name: key, State: state})
	}
	return out, nil
}

func (c *Client) ChatBoosts(ctx context.Context, ref chatref.Ref) (rpc.ChatBoosts, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ChatBoosts{}, err
	}
	return chatBoostsResolved(ctx, c.tg.API(), p)
}
func chatBoostsResolved(ctx context.Context, api *tg.Client, p peers.Peer) (rpc.ChatBoosts, error) {
	v, err := api.PremiumGetBoostsStatus(ctx, p.InputPeer())
	if err != nil {
		return rpc.ChatBoosts{}, mapError(err)
	}
	out := rpc.ChatBoosts{ChatID: toChatOf(p).ID, Level: v.Level, CurrentLevelBoosts: v.CurrentLevelBoosts, Boosts: v.Boosts, BoostURL: v.BoostURL, MyBoost: v.MyBoost}
	if n, ok := v.GetGiftBoosts(); ok {
		out.GiftBoosts = &n
	}
	if n, ok := v.GetNextLevelBoosts(); ok {
		out.NextLevelBoosts = &n
	}
	if v, ok := v.GetPremiumAudience(); ok {
		n := statsPercent(v)
		out.PremiumAudiencePercent = &n
	}
	return out, nil
}
