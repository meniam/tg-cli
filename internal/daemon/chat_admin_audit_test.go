package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// audit03KickFails is an adminTelegram whose kick is refused by Telegram.
type audit03KickFails struct{ *adminTelegram }

func (f *audit03KickFails) Kick(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, rpc.NewError(rpc.CodeTelegram, "USER_ADMIN_INVALID")
}

func audit03Journal(t *testing.T, dir string) []map[string]any {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, "moderation.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("journal line %q: %v", scanner.Text(), err)
		}
		records = append(records, record)
	}
	return records
}

func audit03AdminDaemon(t *testing.T, chatType string) (*daemon, *adminTelegram) {
	t.Helper()
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f, rights: rpc.ChatOwnAdminRights{Creator: true}}
	d.opts.Telegram = a
	d.opts.Paths.Dir = t.TempDir()
	f.resolved = &rpc.Chat{ID: -42, Type: chatType}
	writeAccess(t, path, `{"admin":[-42],"write":[-42]}`)
	return d, a
}

// Each moderation action reaches Telegram once and the six journaled actions
// leave an intent record and a result record with the same subject.
func TestAudit03ChatAdminActionsAreJournaled(t *testing.T) {
	deny := []string{rpc.ChatAdminRights[0]}
	for _, tc := range []struct {
		action   string
		chatType string
		p        any
		user     float64
	}{
		{"leave", "group", rpc.ChatLeaveParams{Chat: "-42"}, 0},
		{"kick", "supergroup", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}, 77},
		{"ban", "supergroup", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}, 77},
		{"unban", "supergroup", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}, 77},
		{"restrict", "supergroup", rpc.ChatRestrictParams{Chat: "-42", User: "77", Deny: deny}, 77},
		{"permissions", "supergroup", rpc.ChatPermissionsParams{Chat: "-42", Deny: deny}, 0},
	} {
		t.Run(tc.action, func(t *testing.T) {
			d, a := audit03AdminDaemon(t, tc.chatType)
			if _, err := d.chatAdmin(context.Background(), tc.action, params(t, tc.p)); err != nil {
				t.Fatal(err)
			}
			if a.calls != 1 {
				t.Fatalf("Telegram calls = %d, want 1", a.calls)
			}
			records := audit03Journal(t, d.opts.Paths.Dir)
			if len(records) != 2 {
				t.Fatalf("journal has %d records, want intent and result: %v", len(records), records)
			}
			for i, want := range []struct{ phase, outcome string }{{"intent", "pending"}, {"result", "ok"}} {
				r := records[i]
				if r["action"] != tc.action || r["chat_id"] != float64(-42) || r["phase"] != want.phase || r["outcome"] != want.outcome {
					t.Errorf("record %d = %v, want action %s chat -42 phase %s outcome %s", i, r, tc.action, want.phase, want.outcome)
				}
				if got, _ := r["user_id"].(float64); got != tc.user {
					t.Errorf("record %d user_id = %v, want %v", i, r["user_id"], tc.user)
				}
			}
		})
	}
}

// edit, join and slow-mode write to Telegram but are not journaled.
func TestAudit03ChatAdminUnjournaledActionsReachTelegram(t *testing.T) {
	title := "new"
	for _, tc := range []struct {
		action   string
		chatType string
		p        any
	}{
		{"edit", "group", rpc.ChatEditParams{Chat: "-42", Title: &title}},
		{"join", "supergroup", rpc.ChatJoinParams{Chat: "-42"}},
		{"slow-mode", "supergroup", rpc.ChatSlowModeParams{Chat: "-42", Seconds: 30}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			d, a := audit03AdminDaemon(t, tc.chatType)
			if _, err := d.chatAdmin(context.Background(), tc.action, params(t, tc.p)); err != nil {
				t.Fatal(err)
			}
			if a.calls != 1 {
				t.Fatalf("Telegram calls = %d, want 1", a.calls)
			}
			if records := audit03Journal(t, d.opts.Paths.Dir); len(records) != 0 {
				t.Fatalf("%s must not be journaled: %v", tc.action, records)
			}
		})
	}
}

// A chat type that does not fit the action is refused before any write.
func TestAudit03ChatAdminRefusesIncompatibleChatType(t *testing.T) {
	for _, tc := range []struct {
		action   string
		chatType string
		p        any
		want     string
	}{
		{"slow-mode", "group", rpc.ChatSlowModeParams{Chat: "-42", Seconds: 30}, "chat type is incompatible with action"},
		{"restrict", "group", rpc.ChatRestrictParams{Chat: "-42", User: "77", Clear: true}, "chat type is incompatible with action"},
		{"ban", "group", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}, "chat type is incompatible with action"},
		{"permissions", "channel", rpc.ChatPermissionsParams{Chat: "-42", Clear: true}, "chat type is incompatible with action"},
		{"join", "group", rpc.ChatJoinParams{Chat: "-42"}, "joining a basic group requires an invite link"},
	} {
		t.Run(tc.action+"/"+tc.chatType, func(t *testing.T) {
			d, a := audit03AdminDaemon(t, tc.chatType)
			_, err := d.chatAdmin(context.Background(), tc.action, params(t, tc.p))
			var rpcErr *rpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams || !strings.Contains(rpcErr.Message, tc.want) {
				t.Fatalf("err = %v, want invalid params %q", err, tc.want)
			}
			if a.calls != 0 {
				t.Fatal("wrote to Telegram")
			}
			if records := audit03Journal(t, d.opts.Paths.Dir); len(records) != 0 {
				t.Fatalf("journal after a refusal: %v", records)
			}
		})
	}
}

// When Telegram refuses the action, the result record carries the outcome.
func TestAudit03ChatAdminJournalsTelegramFailure(t *testing.T) {
	d, a := audit03AdminDaemon(t, "supergroup")
	d.opts.Telegram = &audit03KickFails{a}
	_, err := d.chatAdmin(context.Background(), "kick", params(t, rpc.ChatMemberActionParams{Chat: "-42", User: "77"}))
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeTelegram {
		t.Fatalf("err = %v, want the Telegram error", err)
	}
	records := audit03Journal(t, d.opts.Paths.Dir)
	if len(records) != 2 || records[0]["phase"] != "intent" || records[1]["phase"] != "result" || records[1]["outcome"] != "telegram_error" {
		t.Fatalf("journal = %v", records)
	}
}
