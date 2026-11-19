package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// audit04DialogFake records the dialog writes and answers the state refresh with a
// state the test can look for in the database.
type audit04DialogFake struct {
	*fakeTelegram
	archives []bool // the archive flag of each ArchiveDialogs call
	archived [][]chatref.Ref
	pins     []bool // the pin flag of each PinDialog call
	notifies []rpc.NotifyParams
	refreshs int
	state    rpc.ChatState
}

// Resolve echoes the id of the reference, so a request for several chats resolves
// each one to itself.
func (f *audit04DialogFake) Resolve(_ context.Context, ref chatref.Ref) (rpc.Chat, error) {
	return rpc.Chat{ID: ref.ID, Type: "group", Title: "Group"}, nil
}

func (f *audit04DialogFake) ArchiveDialogs(_ context.Context, refs []chatref.Ref, archive bool) error {
	f.archives = append(f.archives, archive)
	f.archived = append(f.archived, refs)
	return nil
}

func (f *audit04DialogFake) PinDialog(_ context.Context, _ chatref.Ref, pin bool) error {
	f.pins = append(f.pins, pin)
	return nil
}

func (f *audit04DialogFake) ChangeNotify(_ context.Context, _ chatref.Ref, p rpc.NotifyParams) (rpc.NotifyResult, error) {
	f.notifies = append(f.notifies, p)
	return rpc.NotifyResult{MuteUntil: p.MuteUntil, Silent: p.Silent, ShowPreviews: p.Previews, DryRun: p.DryRun}, nil
}

func (f *audit04DialogFake) RefreshDialogStates(context.Context, []chatref.Ref) (rpc.ChatStatesResult, error) {
	f.refreshs++
	return rpc.ChatStatesResult{States: []rpc.ChatState{f.state}}, nil
}

func (f *audit04DialogFake) writes() int { return len(f.archives) + len(f.pins) + len(f.notifies) }

func audit04DialogDaemon(t *testing.T) (*daemon, *audit04DialogFake, *store.DB) {
	t.Helper()
	d, base, db, path := accessTestDaemon(t)
	f := &audit04DialogFake{fakeTelegram: base, state: rpc.ChatState{ChatID: 77, Pinned: true, Archived: true}}
	d.opts.Telegram = f
	writeAccess(t, path, `{"dialogs":true}`)
	if err := db.UpsertChats(context.Background(), []store.Chat{{ID: 77, Type: "group", Title: "Group"}, {ID: 78, Type: "group", Title: "Other"}}); err != nil {
		t.Fatal(err)
	}
	return d, f, db
}

// TestAudit04_ChangeDialogActions runs the four dialog actions and checks the
// answer, the call that reaches Telegram and the state saved afterwards.
func TestAudit04_ChangeDialogActions(t *testing.T) {
	for _, tc := range []struct {
		action  string
		chats   []string
		archive bool // the group of the action
		flag    bool // the flag Telegram must receive
	}{
		{"archive", []string{"77", "78"}, true, true},
		{"unarchive", []string{"77", "78"}, true, false},
		{"pin", []string{"77"}, false, true},
		{"unpin", []string{"77"}, false, false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			d, f, db := audit04DialogDaemon(t)
			ctx := context.Background()
			got, err := d.changeDialog(ctx, params(t, rpc.DialogParams{Chats: tc.chats}), tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if tc.archive {
				result := got.(rpc.DialogArchiveResult)
				if len(result.Chats) != len(tc.chats) || result.DryRun {
					t.Fatalf("result = %+v", result)
				}
				for _, item := range result.Chats {
					if item.Archived != tc.flag {
						t.Errorf("chat %d archived=%v, want %v", item.ChatID, item.Archived, tc.flag)
					}
				}
				if len(f.archives) != 1 || f.archives[0] != tc.flag || len(f.archived[0]) != len(tc.chats) || len(f.pins) != 0 {
					t.Errorf("Telegram archives=%v pins=%v", f.archives, f.pins)
				}
			} else {
				result := got.(rpc.DialogPinResult)
				if result.ChatID != 77 || result.Pinned != tc.flag || result.DryRun {
					t.Fatalf("result = %+v", result)
				}
				if len(f.pins) != 1 || f.pins[0] != tc.flag || len(f.archives) != 0 {
					t.Errorf("Telegram archives=%v pins=%v", f.archives, f.pins)
				}
			}
			if f.refreshs != 1 {
				t.Errorf("the dialog states were refreshed %d times, want once", f.refreshs)
			}
			state, err := db.DialogState(ctx, 77, store.ChatFilter{})
			if err != nil || !state.Pinned || !state.Archived {
				t.Errorf("saved state = %+v %v, want the refreshed one", state, err)
			}
		})
	}
}

