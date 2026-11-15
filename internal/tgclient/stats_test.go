package tgclient

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestStatsAsyncGraphAndBoosts(t *testing.T) {
	var loads int
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
		switch r := input.(type) {
		case *tg.StatsGetMegagroupStatsRequest:
			ready := &tg.StatsGraph{JSON: tg.DataJSON{Data: `{"columns":[]}`}}
			v := &tg.StatsMegagroupStats{GrowthGraph: &tg.StatsGraphAsync{Token: "abc"}, MembersGraph: ready, NewMembersBySourceGraph: ready, LanguagesGraph: ready, MessagesGraph: ready, ActionsGraph: ready, TopHoursGraph: ready, WeekdaysGraph: ready}
			return respond(out, v)
		case *tg.StatsLoadAsyncGraphRequest:
			if r.Token != "abc" {
				t.Fatalf("token: %q", r.Token)
			}
			loads++
			return respond(out, &tg.StatsGraph{JSON: tg.DataJSON{Data: `{"columns":[1]}`}})
		case *tg.PremiumGetBoostsStatusRequest:
			v := &tg.PremiumBoostsStatus{Level: 2, Boosts: 8, BoostURL: "https://t.me/boost/test"}
			v.SetGiftBoosts(0)
			return respond(out, v)
		default:
			t.Fatalf("unexpected request %T", input)
			return nil
		}
	}))
	p := testPeers(api).Channel(&tg.Channel{ID: 8, Title: "group", Megagroup: true})
	v, err := chatStatsResolved(context.Background(), api, p, "")
	if err != nil || len(v.Graphs) != 8 || loads != 0 {
		t.Fatalf("summary: %+v %v", v, err)
	}
	v, err = chatStatsResolved(context.Background(), api, p, "growth")
	if err != nil || string(v.Data) != `{"columns":[1]}` || loads != 1 {
		t.Fatalf("graph: %+v %v", v, err)
	}
	if _, err := chatStatsResolved(context.Background(), api, p, "unknown"); err == nil || loads != 1 {
		t.Fatalf("unknown graph: %v", err)
	}
	b, err := chatBoostsResolved(context.Background(), api, p)
	if err != nil || b.Level != 2 || b.GiftBoosts == nil || *b.GiftBoosts != 0 {
		t.Fatalf("boosts: %+v %v", b, err)
	}
}

func TestBroadcastStatsAndPermissionError(t *testing.T) {
	for _, denied := range []bool{false, true} {
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
			if _, ok := input.(*tg.StatsGetBroadcastStatsRequest); !ok {
				t.Fatalf("unexpected %T", input)
			}
			if denied {
				return tgerr.New(400, "CHAT_ADMIN_REQUIRED")
			}
			g := &tg.StatsGraphError{Error: "not available"}
			return respond(out, &tg.StatsBroadcastStats{Followers: tg.StatsAbsValueAndPrev{Current: 10, Previous: 8}, GrowthGraph: g, FollowersGraph: g, MuteGraph: g, TopHoursGraph: g, InteractionsGraph: g, IvInteractionsGraph: g, ViewsBySourceGraph: g, NewFollowersBySourceGraph: g, LanguagesGraph: g, ReactionsByEmotionGraph: g, StoryInteractionsGraph: g, StoryReactionsByEmotionGraph: g})
		}))
		p := testPeers(api).Channel(&tg.Channel{ID: 8, Title: "news", Broadcast: true})
		v, err := chatStatsResolved(context.Background(), api, p, "")
		if denied {
			if rpcErrorCode(t, err) != rpc.CodeTelegram {
				t.Fatalf("permission: %v", err)
			}
		} else if err != nil || v.Kind != "broadcast" || v.Followers.Current != 10 || len(v.Graphs) != 12 || v.Graphs[0].State != "error" {
			t.Fatalf("broadcast: %+v %v", v, err)
		}
		if !denied {
			_, err = chatStatsResolved(context.Background(), api, p, "growth")
			if rpcErrorCode(t, err) != rpc.CodeTelegram || err.(*rpc.Error).Message != "not available" {
				t.Fatalf("graph error: %v", err)
			}
		}
	}
}
