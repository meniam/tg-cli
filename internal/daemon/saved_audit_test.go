package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type auditSavedFake struct {
	*fakeTelegram
	renames  []rpc.SavedTagRenameParams
	messages []rpc.Message
}

func (f *auditSavedFake) RenameSavedTag(_ context.Context, p rpc.SavedTagRenameParams) error {
	f.renames = append(f.renames, p)
	return nil
}

func (f *auditSavedFake) SavedMessages(context.Context, int64, *chatref.Ref, rpc.SavedMessagesParams, bool) (rpc.MessagesResult, error) {
	return rpc.MessagesResult{Messages: f.messages}, nil
}

func (f *auditSavedFake) SavedList(context.Context, rpc.SavedListParams) (rpc.SavedListResult, error) {
	return rpc.SavedListResult{Dialogs: []rpc.SavedDialog{}}, nil
}

func TestAudit06SavedTagRenameParams(t *testing.T) {
	blank, title := "  ", "tag"
	cases := []struct {
		name string
		p    rpc.SavedTagRenameParams
	}{
		{"no emoji", rpc.SavedTagRenameParams{Title: &title}},
		{"blank emoji", rpc.SavedTagRenameParams{Emoji: " ", Title: &title}},
		{"neither title nor clear", rpc.SavedTagRenameParams{Emoji: "👍"}},
		{"both title and clear", rpc.SavedTagRenameParams{Emoji: "👍", Title: &title, Clear: true}},
		{"blank title", rpc.SavedTagRenameParams{Emoji: "👍", Title: &blank}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, base, _, path := accessTestDaemon(t)
			f := &auditSavedFake{fakeTelegram: base}
			d.opts.Telegram = f
			writeAccess(t, path, `{"write":[42]}`)
			_, err := d.savedTagRename(context.Background(), params(t, tc.p))
			if err == nil || writeCode(t, err) != rpc.CodeInvalidParams || len(f.renames) != 0 {
				t.Fatalf("err = %v, renames = %d", err, len(f.renames))
			}
		})
	}
}

func TestAudit06SavedTagRenameWrites(t *testing.T) {
	title := "tag"
	cases := []struct {
		name  string
		p     rpc.SavedTagRenameParams
		clear bool
	}{
		{"rename", rpc.SavedTagRenameParams{Emoji: "👍", Title: &title}, false},
		{"clear", rpc.SavedTagRenameParams{Emoji: "👍", Clear: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, base, _, path := accessTestDaemon(t)
			f := &auditSavedFake{fakeTelegram: base}
			d.opts.Telegram = f
			writeAccess(t, path, `{"write":[42]}`)
			got, err := d.savedTagRename(context.Background(), params(t, tc.p))
			if err != nil {
				t.Fatal(err)
			}
			result := got.(rpc.SavedTagRenameResult)
			if result.DryRun || result.Emoji != "👍" || result.Cleared != tc.clear || len(f.renames) != 1 || f.renames[0].Emoji != "👍" || f.renames[0].Clear != tc.clear {
				t.Fatalf("result %+v renames %+v", result, f.renames)
			}
			if !tc.clear && (result.Title == nil || *result.Title != "tag") {
				t.Fatalf("title lost: %+v", result)
			}
		})
	}
	t.Run("write permission is checked before the call", func(t *testing.T) {
		d, base, _, path := accessTestDaemon(t)
		f := &auditSavedFake{fakeTelegram: base}
		d.opts.Telegram = f
		writeAccess(t, path, `{}`)
		_, err := d.savedTagRename(context.Background(), params(t, rpc.SavedTagRenameParams{Emoji: "👍", Title: &title}))
		if err == nil || writeCode(t, err) != rpc.CodeWriteDisabled || len(f.renames) != 0 {
			t.Fatalf("err = %v, renames = %d", err, len(f.renames))
		}
	})
}

func TestAudit06SavedHidesUnknownSourceUnderAFilter(t *testing.T) {
	messages := []rpc.Message{
		{ID: 1, ChatID: 42, Date: "2026-01-01T00:00:00Z", Text: "known", SavedSourceID: 77},
		{ID: 2, ChatID: 42, Date: "2026-01-01T00:00:00Z", Text: "unknown", SavedSourceUnknown: true},
	}
	cases := []struct {
		name   string
		access string
		want   []int
	}{
		{"filter on hides the unknown source", `{"block":[88]}`, []int{1}},
		{"no filter shows it", `{}`, []int{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, base, _, path := accessTestDaemon(t)
			d.opts.Telegram = &auditSavedFake{fakeTelegram: base, messages: messages}
			writeAccess(t, path, tc.access)
			got, err := d.savedMessages(context.Background(), params(t, rpc.SavedMessagesParams{Query: "x"}), true)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int
			for _, m := range got.(rpc.MessagesResult).Messages {
				ids = append(ids, m.ID)
			}
			if len(ids) != len(tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("ids = %v, want %v", ids, tc.want)
				}
			}
		})
	}
}

func TestAudit06SavedLimitBounds(t *testing.T) {
	for _, limit := range []int{-1, 101, 1000} {
		d, base, _, _ := accessTestDaemon(t)
		d.opts.Telegram = &auditSavedFake{fakeTelegram: base}
		if _, err := d.savedList(context.Background(), params(t, rpc.SavedListParams{Limit: limit})); err == nil || writeCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("list limit %d: %v", limit, err)
		}
		if _, err := d.savedMessages(context.Background(), params(t, rpc.SavedMessagesParams{Query: "x", Limit: limit}), true); err == nil || writeCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("messages limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int{0, 1, 100} {
		d, base, _, _ := accessTestDaemon(t)
		d.opts.Telegram = &auditSavedFake{fakeTelegram: base}
		if _, err := d.savedList(context.Background(), params(t, rpc.SavedListParams{Limit: limit})); err != nil {
			t.Errorf("list limit %d: %v", limit, err)
		}
		if _, err := d.savedMessages(context.Background(), params(t, rpc.SavedMessagesParams{Query: "x", Limit: limit}), true); err != nil {
			t.Errorf("messages limit %d: %v", limit, err)
		}
	}
}
