package daemon

import (
	"context"
	"encoding/json"
	"time"

	"tg-cli/internal/rpc"
)

// chatFullStart queues the full-data job. A chat argument narrows the job to that
// chat; without it every allowed group and channel without a complete read is
// taken, or with refresh every allowed group and channel.
func (d *daemon) chatFullStart(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatFullStartParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if d.full == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "the full-data job is not available")
	}
	return d.full.start(ctx, p.Chat, p.Refresh)
}

// chatFullStatus reports the job of this daemon run. It never calls Telegram.
func (d *daemon) chatFullStatus(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatFullStatusParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if d.full == nil {
		return rpc.ChatFullJob{State: rpc.ChatFullIdle, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	return d.full.status(), nil
}

// chatFullCancel stops the job after the chat it is reading.
func (d *daemon) chatFullCancel(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatFullCancelParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if d.full == nil {
		return rpc.ChatFullJob{State: rpc.ChatFullIdle, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	return d.full.cancelJob(), nil
}
