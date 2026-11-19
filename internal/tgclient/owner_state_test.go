package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestOwnerFoldersConversion(t *testing.T) {
	filters := []tg.DialogFilterClass{&tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "A"}, PinnedPeers: []tg.InputPeerClass{&tg.InputPeerSelf{}, &tg.InputPeerChannel{ChannelID: 9, AccessHash: 42}}}, &tg.DialogFilterDefault{}, &tg.DialogFilterChatlist{ID: 3, Title: tg.TextWithEntities{Text: "Shared"}, IncludePeers: []tg.InputPeerClass{&tg.InputPeerUserFromMessage{UserID: 7}}}}
	if !folderNeedsSelf(filters) {
		t.Fatal("missing self")
	}
	got, err := convertFolders(filters, 5)
	if err != nil || len(got.Folders) != 3 || got.Folders[1].Kind != "all" || got.Folders[1].Position != 1 || got.Folders[0].Pinned[0].ID != 5 || got.Folders[0].Pinned[1].ID != -1000000000009 || got.Folders[2].Include[0].ID != 7 {
		t.Fatalf("folders %+v %v", got, err)
	}
	if _, err = convertFolders(filters, 0); err == nil {
		t.Fatal("missing self accepted")
	}
}
func TestOwnerBlockedPageAndErrors(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.ContactsGetBlockedRequest)
		if !ok || req.Offset != 2 || req.Limit != 3 {
			t.Fatalf("request %+v", in)
		}
		return respond(out, &tg.ContactsBlockedSlice{Count: 5, Blocked: []tg.PeerBlocked{{PeerID: &tg.PeerUser{UserID: 4}, Date: 123}}, Users: []tg.UserClass{user(4, "Ann", "", "ann", false)}})
	}))
	got, _, _, err := blockedPage(context.Background(), api, rpc.BlockedParams{Limit: 3, Cursor: "2"})
	if err != nil || got.NextCursor != "3" || len(got.Peers) != 1 || got.Peers[0].Title != "Ann" {
		t.Fatalf("result %+v %v", got, err)
	}
	for _, e := range []struct {
		err  error
		code int
	}{{tgerr.New(420, "FLOOD_WAIT_30"), rpc.CodeFloodWait}, {tgerr.New(401, "AUTH_KEY_UNREGISTERED"), rpc.CodeNotAuthorized}} {
		bad := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return e.err }))
		_, _, _, err := blockedPage(context.Background(), bad, rpc.BlockedParams{})
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != e.code {
			t.Fatalf("error %v", err)
		}
	}
}
func TestOwnerNotifyReadOnly(t *testing.T) {
	var peers []string
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.AccountGetNotifySettingsRequest)
		if !ok {
			t.Errorf("write or unexpected request %T", in)
			return errors.New("wrong request")
		}
		peers = append(peers, req.Peer.TypeName())
		return respond(out, &tg.PeerNotifySettings{})
	}))
	result, err := notifyDefaultsAPI(context.Background(), api)
	wantPeers := []string{"inputNotifyUsers", "inputNotifyChats", "inputNotifyBroadcasts"}
	if err != nil || !slices.Equal(peers, wantPeers) || len(result.Defaults) != 3 || result.Defaults[0].Silent != nil || result.Defaults[0].MuteUntil != nil {
		t.Fatalf("result %+v peers %v %v", result, peers, err)
	}
	for i, scope := range []string{"users", "chats", "broadcasts"} {
		if result.Defaults[i].Scope != scope {
			t.Fatalf("defaults[%d].Scope = %q, want %q", i, result.Defaults[i].Scope, scope)
		}
	}
}

func TestOwnerSessionsAndDialogState(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.AccountGetAuthorizationsRequest); !ok {
			t.Fatalf("unexpected request %T", in)
		}
		return respond(out, &tg.AccountAuthorizations{AuthorizationTTLDays: 90, Authorizations: []tg.Authorization{{Hash: 42, Current: true, DeviceModel: "device", IP: "secret", Country: "X"}}})
	}))
	got, err := sessionsAPI(context.Background(), api)
	if err != nil || len(got.Sessions) != 1 || got.Sessions[0].Hash != 42 || !got.Sessions[0].Current || got.AuthorizationTTLDays != 90 {
		t.Fatalf("sessions %+v %v", got, err)
	}
	data, err := json.Marshal(got)
	if err != nil || strings.Contains(string(data), "secret") || strings.Contains(string(data), "42") {
		t.Fatalf("session leaked private fields: %s %v", data, err)
	}
	d := &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 1}, UnreadCount: 5, UnreadMark: true, TopMessage: 9}
	d.SetFolderID(1)
	d.NotifySettings.SetSilent(false)
	d.NotifySettings.SetMuteUntil(0)
	s, ok := dialogState(d)
	if !ok || s.ChatID != -1000000000001 || !s.Archived || s.Silent == nil || *s.Silent || s.MuteUntil == nil || *s.MuteUntil != 0 || s.ShowPreviews != nil || s.Unread != 5 {
		t.Fatalf("dialog %+v", s)
	}
}

func TestOwnerFolderAndSingleDialogRequests(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch v := in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			return respond(out, &tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{&tg.DialogFilterDefault{}}})
		case *tg.MessagesGetPeerDialogsRequest:
			if len(v.Peers) != 1 {
				t.Fatalf("peers %+v", v.Peers)
			}
			p, ok := v.Peers[0].(*tg.InputDialogPeer)
			if !ok {
				t.Fatalf("peer %T", v.Peers[0])
			}
			if _, ok = p.Peer.(*tg.InputPeerSelf); !ok {
				t.Fatalf("input %T", p.Peer)
			}
			return respond(out, &tg.MessagesPeerDialogs{})
		default:
			t.Fatalf("unexpected request %T", in)
			return nil
		}
	}))
	folders, err := foldersAPI(context.Background(), api, func(context.Context) (*rpc.User, error) { t.Fatal("unneeded self lookup"); return nil, nil })
	if err != nil || len(folders.Folders) != 1 || folders.Folders[0].Kind != "all" {
		t.Fatalf("folders %+v %v", folders, err)
	}
	if _, err = peerDialogsAPI(context.Background(), api, &tg.InputPeerSelf{}); err != nil {
		t.Fatal(err)
	}
}
