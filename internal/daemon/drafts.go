package daemon

import (
	"context"
	"encoding/json"
	"tg-cli/internal/rpc"
	"unicode/utf8"
)

func (d *daemon) draftGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.DraftParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, false)
	if err != nil {
		return nil, err
	}
	res, err := d.telegram(ctx).DraftGet(ctx, ref)
	res.ChatID = id
	return res, err
}
func (d *daemon) draftList(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	res, err := d.telegram(ctx).DraftList(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]rpc.Draft, 0, len(res.Drafts))
	for _, v := range res.Drafts {
		if d.policy().Allowed(v.ChatID) {
			visible = append(visible, v)
		}
	}
	res.Drafts = visible
	return res, nil
}
func (d *daemon) draftSet(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.draftWrite(ctx, raw, false)
}
func (d *daemon) draftClear(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.draftWrite(ctx, raw, true)
}
func (d *daemon) draftWrite(ctx context.Context, raw json.RawMessage, clear bool) (any, error) {
	var p rpc.DraftParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ReplyTo < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "reply id must not be negative")
	}
	if clear {
		p.Text = ""
		p.ReplyTo = 0
	} else if err := validWriteText(p.Text); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		out := rpc.DraftWriteResult{ChatID: id, DryRun: true, Cleared: clear, TextLength: utf8.RuneCountInString(p.Text)}
		if !clear {
			draft, err := d.telegram(ctx).DraftGet(ctx, ref)
			if err != nil {
				return nil, err
			}
			out.Replaces = !draft.Empty
		}
		return out, nil
	}
	out, err := d.telegram(ctx).DraftSet(ctx, ref, p)
	out.ChatID = id
	return out, err
}
