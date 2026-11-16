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
	res, err := d.telegram(ctx).ScheduledList(ctx, ref)
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
	res := rpc.ScheduledResult{ChatID: id}
	if send {
		res.Messages = []rpc.Message{}
	}
	var first, stopped error
	succeeded := 0
	for _, ids := range rpc.Chunks(p.IDs, 100) {
		if stopped != nil {
			addBatchErrors(&res.Batch, ids, stopped)
			continue
		}
		var part rpc.ScheduledResult
		if send {
			part, err = d.telegram(ctx).ScheduledSendNow(ctx, ref, ids)
		} else {
			part, err = d.telegram(ctx).ScheduledDelete(ctx, ref, ids)
		}
		if err != nil {
			if first == nil {
				first = err
			}
			addBatchErrors(&res.Batch, ids, err)
			if stopBatch(err) {
				stopped = err
			}
			continue
		}
		succeeded += len(ids)
		res.Messages = append(res.Messages, part.Messages...)
		res.Deleted = append(res.Deleted, part.Deleted...)
	}
	if succeeded == 0 && first != nil {
		return nil, first
	}
	res.Partial = len(res.Errors) != 0
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
