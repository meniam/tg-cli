package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type batchTelegram struct {
	*writeTelegram
	deletes   int
	forwards  int
	scheduled int
}

func (f *batchTelegram) ScheduledDelete(_ context.Context, _ chatref.Ref, ids []int) (rpc.ScheduledResult, error) {
	f.scheduled++
	if f.scheduled == 2 {
		return rpc.ScheduledResult{}, rpc.NewError(rpc.CodeFloodWait, "wait")
	}
	return rpc.ScheduledResult{Deleted: ids}, nil
}

func (f *batchTelegram) Delete(_ context.Context, _ chatref.Ref, p rpc.DeleteParams) (rpc.DeleteResult, error) {
	f.deletes++
	if f.deletes == 2 {
		return rpc.DeleteResult{}, &rpc.Error{Code: rpc.CodeFloodWait, Message: "wait", Data: map[string]any{"retry_after_seconds": 7}}
	}
	return rpc.DeleteResult{Deleted: p.IDs}, nil
}
func (f *batchTelegram) Forward(_ context.Context, _, _ chatref.Ref, p rpc.ForwardParams) (rpc.ForwardResult, error) {
	f.forwards++
	return rpc.ForwardResult{Batch: rpc.Batch{Partial: true, Errors: []rpc.ItemError{{ID: p.IDs[1], Code: "not_found", Message: "missing"}}}, Messages: []rpc.ForwardedMessage{{ID: 17, SourceID: p.IDs[0], Date: "2026-01-01T00:00:00Z"}}}, nil
}
func TestBatchDeleteFloodAndJournal(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[-1000000000010]}`)
	f := &batchTelegram{writeTelegram: base}
	d.opts.Telegram = f
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	raw := params(t, rpc.DeleteParams{Chat: "-1000000000010", IDs: ids, Revoke: true})
	got, err := d.messageDelete(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	res := got.(rpc.DeleteResult)
	if !res.Partial || len(res.Deleted) != 100 || len(res.Errors) != 150 || f.deletes != 2 || res.Errors[149].RetryAfterSeconds != 7 {
		t.Fatalf("%+v calls=%d", res, f.deletes)
	}
	journal, err := os.ReadFile(d.opts.Paths.Deletions)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(journal), "\n") != 4 {
		t.Fatalf("journal=%s", journal)
	}
}
func TestBatchForwardReplay(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	f := &batchTelegram{writeTelegram: base}
	d.opts.Telegram = f
	raw, err := json.Marshal(rpc.ForwardParams{From: "42", To: "42", IDs: []int{1, 2}, IdempotencyKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.messageForward(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.messageForward(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !first.(rpc.ForwardResult).Partial || !second.(rpc.ForwardResult).Partial || !second.(rpc.ForwardResult).Replayed || f.forwards != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, f.forwards)
	}
}

func TestScheduledBatchStopsOnFlood(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	f := &batchTelegram{writeTelegram: base}
	d.opts.Telegram = f
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	got, err := d.scheduledDelete(context.Background(), params(t, rpc.ScheduledParams{Chat: "42", IDs: ids}))
	if err != nil {
		t.Fatal(err)
	}
	res := got.(rpc.ScheduledResult)
	if !res.Partial || len(res.Deleted) != 100 || len(res.Errors) != 150 || f.scheduled != 2 {
		t.Fatalf("result=%+v calls=%d", res, f.scheduled)
	}
}

func TestBatchDeleteStopsAfterIntentFailure(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[-1000000000010]}`)
	base.onDelete = func() { d.opts.Paths.Deletions = filepath.Join(t.TempDir(), "missing", "journal") }
	d.opts.Telegram = base
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	got, err := d.messageDelete(context.Background(), params(t, rpc.DeleteParams{Chat: "-1000000000010", IDs: ids, Revoke: true}))
	if err != nil {
		t.Fatal(err)
	}
	res := got.(rpc.DeleteResult)
	if !res.Partial || len(res.Deleted) != 100 || len(res.Errors) != 150 || len(base.calls) != 1 || res.Errors[0].Code != "failure" {
		t.Fatalf("result=%+v calls=%v", res, base.calls)
	}
}

