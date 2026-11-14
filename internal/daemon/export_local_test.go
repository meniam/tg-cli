package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type exportTelegram struct {
	*fakeTelegram
	calls atomic.Int64
}

func (f *exportTelegram) Self(context.Context) (*rpc.User, error) {
	f.calls.Add(1)
	return nil, nil
}
func (f *exportTelegram) Resolve(context.Context, chatref.Ref) (rpc.Chat, error) {
	f.calls.Add(1)
	return rpc.Chat{}, errors.New("unexpected Telegram resolve")
}
func (f *exportTelegram) History(context.Context, chatref.Ref, rpc.HistoryQuery) (rpc.MessagesResult, error) {
	f.calls.Add(1)
	return rpc.MessagesResult{}, errors.New("unexpected Telegram history")
}
func (f *exportTelegram) HistoryPage(context.Context, chatref.Ref, int) (rpc.HistoryPage, error) {
	f.calls.Add(1)
	return rpc.HistoryPage{}, errors.New("unexpected Telegram history page")
}

func exportTestDaemon(t *testing.T) (*daemon, *store.DB, string) {
	t.Helper()
	d, fake, db, path := accessTestDaemon(t)
	tg := &exportTelegram{fakeTelegram: fake}
	d.opts.Telegram = tg
	t.Cleanup(func() {
		if calls := tg.calls.Load(); calls != 0 {
			t.Errorf("export called Telegram %d times", calls)
		}
	})
	return d, db, path
}

func saveExportMessages(t *testing.T, db *store.DB, chatID int64, ids ...int) {
	t.Helper()
	messages := make([]store.Message, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, store.Message{
			ChatID: chatID, ID: id, Date: int64(1700000000 + id),
			Text: "item", Raw: []byte(`{}`),
		})
	}
	if err := db.UpsertMessages(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
}