// TestAudit04_ChangeDialogChatCount covers the limits of the chat list: one to a
// hundred chats to archive, exactly one to pin.
func TestAudit04_ChangeDialogChatCount(t *testing.T) {
	d, f, _ := audit04DialogDaemon(t)
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprint(100 + i)
	}
	for _, tc := range []struct {
		name   string
		action string
		chats  []string
	}{
		{"archive without chats", "archive", nil},
		{"archive 101 chats", "archive", many},
		{"unarchive without chats", "unarchive", []string{}},
		{"pin two chats", "pin", []string{"77", "78"}},
		{"unpin two chats", "unpin", []string{"77", "78"}},
		{"pin without chats", "pin", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.changeDialog(context.Background(), params(t, rpc.DialogParams{Chats: tc.chats}), tc.action)
			if code := rpcCode(t, err); code != rpc.CodeInvalidParams {
				t.Fatalf("code = %d, want %d (%v)", code, rpc.CodeInvalidParams, err)
			}
		})
	}
	if f.writes() != 0 || f.refreshs != 0 {
		t.Errorf("an invalid request reached Telegram: %+v", f)
	}
}

// TestAudit04_ChangeNotifyValidation covers the settings check of the notification
// handlers: a setting is required and a mute time must lie in the future.
func TestAudit04_ChangeNotifyValidation(t *testing.T) {
	d, f, _ := audit04DialogDaemon(t)
	past, negative, overflow := int(time.Now().Add(-time.Hour).Unix()), -1, 2147483648
	for _, tc := range []struct {
		name string
		p    rpc.NotifyParams
	}{
		{"no setting", rpc.NotifyParams{Chat: "77"}},
		{"mute time in the past", rpc.NotifyParams{Chat: "77", MuteUntil: &past}},
		{"negative mute time", rpc.NotifyParams{Chat: "77", MuteUntil: &negative}},
		{"mute time over the limit", rpc.NotifyParams{Chat: "77", MuteUntil: &overflow}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.changeNotify(context.Background(), params(t, tc.p))
			if code := rpcCode(t, err); code != rpc.CodeInvalidParams {
				t.Fatalf("code = %d, want %d (%v)", code, rpc.CodeInvalidParams, err)
			}
		})
	}
	if f.writes() != 0 {
		t.Errorf("an invalid request reached Telegram: %+v", f.notifies)
	}
}

// TestAudit04_ChangeNotifyWrites covers the live write: Telegram gets the settings,
// the dialog state is refreshed and saved, and a dry run does neither refresh nor
// save.
func TestAudit04_ChangeNotifyWrites(t *testing.T) {
	d, f, db := audit04DialogDaemon(t)
	ctx := context.Background()
	silent := true
	until := int(time.Now().Add(time.Hour).Unix())

	got, err := d.changeNotify(ctx, params(t, rpc.NotifyParams{Chat: "77", Silent: &silent, MuteUntil: &until}))
	if err != nil {
		t.Fatal(err)
	}
	result := got.(rpc.NotifyResult)
	if result.DryRun || result.Silent == nil || !*result.Silent || result.MuteUntil == nil || *result.MuteUntil != until {
		t.Errorf("result = %+v", result)
	}
	if len(f.notifies) != 1 || f.notifies[0].Silent == nil || !*f.notifies[0].Silent || f.notifies[0].DryRun {
		t.Fatalf("Telegram got %+v, want one live write", f.notifies)
	}
	if f.refreshs != 1 {
		t.Errorf("states refreshed %d times, want once", f.refreshs)
	}
	if state, err := db.DialogState(ctx, 77, store.ChatFilter{}); err != nil || !state.Pinned {
		t.Errorf("saved state = %+v %v, want the refreshed one", state, err)
	}

	if _, err := d.changeNotify(ctx, params(t, rpc.NotifyParams{Chat: "77", Silent: &silent, DryRun: true})); err != nil {
		t.Fatal(err)
	}
	if len(f.notifies) != 2 || !f.notifies[1].DryRun || f.refreshs != 1 {
		t.Errorf("a dry run refreshed the state: notifies=%+v refreshs=%d", f.notifies, f.refreshs)
	}
}