func TestForwardCopyChangesIdempotency(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	f := &batchTelegram{writeTelegram: base}
	d.opts.Telegram = f
	p := rpc.ForwardParams{From: "42", To: "42", IDs: []int{1, 2}, IdempotencyKey: "copy-key"}
	if _, err := d.messageForward(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	p.Copy = true
	_, err := d.messageForward(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeInvalidParams || f.forwards != 1 {
		t.Fatalf("copy with same key: %v calls=%d", err, f.forwards)
	}
	p.NoCaptions = true
	p.Copy = false
	_, err = d.messageForward(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("captions without copy: %v", err)
	}
}

func TestDeleteBatchStopsAfterAcknowledgedRevocation(t *testing.T) {
	d, tg, db, _ := writeDaemon(t)
	d.opts.Access = nil
	ctx := context.Background()
	if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`)}); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	finished := make(chan struct{})
	tg.onDelete = func() {
		tg.onDelete = nil
		if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[]`)}); err != nil {
			t.Error(err)
		}
		go func() { defer close(finished); d.refreshAccess(ctx) }()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-finished:
				return
			case <-deadline:
				t.Error("revocation did not reach policy lock")
				return
			default:
			}
			if !d.accessMu.TryRLock() {
				return
			}
			d.accessMu.RUnlock()
			runtime.Gosched()
		}
	}
	ids := make([]int, 101)
	for i := range ids {
		ids[i] = i + 1
	}
	out, err := d.deleteChunks(ctx, chatref.Ref{Kind: chatref.ID, ID: 42}, 42, 1, rpc.DeleteParams{IDs: ids, Revoke: true})
	<-finished
	if err != nil {
		t.Fatal(err)
	}
	res := out.(rpc.DeleteResult)
	if len(tg.calls) != 1 || !res.Partial || len(res.Deleted) != 100 || len(res.Errors) != 1 {
		t.Fatalf("batch after revoke: calls=%v result=%+v", tg.calls, res)
	}
}

type scheduledRevocationTelegram struct {
	*writeTelegram
	calls  int
	revoke func()
}

func (f *scheduledRevocationTelegram) ScheduledDelete(_ context.Context, _ chatref.Ref, ids []int) (rpc.ScheduledResult, error) {
	f.calls++
	if f.calls == 1 {
		f.revoke()
	}
	return rpc.ScheduledResult{Deleted: ids}, nil
}
func (f *scheduledRevocationTelegram) ScheduledSendNow(_ context.Context, _ chatref.Ref, ids []int) (rpc.ScheduledResult, error) {
	f.calls++
	if f.calls == 1 {
		f.revoke()
	}
	return rpc.ScheduledResult{}, nil
}

func TestScheduledBatchStopsAfterAcknowledgedRevocation(t *testing.T) {
	for _, send := range []bool{false, true} {
		t.Run(fmt.Sprintf("send=%t", send), func(t *testing.T) {
			d, base, db, _ := writeDaemon(t)
			d.opts.Access = nil
			ctx := context.Background()
			if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`)}); err != nil {
				t.Fatal(err)
			}
			d.refreshAccess(ctx)
			finished := make(chan struct{})
			fake := &scheduledRevocationTelegram{writeTelegram: base}
			d.opts.Telegram = fake
			fake.revoke = func() {
				if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[]`)}); err != nil {
					t.Error(err)
				}
				go func() { defer close(finished); d.refreshAccess(ctx) }()
				deadline := time.After(5 * time.Second)
				for {
					select {
					case <-finished:
						return
					case <-deadline:
						t.Error("revocation did not reach policy lock")
						return
					default:
					}
					if !d.accessMu.TryRLock() {
						return
					}
					d.accessMu.RUnlock()
					runtime.Gosched()
				}
			}
			ids := make([]int, 101)
			for i := range ids {
				ids[i] = i + 1
			}
			out, err := d.scheduledWrite(ctx, params(t, rpc.ScheduledParams{Chat: "42", IDs: ids}), send)
			<-finished
			if err != nil {
				t.Fatal(err)
			}
			res := out.(rpc.ScheduledResult)
			if fake.calls != 1 || !res.Partial || len(res.Errors) != 1 {
				t.Fatalf("scheduled after revoke: calls=%d result=%+v", fake.calls, res)
			}
		})
	}
}
