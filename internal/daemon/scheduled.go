package daemon

import (
	"context"
	"encoding/json"
	"tg-cli/internal/rpc"
)

func (d *daemon) scheduledList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ScheduledParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, false)
	if err != nil {
		return nil, err
	}
	res, err := d.opts.Telegram.ScheduledList(ctx, ref)
	res.ChatID = id
	return res, err
}
func (d *daemon) scheduledDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.scheduledWrite(ctx, raw, false)
}
func (d *daemon) scheduledSendNow(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.scheduledWrite(ctx, raw, true)
}
func (d *daemon) scheduledWrite(ctx context.Context, raw json.RawMessage, send bool) (any, error) {
	var p rpc.ScheduledParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := validWriteIDs(p.IDs); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.ScheduledResult{ChatID: id, DryRun: true}, nil
	}
	var res rpc.ScheduledResult
	if send {
		res, err = d.opts.Telegram.ScheduledSendNow(ctx, ref, p.IDs)
	} else {
		res, err = d.opts.Telegram.ScheduledDelete(ctx, ref, p.IDs)
	}
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	if send {
		complete := make([]rpc.Message, 0, len(res.Messages))
		for _, message := range res.Messages {
			if message.Date != "" {
				complete = append(complete, message)
			}
		}
		d.saveMessages(ctx, rpc.MethodMessageScheduledSendNow, complete)
	}
	return res, nil
}
