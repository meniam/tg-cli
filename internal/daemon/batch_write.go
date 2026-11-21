package daemon

import (
	"context"
	"errors"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func stopBatch(err error) bool {
	var e *rpc.Error
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &e) && (e.Code == rpc.CodeFloodWait || e.Code == rpc.CodeNotAuthorized))
}

func addBatchErrors(batch *rpc.Batch, ids []int, err error) {
	for _, id := range ids {
		batch.Errors = append(batch.Errors, rpc.ItemErrorFrom(id, err))
	}
}

func (d *daemon) deleteChunks(ctx context.Context, ref chatref.Ref, chatID, accountID int64, p rpc.DeleteParams) (any, error) {
	result := rpc.DeleteResult{ChatID: chatID, Deleted: []int{}}
	var first, stopped error
	for _, ids := range rpc.Chunks(p.IDs, 100) {
		if stopped != nil {
			addBatchErrors(&result.Batch, ids, stopped)
			continue
		}
		rec := deletionRecord{Time: time.Now().UTC().Format(time.RFC3339), Account: accountID, Tool: "tg", Phase: "intent", ChatID: chatID, IDs: ids, Revoke: p.Revoke, Outcome: "pending"}
		if err := d.writeDeletion(rec); err != nil {
			stopped = errors.New("record deletion intent failed")
			if first == nil {
				first = stopped
			}
			addBatchErrors(&result.Batch, ids, stopped)
			continue
		}
		part := p
		part.IDs = ids
		releaseAccess, err := d.lockChatWrite(chatID)
		if err == nil {
			_, err = d.telegram(ctx).Delete(ctx, ref, part)
			releaseAccess()
		} else {
			stopped = err
		}
		rec.Phase = "result"
		rec.Time = time.Now().UTC().Format(time.RFC3339)
		rec.Outcome = writeOutcome(err)
		if journalErr := d.writeDeletion(rec); journalErr != nil {
			d.log.Error("deletion result journal failed")
		}
		if err != nil {
			if first == nil {
				first = err
			}
			addBatchErrors(&result.Batch, ids, err)
			if stopBatch(err) {
				stopped = err
			}
			continue
		}
		result.Deleted = append(result.Deleted, ids...)
		if _, err := d.store.MarkMessagesDeleted(ctx, chatID, ids); err != nil {
			d.log.Error("deleted message storage failed")
		}
	}
	if len(result.Deleted) == 0 && first != nil {
		return nil, first
	}
	result.Partial = len(result.Errors) > 0
	return result, nil
}
