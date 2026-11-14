package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type writeTelegram struct {
	*fakeTelegram
	calls      []string
	callErr    error
	onDelete   func()
	checkCalls int
	checkErr   error
	check      rpc.TextCheck
	sendResult rpc.SendResult
	editResult rpc.SendResult
	lastSend   rpc.SendParams
	lastEdit   rpc.EditParams
}

func (f *writeTelegram) CheckText(_ context.Context, p rpc.SendParams) (rpc.TextCheck, error) {
	f.checkCalls++
	if f.checkErr != nil {
		return rpc.TextCheck{}, f.checkErr
	}
	if f.check.Mode != "" {
		return f.check, nil
	}
	return rpc.TextCheck{Mode: "plain", PlainLength: len([]rune(p.Text))}, nil
}

func (f *writeTelegram) Send(_ context.Context, _ chatref.Ref, p rpc.SendParams) (rpc.SendResult, error) {
	f.calls = append(f.calls, "send")
	f.lastSend = p
	if f.sendResult.ID != 0 {
		return f.sendResult, f.callErr
	}
	return rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z"}, f.callErr
}
func (f *writeTelegram) Edit(_ context.Context, _ chatref.Ref, p rpc.EditParams) (rpc.SendResult, error) {
	f.calls = append(f.calls, "edit")
	f.lastEdit = p
	if f.editResult.ID != 0 {
		return f.editResult, f.callErr
	}
	return rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z"}, f.callErr
}
func (f *writeTelegram) Delete(_ context.Context, _ chatref.Ref, p rpc.DeleteParams) (rpc.DeleteResult, error) {
	f.calls = append(f.calls, "delete")
	if f.onDelete != nil {
		f.onDelete()
	}
	return rpc.DeleteResult{Deleted: append([]int(nil), p.IDs...)}, f.callErr
}
func (f *writeTelegram) Forward(_ context.Context, _, _ chatref.Ref, _ rpc.ForwardParams) (rpc.ForwardResult, error) {
	f.calls = append(f.calls, "forward")
	return rpc.ForwardResult{Messages: []rpc.ForwardedMessage{{ID: 12, Date: "2026-11-14T10:00:00Z"}}}, f.callErr
}

func writeDaemon(t *testing.T) (*daemon, *writeTelegram, *store.DB, string) {
	t.Helper()
	d, f, db, path := accessTestDaemon(t)
	wt := &writeTelegram{fakeTelegram: f}
	d.opts.Telegram = wt
	return d, wt, db, path
}
func writeCode(t *testing.T, err error) int {
	t.Helper()
	var e *rpc.Error
	if !errors.As(err, &e) {
		t.Fatalf("not rpc error: %v", err)
	}
	return e.Code
}
func storedMessage(t *testing.T, db *store.DB, chat int64, id int, text string) {
	t.Helper()
	if err := db.UpsertMessages(context.Background(), []store.Message{{ChatID: chat, ID: id, Date: 1700000000, Text: text, Raw: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteDisabledAndAccessVisibility(t *testing.T) {
	d, tg, _, path := writeDaemon(t)
	ctx := context.Background()
	checks := []struct {
		name   string
		method func() (any, error)
	}{
		{"send", func() (any, error) { return d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "hi"})) }},
		{"edit", func() (any, error) {
			return d.messageEdit(ctx, params(t, rpc.EditParams{Chat: "42", ID: 1, Text: "hi"}))
		}},
		{"delete", func() (any, error) {
			return d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{1}, Revoke: true}))
		}},
		{"forward", func() (any, error) {
			return d.messageForward(ctx, params(t, rpc.ForwardParams{From: "42", To: "42", IDs: []int{1}}))
		}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.method()
			if code := writeCode(t, err); code != rpc.CodeWriteDisabled {
				t.Fatalf("code=%d", code)
			}
		})
	}
	writeAccess(t, path, `{"allow":[],"write":[]}`)
	_, err := d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "hi"}))
	if writeCode(t, err) != rpc.CodeWriteDisabled || len(tg.calls) != 0 {
		t.Fatalf("no write list: %v calls=%v", err, tg.calls)
	}
	writeAccess(t, path, `{"allow":[1],"write":[42]}`)
	_, err = d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "hi"}))
	_, readErr := d.chatResolve(ctx, params(t, rpc.ChatRefParams{Chat: "42"}))
	sameRPCError(t, err, readErr)
	if len(tg.calls) != 0 {
		t.Fatalf("closed chat wrote: %v", tg.calls)
	}
	writeAccess(t, path, `{"block":[42],"write":[42]}`)
	_, err = d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "hi"}))
	sameRPCError(t, err, readErr)
	writeAccess(t, path, `{"allow":[1],"write":[42]}`)
	tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Hidden"}
	_, err = d.messageSend(ctx, params(t, rpc.SendParams{Chat: "@hiddenuser", Text: "hi"}))
	sameRPCError(t, err, rpc.ErrChatNotFound())
	tg.missing = true
	_, missing := d.messageSend(ctx, params(t, rpc.SendParams{Chat: "@missinguser", Text: "hi"}))
	sameRPCError(t, err, missing)
}

