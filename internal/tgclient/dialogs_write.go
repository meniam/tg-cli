package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"reflect"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) ArchiveDialogs(ctx context.Context, refs []chatref.Ref, archive bool) error {
	items := make([]tg.InputFolderPeer, 0, len(refs))
	folder := 0
	if archive {
		folder = 1
	}
	for _, ref := range refs {
		p, err := c.resolve(ctx, ref)
		if err != nil {
			return err
		}
		items = append(items, tg.InputFolderPeer{Peer: p.InputPeer(), FolderID: folder})
	}
	updates, err := c.tg.API().FoldersEditPeerFolders(ctx, items)
	if err != nil {
		return mapError(err)
	}
	m, err := c.manager(ctx)
	if err != nil {
		return err
	}
	if err := applyContactUpdates(ctx, m, updates); err != nil {
		return err
	}
	return c.updates.Handle(ctx, updates)
}
func (c *Client) PinDialog(ctx context.Context, ref chatref.Ref, pin bool) error {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return err
	}
	req := &tg.MessagesToggleDialogPinRequest{Peer: &tg.InputDialogPeer{Peer: p.InputPeer()}}
	req.SetPinned(pin)
	ok, err := c.tg.API().MessagesToggleDialogPin(ctx, req)
	if tgerr.Is(err, "PINNED_DIALOGS_TOO_MUCH") {
		return rpc.NewError(rpc.CodeTelegram, "the pinned dialog limit has been reached")
	}
	if err != nil {
		return mapError(err)
	}
	if !ok {
		return rpc.NewError(rpc.CodeTelegram, "Telegram rejected dialog pin")
	}
	return nil
}

func (c *Client) RefreshDialogStates(ctx context.Context, refs []chatref.Ref) (rpc.ChatStatesResult, error) {
	peers := make([]tg.InputDialogPeerClass, 0, len(refs))
	for _, ref := range refs {
		p, err := c.resolve(ctx, ref)
		if err != nil {
			return rpc.ChatStatesResult{}, err
		}
		peers = append(peers, &tg.InputDialogPeer{Peer: p.InputPeer()})
	}
	response, err := c.tg.API().MessagesGetPeerDialogs(ctx, peers)
	if err != nil {
		return rpc.ChatStatesResult{}, mapError(err)
	}
	mgr, err := c.manager(ctx)
	if err != nil {
		return rpc.ChatStatesResult{}, err
	}
	if err = mgr.Apply(ctx, response.Users, response.Chats); err != nil {
		return rpc.ChatStatesResult{}, mapError(err)
	}
	out := rpc.ChatStatesResult{States: []rpc.ChatState{}}
	for _, raw := range response.Dialogs {
		if d, ok := raw.(*tg.Dialog); ok {
			if state, ok := dialogState(d); ok {
				out.States = append(out.States, state)
			}
		}
	}
	return out, nil
}

func notifySettingsInput(current *tg.PeerNotifySettings, p rpc.NotifyParams) tg.InputPeerNotifySettings {
	var next tg.InputPeerNotifySettings
	if v, ok := current.GetShowPreviews(); ok {
		next.SetShowPreviews(v)
	}
	if v, ok := current.GetSilent(); ok {
		next.SetSilent(v)
	}
	if v, ok := current.GetMuteUntil(); ok {
		next.SetMuteUntil(v)
	}
	if v, ok := current.GetOtherSound(); ok {
		next.SetSound(v)
	}
	if v, ok := current.GetStoriesMuted(); ok {
		next.SetStoriesMuted(v)
	}
	if v, ok := current.GetStoriesHideSender(); ok {
		next.SetStoriesHideSender(v)
	}
	if v, ok := current.GetStoriesOtherSound(); ok {
		next.SetStoriesSound(v)
	}
	if p.Previews != nil {
		next.SetShowPreviews(*p.Previews)
	}
	if p.Silent != nil {
		next.SetSilent(*p.Silent)
	}
	if p.MuteUntil != nil {
		next.SetMuteUntil(*p.MuteUntil)
	}
	return next
}
func (c *Client) ChangeNotify(ctx context.Context, ref chatref.Ref, p rpc.NotifyParams) (rpc.NotifyResult, error) {
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.NotifyResult{}, err
	}
	input := &tg.InputNotifyPeer{Peer: peer.InputPeer()}
	current, err := c.tg.API().AccountGetNotifySettings(ctx, input)
	if err != nil {
		return rpc.NotifyResult{}, mapError(err)
	}
	if !samePlatformSounds(current) {
		return rpc.NotifyResult{}, rpc.NewError(rpc.CodeTelegram, "cannot safely preserve different platform notification sounds")
	}
	next := notifySettingsInput(current, p)
	if !p.DryRun {
		ok, e := c.tg.API().AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{Peer: input, Settings: next})
		if e != nil {
			return rpc.NotifyResult{}, mapError(e)
		}
		if !ok {
			return rpc.NotifyResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram rejected notification settings")
		}
	}
	return rpc.NotifyResult{ChatID: int64(peer.TDLibPeerID()), MuteUntil: optInt(next.GetMuteUntil()), Silent: optBool(next.GetSilent()), ShowPreviews: optBool(next.GetShowPreviews()), DryRun: p.DryRun}, nil
}

func samePlatformSounds(current *tg.PeerNotifySettings) bool {
	a, aok := current.GetIosSound()
	b, bok := current.GetAndroidSound()
	c, cok := current.GetOtherSound()
	if aok != bok || bok != cok || (aok && (!reflect.DeepEqual(a, b) || !reflect.DeepEqual(b, c))) {
		return false
	}
	a, aok = current.GetStoriesIosSound()
	b, bok = current.GetStoriesAndroidSound()
	c, cok = current.GetStoriesOtherSound()
	return aok == bok && bok == cok && (!aok || (reflect.DeepEqual(a, b) && reflect.DeepEqual(b, c)))
}
