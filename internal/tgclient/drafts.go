package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"unicode/utf8"
)

func draftFromTL(id int64, raw tg.DraftMessageClass) rpc.Draft {
	d := rpc.Draft{ChatID: id, Empty: true}
	if v, ok := raw.(*tg.DraftMessage); ok {
		d.Text = v.Message
		d.Empty = false
		if v.Date > 0 {
			d.Date = formatDate(v.Date)
		}
		if r, ok := v.ReplyTo.(*tg.InputReplyToMessage); ok {
			d.ReplyTo = r.ReplyToMsgID
		}
	}
	return d
}
func (c *Client) DraftGet(ctx context.Context, ref chatref.Ref) (rpc.Draft, error) {
	r, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.Draft{}, err
	}
	id := int64(r.TDLibPeerID())
	result, err := c.tg.API().MessagesGetPeerDialogs(ctx, []tg.InputDialogPeerClass{&tg.InputDialogPeer{Peer: r.InputPeer()}})
	if err != nil {
		return rpc.Draft{}, mapError(err)
	}
	for _, v := range result.Dialogs {
		if d, ok := v.(*tg.Dialog); ok {
			raw, _ := d.GetDraft()
			return draftFromTL(id, raw), nil
		}
	}
	return rpc.Draft{ChatID: id, Empty: true}, nil
}
func (c *Client) DraftList(ctx context.Context) (rpc.DraftList, error) {
	u, err := c.tg.API().MessagesGetAllDrafts(ctx)
	if err != nil {
		return rpc.DraftList{}, mapError(err)
	}
	var changes []tg.UpdateClass
	switch v := u.(type) {
	case *tg.Updates:
		changes = v.Updates
	case *tg.UpdatesCombined:
		changes = v.Updates
	case *tg.UpdateShort:
		changes = []tg.UpdateClass{v.Update}
	}
	out := rpc.DraftList{Drafts: []rpc.Draft{}}
	for _, v := range changes {
		if d, ok := v.(*tg.UpdateDraftMessage); ok {
			if _, ok := d.GetTopMsgID(); ok {
				continue
			}
			if _, ok := d.GetSavedPeerID(); ok {
				continue
			}
			id := botPeerID(d.Peer)
			if id != 0 {
				draft := draftFromTL(id, d.Draft)
				if !draft.Empty {
					out.Drafts = append(out.Drafts, draft)
				}
			}
		}
	}
	return out, nil
}
func (c *Client) DraftSet(ctx context.Context, ref chatref.Ref, p rpc.DraftParams) (rpc.DraftWriteResult, error) {
	r, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.DraftWriteResult{}, err
	}
	req := &tg.MessagesSaveDraftRequest{Peer: r.InputPeer(), Message: p.Text}
	if p.ReplyTo > 0 {
		req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: p.ReplyTo}
	}
	ok, err := c.tg.API().MessagesSaveDraft(ctx, req)
	if err != nil {
		return rpc.DraftWriteResult{}, mapActionError(err)
	}
	if !ok {
		return rpc.DraftWriteResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram rejected draft")
	}
	return rpc.DraftWriteResult{ChatID: int64(r.TDLibPeerID()), TextLength: utf8.RuneCountInString(p.Text), Cleared: p.Text == ""}, nil
}
