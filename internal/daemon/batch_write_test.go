package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