func TestWriteDryRunAndPersistence(t *testing.T) {
	d, tg, db, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	ctx := context.Background()
	storedMessage(t, db, 42, 8, "prior")
	for _, call := range []struct {
		name string
		run  func() (any, error)
	}{
		{"send", func() (any, error) {
			return d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "dry text", DryRun: true}))
		}},
		{"edit", func() (any, error) {
			return d.messageEdit(ctx, params(t, rpc.EditParams{Chat: "42", ID: 8, Text: "dry edit", DryRun: true}))
		}},
		{"delete", func() (any, error) {
			return d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{8}, Revoke: true, DryRun: true}))
		}},
		{"forward", func() (any, error) {
			return d.messageForward(ctx, params(t, rpc.ForwardParams{From: "42", To: "42", IDs: []int{8}, DryRun: true}))
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			result, err := call.run()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(result)
			if !bytes.Contains(raw, []byte(`"dry_run":true`)) || bytes.Contains(raw, []byte("dry text")) || bytes.Contains(raw, []byte("dry edit")) {
				t.Fatalf("dry run: %s", raw)
			}
		})
	}
	if len(tg.calls) != 0 {
		t.Fatalf("dry run wrote: %v", tg.calls)
	}
	if _, err := os.Stat(d.opts.Paths.Deletions); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run journal: %v", err)
	}
	if _, err := db.GetMessage(ctx, 42, 11); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dry run saved message: %v", err)
	}
	result, err := d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "saved text"}))
	if err != nil || result.(rpc.SendResult).ID != 11 {
		t.Fatalf("send: %+v %v", result, err)
	}
	got, err := db.GetMessage(ctx, 42, 11)
	if err != nil || got.Text != "saved text" || got.Out == nil || !*got.Out {
		t.Fatalf("saved: %+v %v", got, err)
	}
	_, err = d.messageEdit(ctx, params(t, rpc.EditParams{Chat: "42", ID: 11, Text: "edited text"}))
	if err != nil {
		t.Fatal(err)
	}
	got, err = db.GetMessage(ctx, 42, 11)
	if err != nil || got.Text != "edited text" || got.Out == nil || !*got.Out {
		t.Fatalf("edited: %+v %v", got, err)
	}
	versions, err := db.MessageVersions(ctx, 42, 11)
	if err != nil || len(versions) != 1 || versions[0].Text != "saved text" {
		t.Fatalf("versions: %+v %v", versions, err)
	}
	_, err = d.messageForward(ctx, params(t, rpc.ForwardParams{From: "42", To: "42", IDs: []int{11}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMessage(ctx, 42, 12); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("forward was stored: %v", err)
	}
	tg.callErr = rpc.NewError(rpc.CodeFloodWait, "FLOOD_WAIT")
	_, err = d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "not saved"}))
	if writeCode(t, err) != rpc.CodeFloodWait {
		t.Fatal(err)
	}
	got, err = db.GetMessage(ctx, 42, 11)
	if err != nil || got.Text != "edited text" {
		t.Fatalf("telegram error changed message: %+v %v", got, err)
	}
}