func exportPage(t *testing.T, d *daemon, p rpc.ExportParams) rpc.ExportPage {
	t.Helper()
	result, err := d.messageExportLocal(context.Background(), params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	return result.(rpc.ExportPage)
}

func TestExportLocalThreePagesWithoutTelegram(t *testing.T) {
	d, db, _ := exportTestDaemon(t)
	saveExportMessages(t, db, 42, 5, 1, 4, 2, 3)
	first, err := db.GetMessage(context.Background(), 42, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.GetMessage(context.Background(), 42, 2)
	if err != nil {
		t.Fatal(err)
	}
	two, _ := json.Marshal(rpc.ExportPage{Messages: []rpc.ExportMessage{
		{Message: rpcStoredMessage(first)}, {Message: rpcStoredMessage(second)},
	}, NextAfter: 2})
	budget := len(two)
	var ids []int
	after := 0
	pages := 0
	for {
		result, err := d.exportLocalPage(context.Background(), params(t, rpc.ExportParams{Chat: "42", After: after}), budget)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if len(encoded) > budget {
			t.Fatalf("page size %d exceeds budget %d", len(encoded), budget)
		}
		pages++
		for _, message := range result.Messages {
			ids = append(ids, message.ID)
		}
		if result.NextAfter == 0 {
			break
		}
		if result.NextAfter <= after {
			t.Fatalf("cursor did not advance: %d after %d", result.NextAfter, after)
		}
		after = result.NextAfter
	}
	if pages != 3 || !slices.Equal(ids, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("pages=%d ids=%v", pages, ids)
	}
}

func TestExportLocalAccessAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chat   string
		access string
		p      rpc.ExportParams
		code   int
	}{
		{"closed numeric", "42", `{"allow":[77]}`, rpc.ExportParams{}, rpc.CodeNotFound},
		{"closed username", "@teamchat", `{"allow":[77]}`, rpc.ExportParams{}, rpc.CodeNotFound},
		{"unknown username", "@unknownname", "", rpc.ExportParams{}, rpc.CodeNotFound},
		{"unknown numeric", "99", "", rpc.ExportParams{}, rpc.CodeNotFound},
		{"invalid since", "42", "", rpc.ExportParams{Since: "bad"}, rpc.CodeInvalidParams},
		{"invalid until", "42", "", rpc.ExportParams{Until: "bad"}, rpc.CodeInvalidParams},
		{"reversed dates", "42", "", rpc.ExportParams{Since: "2026-11-15T00:00:00Z", Until: "2026-11-14T00:00:00Z"}, rpc.CodeInvalidParams},
		{"negative cursor", "42", "", rpc.ExportParams{After: -1}, rpc.CodeInvalidParams},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, db, path := exportTestDaemon(t)
			saveExportMessages(t, db, 42, 1)
			username := "teamchat"
			if err := db.UpsertChats(context.Background(), []store.Chat{{ID: 42, Type: "user", Username: &username}}); err != nil {
				t.Fatal(err)
			}
			if tc.access != "" {
				writeAccess(t, path, tc.access)
			}
			p := tc.p
			p.Chat = tc.chat
			_, err := d.messageExportLocal(context.Background(), params(t, p))
			if rpcCode(t, err) != tc.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExportLocalPolicyChangesBetweenPages(t *testing.T) {
	d, db, path := exportTestDaemon(t)
	saveExportMessages(t, db, 42, 1, 2, 3)
	writeAccess(t, path, `{"allow":[42]}`)
	first, err := db.GetMessage(context.Background(), 42, 1)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := json.Marshal(rpc.ExportPage{Messages: []rpc.ExportMessage{{Message: rpcStoredMessage(first)}}, NextAfter: 1})
	page, err := d.exportLocalPage(context.Background(), params(t, rpc.ExportParams{Chat: "42"}), len(one))
	if err != nil || page.NextAfter != 1 {
		t.Fatalf("first page: %+v err=%v", page, err)
	}
	writeAccess(t, path, `{"allow":[999]}`)
	_, err = d.messageExportLocal(context.Background(), params(t, rpc.ExportParams{Chat: "42", After: page.NextAfter}))
	if rpcCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed second page: %v", err)
	}
}

func TestExportLocalDatesDeletedAndEmptyPage(t *testing.T) {
	d, db, _ := exportTestDaemon(t)
	saveExportMessages(t, db, 42, 1, 2, 3, 4)
	if _, err := db.MarkMessagesDeleted(context.Background(), 42, []int{2}); err != nil {
		t.Fatal(err)
	}
	since := time.Unix(1700000002, 0).UTC().Format(time.RFC3339)
	until := time.Unix(1700000003, 0).UTC().Format(time.RFC3339)
	visible := exportPage(t, d, rpc.ExportParams{Chat: "42", Since: since, Until: until})
	if len(visible.Messages) != 1 || visible.Messages[0].ID != 3 || visible.NextAfter != 0 {
		t.Fatalf("filtered: %+v", visible)
	}
	withDeleted := exportPage(t, d, rpc.ExportParams{Chat: "42", Since: since, Until: until, IncludeDeleted: true})
	if len(withDeleted.Messages) != 2 || withDeleted.Messages[0].ID != 2 || !withDeleted.Messages[0].Deleted || withDeleted.Messages[1].ID != 3 || withDeleted.Messages[1].Deleted {
		t.Fatalf("deleted: %+v", withDeleted)
	}
	empty := exportPage(t, d, rpc.ExportParams{Chat: "42", After: 4})
	if empty.Messages == nil || len(empty.Messages) != 0 || empty.NextAfter != 0 {
		t.Fatalf("empty page: %+v", empty)
	}
	encoded, _ := json.Marshal(empty)
	if string(encoded) != `{"messages":[]}` {
		t.Fatalf("empty JSON: %s", encoded)
	}
}

func TestExportLocalOversizedSingleMessage(t *testing.T) {
	d, db, _ := exportTestDaemon(t)
	message := store.Message{ChatID: 42, ID: 123, Date: 1700000123, Text: strings.Repeat("x", maxPageBytes), Raw: []byte(`{}`)}
	if err := db.UpsertMessages(context.Background(), []store.Message{message}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, err := d.messageExportLocal(context.Background(), params(t, rpc.ExportParams{Chat: "42"}))
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInternal || !strings.Contains(rpcErr.Message, "123") || strings.Contains(rpcErr.Message, "xxx") {
			t.Fatalf("oversized message: %v", err)
		}
	}
}

func TestExportLocalDefaultBudgetFitsRPCFrame(t *testing.T) {
	d, db, _ := exportTestDaemon(t)
	text := strings.Repeat("x", 400<<10)
	messages := []store.Message{
		{ChatID: 42, ID: 1, Date: 1700000001, Text: text, Raw: []byte(`{}`)},
		{ChatID: 42, ID: 2, Date: 1700000002, Text: text, Raw: []byte(`{}`)},
	}
	if err := db.UpsertMessages(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	first := exportPage(t, d, rpc.ExportParams{Chat: "42"})
	if len(first.Messages) != 1 || first.NextAfter != 1 {
		t.Fatalf("first large page: messages=%d next=%d", len(first.Messages), first.NextAfter)
	}
	resultJSON, err := json.Marshal(first)
	if err != nil || len(resultJSON) > maxPageBytes {
		t.Fatalf("result size=%d err=%v", len(resultJSON), err)
	}
	frame, err := json.Marshal(rpc.Response{JSONRPC: rpc.Version, ID: json.RawMessage(`1`), Result: resultJSON})
	if err != nil || len(frame)+1 > rpc.MaxFrameSize {
		t.Fatalf("frame size=%d err=%v", len(frame)+1, err)
	}
	second := exportPage(t, d, rpc.ExportParams{Chat: "42", After: first.NextAfter})
	if len(second.Messages) != 1 || second.Messages[0].ID != 2 || second.NextAfter != 0 {
		t.Fatalf("second large page: messages=%d next=%d", len(second.Messages), second.NextAfter)
	}
}

func TestEventEditPersistsMessageVersionForExport(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	chatID := int64(42)
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &chatID, testMessage(7, chatID, "old text"), "new:42:7"))
	d.handleEvent(testEvent(t, rpc.EventMessageEdit, &chatID, testMessage(7, chatID, "new text"), "edit:42:7"))
	stored, err := db.GetMessage(context.Background(), chatID, 7)
	if err != nil || stored.Text != "new text" {
		t.Fatalf("edited message: %+v err=%v", stored, err)
	}
	versions, err := db.MessageVersions(context.Background(), chatID, 7)
	if err != nil || len(versions) != 1 || versions[0].Text != "old text" {
		t.Fatalf("previous version: %+v err=%v", versions, err)
	}
	page := exportPage(t, d, rpc.ExportParams{Chat: "42"})
	if len(page.Messages) != 1 || page.Messages[0].Text != "new text" || page.Messages[0].Deleted {
		t.Fatalf("edited export: %+v", page)
	}
}

func TestEventDeleteMarksMessageForExport(t *testing.T) {
	d, _, db, _ := accessTestDaemon(t)
	chatID := int64(42)
	d.handleEvent(testEvent(t, rpc.EventMessageNew, &chatID, testMessage(7, chatID, "old text"), "new:42:7"))
	d.handleEvent(testEvent(t, rpc.EventMessageDelete, &chatID, rpc.MessageDeleteData{ChatID: &chatID, IDs: []int{7}}, "delete:42:7"))
	stored, err := db.GetMessage(context.Background(), chatID, 7)
	if err != nil || stored.DeletedAt == nil {
		t.Fatalf("deletion marker: %+v err=%v", stored, err)
	}
	if page := exportPage(t, d, rpc.ExportParams{Chat: "42"}); len(page.Messages) != 0 {
		t.Fatalf("deleted message was visible: %+v", page)
	}
	page := exportPage(t, d, rpc.ExportParams{Chat: "42", IncludeDeleted: true})
	if len(page.Messages) != 1 || page.Messages[0].ID != 7 || !page.Messages[0].Deleted {
		t.Fatalf("deleted export: %+v", page)
	}
}
