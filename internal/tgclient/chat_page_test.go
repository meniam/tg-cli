package tgclient

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestDialogPageOffsets(t *testing.T) {
	// Two pinned users, an ordinary user, a bot, a hidden user and two users.
	// The server repeats the offset peer at the boundary.
	query := dialogs.QueryFunc(func(_ context.Context, req dialogs.Request) (tg.MessagesDialogsClass, error) {
		start := 1
		if p, ok := req.OffsetPeer.(*tg.InputPeerUser); ok {
			start = int(p.UserID)
			if req.OffsetID != start*10 || req.OffsetDate != 100-start || p.AccessHash != int64(start*9) {
				t.Fatalf("wrong Telegram offset %+v, peer %+v", req, p)
			}
		}
		res := &tg.MessagesDialogs{}
		for id := start; id <= 7; id++ {
			peer := &tg.PeerUser{UserID: int64(id)}
			u := &tg.User{ID: int64(id), Bot: id == 4, AccessHash: int64(id * 9)}
			res.Users = append(res.Users, u)
			res.Dialogs = append(res.Dialogs, &tg.Dialog{Peer: peer, Pinned: id <= 2})
			res.Messages = append(res.Messages, &tg.Message{PeerID: peer, ID: id * 10, Date: 100 - id})
		}
		return res, nil
	})
	var after rpc.DialogOffset
	for i, want := range [][]int64{{1}, {2, 3}, {6}, {7}} {
		cached := false
		chats, next, err := readDialogPage(t.Context(), dialogs.NewIterator(query, 100), len(want), "user", after, func(id int64) bool { return id != 5 }, func(users []tg.UserClass, _ []tg.ChatClass) error { cached = len(users) > 0; return nil })
		var ids []int64
		for _, chat := range chats {
			ids = append(ids, chat.ID)
		}
		if err != nil || !cached || !slices.Equal(ids, want) {
			t.Fatalf("page %d: ids %v, cache %v, error %v", i, ids, cached, err)
		}
		if i == 3 {
			if next != nil {
				t.Fatal("cursor after final page")
			}
			break
		}
		if next == nil {
			t.Fatal("missing cursor")
		}
		if i == 1 && next.PeerID != 5 {
			t.Fatalf("cursor should pass both filtered dialogs: %+v", next)
		}
		after = *next
	}
}

func TestDialogPageFloodDuringLookahead(t *testing.T) {
	calls := 0
	query := dialogs.QueryFunc(func(context.Context, dialogs.Request) (tg.MessagesDialogsClass, error) {
		calls++
		if calls > 1 {
			return nil, tgerr.New(420, "FLOOD_WAIT_60")
		}
		peer := &tg.PeerUser{UserID: 42}
		return &tg.MessagesDialogsSlice{Count: 10, Dialogs: []tg.DialogClass{&tg.Dialog{Peer: peer}}, Messages: []tg.MessageClass{&tg.Message{PeerID: peer, ID: 1, Date: 2}}}, nil
	})
	chats, next, err := readDialogPage(t.Context(), dialogs.NewIterator(query, 1), 1, "", rpc.DialogOffset{}, func(int64) bool { return true }, nil)
	var rpcErr *rpc.Error
	if chats != nil || next != nil || !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait {
		t.Fatalf("partial result %v, %v, %v", chats, next, err)
	}
}

func TestDialogPageAcrossHiddenBatches(t *testing.T) {
	const total = 600
	// Only the first and last dialogs are visible. Telegram includes the offset
	// peer on the next batch, so counting raw rows would lose the final chat.
	q := dialogs.QueryFunc(func(_ context.Context, req dialogs.Request) (tg.MessagesDialogsClass, error) {
		start := 1
		if p, ok := req.OffsetPeer.(*tg.InputPeerChat); ok {
			start = int(p.ChatID)
		}
		page := &tg.MessagesDialogsSlice{Count: total}
		for id := start; id <= total && id < start+req.Limit; id++ {
			peer := &tg.PeerChat{ChatID: int64(id)}
			page.Dialogs = append(page.Dialogs, &tg.Dialog{Peer: peer})
			page.Messages = append(page.Messages, &tg.Message{ID: id, PeerID: peer, Date: total + 1 - id})
		}
		return page, nil
	})
	allowed := func(id int64) bool { return id == -1 || id == -total }
	cache := func([]tg.UserClass, []tg.ChatClass) error { return nil }
	chats, next, err := readDialogPage(t.Context(), chatDialogIterator(q, 100), 2, "group", rpc.DialogOffset{}, allowed, cache)
	if err != nil || next != nil || len(chats) != 2 || chats[0].ID != -1 || chats[1].ID != -total {
		t.Fatalf("visible page: %+v, next %+v, err %v", chats, next, err)
	}
}