func TestWriteDeleteOwnershipJournalAndFlood(t *testing.T) {
	d, tg, db, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42,-1000000000010]}`)
	ctx := context.Background()
	storedMessage(t, db, 42, 7, "private")
	storedMessage(t, db, 77, 8, "foreign")
	for _, id := range []int{8, 999} {
		_, err := d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{id}, Revoke: true}))
		if writeCode(t, err) != rpc.CodeNotFound {
			t.Fatalf("id=%d: %v", id, err)
		}
	}
	if len(tg.calls) != 0 {
		t.Fatalf("foreign id deleted: %v", tg.calls)
	}
	tg.onDelete = func() {
		raw, err := os.ReadFile(d.opts.Paths.Deletions)
		if err != nil || len(bytes.Split(bytes.TrimSpace(raw), []byte("\n"))) != 1 {
			t.Errorf("intent not durable before Telegram: %s %v", raw, err)
		}
	}
	result, err := d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{7}, Revoke: true}))
	if err != nil || len(result.(rpc.DeleteResult).Deleted) != 1 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	raw, err := os.ReadFile(d.opts.Paths.Deletions)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(d.opts.Paths.Deletions)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode: %v %v", info, err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != 2 || bytes.Contains(raw, []byte("private")) {
		t.Fatalf("journal: %s", raw)
	}
	var first, second deletionRecord
	if json.Unmarshal(lines[0], &first) != nil || json.Unmarshal(lines[1], &second) != nil || first.Phase != "intent" || second.Phase != "result" || first.Account != 42 || first.Tool != "tg" || second.Outcome != "ok" || first.ChatID != 42 || first.IDs[0] != 7 || !first.Revoke {
		t.Fatalf("records: %+v %+v", first, second)
	}
	if _, err := time.Parse(time.RFC3339, first.Time); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetMessage(ctx, 42, 7)
	if err != nil || saved.DeletedAt == nil {
		t.Fatalf("delete marker: %+v %v", saved, err)
	}
	tg.onDelete = nil
	d.opts.Paths.Deletions = filepath.Join(t.TempDir(), "missing", "deletions.jsonl")
	storedMessage(t, db, 42, 9, "protected")
	_, err = d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{9}, Revoke: true}))
	if err == nil || len(tg.calls) != 1 {
		t.Fatalf("intent failure allowed delete: %v calls=%v", err, tg.calls)
	}
	d.opts.Paths.Deletions = filepath.Join(t.TempDir(), "deletions.jsonl")
	tg.callErr = rpc.NewError(rpc.CodeFloodWait, "FLOOD_WAIT")
	_, err = d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{9}, Revoke: true}))
	if writeCode(t, err) != rpc.CodeFloodWait || len(tg.calls) != 2 {
		t.Fatalf("flood: %v calls=%v", err, tg.calls)
	}
	raw, _ = os.ReadFile(d.opts.Paths.Deletions)
	if !strings.Contains(string(raw), `"outcome":"flood_wait"`) {
		t.Fatalf("flood journal: %s", raw)
	}
	saved, err = db.GetMessage(ctx, 42, 9)
	if err != nil || saved.DeletedAt != nil {
		t.Fatalf("flood deleted locally: %+v %v", saved, err)
	}
	tg.callErr = nil
	_, err = d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "-1000000000010", IDs: []int{99}, Revoke: true}))
	if err != nil || len(tg.calls) != 3 {
		t.Fatalf("channel delete: %v calls=%v", err, tg.calls)
	}
	_, err = d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "-1000000000010", IDs: []int{99}, ForMe: true}))
	if writeCode(t, err) != rpc.CodeInvalidParams || len(tg.calls) != 3 {
		t.Fatalf("channel for me: %v calls=%v", err, tg.calls)
	}
}

func TestWriteForwardPermissionsAndInvalidParams(t *testing.T) {
	d, tg, _, path := writeDaemon(t)
	ctx := context.Background()
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	_, err := d.messageForward(ctx, params(t, rpc.ForwardParams{From: "77", To: "42", IDs: []int{1}}))
	if writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed source: %v", err)
	}
	writeAccess(t, path, `{"allow":[42,77],"write":[42]}`)
	_, err = d.messageForward(ctx, params(t, rpc.ForwardParams{From: "42", To: "77", IDs: []int{1}}))
	if writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("unwritable destination: %v", err)
	}
	for _, input := range []struct{ method func() (any, error) }{
		{func() (any, error) { return d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: ""})) }},
		{func() (any, error) {
			return d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: strings.Repeat("x", 4097)}))
		}},
		{func() (any, error) {
			return d.messageEdit(ctx, params(t, rpc.EditParams{Chat: "42", ID: 0, Text: "x"}))
		}},
		{func() (any, error) {
			return d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{0}, Revoke: true}))
		}},
		{func() (any, error) {
			return d.messageDelete(ctx, params(t, rpc.DeleteParams{Chat: "42", IDs: []int{1}}))
		}},
		{func() (any, error) {
			return d.messageForward(ctx, params(t, rpc.ForwardParams{From: "42", To: "42", IDs: []int{-1}}))
		}},
	} {
		_, err := input.method()
		if writeCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("invalid params: %v", err)
		}
	}
	if len(tg.calls) != 0 {
		t.Fatalf("invalid request wrote: %v", tg.calls)
	}
}

func TestDeletionResultJournalFailureKeepsTelegramOutcome(t *testing.T) {
	d, tg, db, path := writeDaemon(t)
	var journal bytes.Buffer
	d.log = slog.New(slog.NewTextHandler(&journal, nil))
	writeAccess(t, path, `{"write":[42]}`)
	storedMessage(t, db, 42, 19, "owned")
	tg.onDelete = func() { d.opts.Paths.Deletions = filepath.Join(t.TempDir(), "missing", "deletions.jsonl") }
	result, err := d.messageDelete(context.Background(), params(t, rpc.DeleteParams{Chat: "42", IDs: []int{19}, Revoke: true}))
	if err != nil || result.(rpc.DeleteResult).Deleted[0] != 19 || len(tg.calls) != 1 {
		t.Fatalf("result journal failure: %+v %v calls=%v", result, err, tg.calls)
	}
	if !strings.Contains(journal.String(), "deletion result journal failed") {
		t.Fatalf("missing journal error log: %s", journal.String())
	}
}

func TestWriteCountAppearsInStatusWithoutReadFilter(t *testing.T) {
	d, tg, _, path := writeDaemon(t)
	d.login = newLoginManager(context.Background(), tg)
	writeAccess(t, path, `{"write":[42]}`)
	result, err := d.status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := result.(rpc.StatusResult)
	if st.Access == nil || st.Access.Enabled || st.Access.Write != 1 {
		t.Fatalf("write-only status: %+v", st.Access)
	}
}
