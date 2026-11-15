package tgclient

import (
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
	"tg-cli/internal/rpc"
)

func TestFolderOrderRequiresCompleteSet(t *testing.T) {
	writes := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			return respond(out, &tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{&tg.DialogFilterDefault{}, &tg.DialogFilter{ID: 2}, &tg.DialogFilterChatlist{ID: 3}}})
		case *tg.MessagesUpdateDialogFiltersOrderRequest:
			writes++
			return respond(out, &tg.BoolTrue{})
		}
		t.Fatalf("unexpected %T", in)
		return nil
	}))
	err := orderFoldersAPI(context.Background(), api, []int{2}, false)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams || writes != 0 {
		t.Fatalf("partial order: %v writes=%d", err, writes)
	}
	if err = orderFoldersAPI(context.Background(), api, []int{3, 2}, true); err != nil || writes != 0 {
		t.Fatalf("dry-run: %v writes=%d", err, writes)
	}
	if err = orderFoldersAPI(context.Background(), api, []int{3, 2}, false); err != nil || writes != 1 {
		t.Fatalf("write: %v writes=%d", err, writes)
	}
}

func TestFolderEditPreservesHiddenPeersAndRules(t *testing.T) {
	original := &tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "Old"}, PinnedPeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 9, AccessHash: 123}}, IncludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 8, AccessHash: 456}}}
	original.SetGroups(true)
	original.SetExcludeMuted(true)
	title := "New"
	writes := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch v := in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			return respond(out, &tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{original}})
		case *tg.MessagesUpdateDialogFilterRequest:
			writes++
			f, ok := v.Filter.(*tg.DialogFilter)
			if !ok || f.Title.Text != "New" || !f.Groups || !f.ExcludeMuted || len(f.PinnedPeers) != 1 || len(f.IncludePeers) != 1 {
				t.Fatalf("filter %+v", v.Filter)
			}
			pinned, ok := f.PinnedPeers[0].(*tg.InputPeerChannel)
			if !ok || pinned.AccessHash != 123 {
				t.Fatalf("pinned %+v", f.PinnedPeers)
			}
			included, ok := f.IncludePeers[0].(*tg.InputPeerUser)
			if !ok || included.AccessHash != 456 {
				t.Fatalf("included %+v", f.IncludePeers)
			}
			return respond(out, &tg.BoolTrue{})
		}
		t.Fatalf("unexpected %T", in)
		return nil
	}))
	result, err := writeFolderAPI(context.Background(), api, nil, nil, rpc.FolderWriteParams{ID: 2, Title: &title}, "edit")
	if err != nil || writes != 1 || result.ID != 2 {
		t.Fatalf("result %+v writes=%d err=%v", result, writes, err)
	}
}

func TestFolderCreateUsesFirstAvailableIDAndRejectsShared(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetDialogFiltersRequest); !ok {
			t.Fatalf("unexpected write %T", in)
		}
		return respond(out, &tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 2}, &tg.DialogFilterChatlist{ID: 4}}})
	}))
	title, rules := "Folder", "groups"
	out, err := writeFolderAPI(context.Background(), api, nil, nil, rpc.FolderWriteParams{Title: &title, Rules: &rules, DryRun: true}, "create")
	if err != nil || out.ID != 3 {
		t.Fatalf("create: %+v %v", out, err)
	}
	_, err = writeFolderAPI(context.Background(), api, nil, nil, rpc.FolderWriteParams{ID: 4, DryRun: true}, "delete")
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams {
		t.Fatalf("shared: %v", err)
	}
}
