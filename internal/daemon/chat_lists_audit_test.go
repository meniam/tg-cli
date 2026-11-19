package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// TestAudit04_ChatListsPassParametersToTelegram checks what the handlers hand to
// Telegram: the defaults of the limit and the values the caller chose.
func TestAudit04_ChatListsPassParametersToTelegram(t *testing.T) {
	ctx := context.Background()
	d, tg, _, _ := listTestDaemon(t)
	tg.resolved = &rpc.Chat{ID: -100, Type: "group", Title: "Open"}

	if _, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100"})); err != nil {
		t.Fatal(err)
	}
	if got := tg.membersParams; got.Limit != 50 || got.Offset != 0 || got.Filter != "" {
		t.Errorf("members defaults = %+v, want limit 50", got)
	}
	if _, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100", Limit: 7, Offset: 3, Filter: "admins"})); err != nil {
		t.Fatal(err)
	}
	if got := tg.membersParams; got.Limit != 7 || got.Offset != 3 || got.Filter != "admins" {
		t.Errorf("members params = %+v, want limit 7, offset 3, filter admins", got)
	}

	if _, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "-100"})); err != nil {
		t.Fatal(err)
	}
	if got := tg.topicsParams; got.Limit != 50 {
		t.Errorf("topics defaults = %+v, want limit 50", got)
	}
	if _, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "-100", Limit: 9})); err != nil {
		t.Fatal(err)
	}
	if got := tg.topicsParams; got.Limit != 9 {
		t.Errorf("topics params = %+v, want limit 9", got)
	}

	tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice"}
	if _, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42"})); err != nil {
		t.Fatal(err)
	}
	if got := tg.commonParams; got.Limit != 50 {
		t.Errorf("common defaults = %+v, want limit 50", got)
	}
	if _, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42", Limit: 4})); err != nil {
		t.Fatal(err)
	}
	if got := tg.commonParams; got.Limit != 4 {
		t.Errorf("common params = %+v, want limit 4", got)
	}
}

// TestAudit04_ChatListsPassTelegramErrors checks that an error of Telegram reaches
// the caller unchanged and that nothing is saved from a failed read.
func TestAudit04_ChatListsPassTelegramErrors(t *testing.T) {
	ctx := context.Background()
	want := rpc.NewError(rpc.CodeTelegram, "CHANNEL_PRIVATE")
	for _, tc := range []struct {
		name string
		call func(d *daemon) error
	}{
		{"members", func(d *daemon) error {
			_, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100"}))
			return err
		}},
		{"topics", func(d *daemon) error {
			_, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "-100"}))
			return err
		}},
		{"common", func(d *daemon) error {
			_, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42"}))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, db, _ := listTestDaemon(t)
			tg.err = want
			if tc.name == "common" {
				tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice"}
			} else {
				tg.resolved = &rpc.Chat{ID: -100, Type: "group", Title: "Open"}
				tg.members = rpc.MembersResult{Members: []rpc.Member{{ID: 21, Name: "Alice"}}}
			}
			sameRPCError(t, tc.call(d), want)
			if _, err := db.GetPerson(ctx, 21); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("a failed read saved a person: %v", err)
			}
		})
	}
}

// TestAudit04_ChatListsCheckAccessAfterTheAnswer closes the chat in access.json
// while Telegram answers: the answer is dropped and nothing is saved.
func TestAudit04_ChatListsCheckAccessAfterTheAnswer(t *testing.T) {
	ctx := context.Background()
	t.Run("members", func(t *testing.T) {
		d, tg, db, path := listTestDaemon(t)
		tg.resolved = &rpc.Chat{ID: -100, Type: "group", Title: "Open"}
		tg.members = rpc.MembersResult{Members: []rpc.Member{{ID: 21, Name: "Alice"}}}
		tg.during = func() { audit04WriteAccess(t, path, `{"block":[-100]}`, time.Hour) }
		got, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100"}))
		sameRPCError(t, err, rpc.ErrUnknownChatID())
		if got != nil {
			t.Errorf("a closed chat returned %+v", got)
		}
		if _, err := db.GetPerson(ctx, 21); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a person of a closed chat was saved: %v", err)
		}
	})
	t.Run("topics", func(t *testing.T) {
		d, tg, _, path := listTestDaemon(t)
		tg.resolved = &rpc.Chat{ID: -100, Type: "group", Title: "Open"}
		tg.topics = rpc.TopicsResult{Topics: []rpc.Topic{{ID: 1, Title: "General"}}}
		tg.during = func() { audit04WriteAccess(t, path, `{"block":[-100]}`, time.Hour) }
		got, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "-100"}))
		sameRPCError(t, err, rpc.ErrUnknownChatID())
		if got != nil {
			t.Errorf("a closed chat returned %+v", got)
		}
	})
	t.Run("common", func(t *testing.T) {
		d, tg, db, path := listTestDaemon(t)
		tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice"}
		tg.pages = map[string]rpc.CommonChatsResult{"": {Chats: []rpc.CommonChat{{ID: -101, Type: "group", Title: "Shared"}}}}
		tg.during = func() { audit04WriteAccess(t, path, `{"block":[42]}`, time.Hour) }
		got, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42"}))
		sameRPCError(t, err, rpc.ErrChatNotFound())
		if got != nil {
			t.Errorf("a closed user returned %+v", got)
		}
		if _, err := db.GetChat(ctx, -101); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a shared chat of a closed user was saved: %v", err)
		}
	})
}
