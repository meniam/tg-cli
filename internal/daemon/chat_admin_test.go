package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type adminTelegram struct {
	*fakeTelegram
	calls  int
	rights rpc.ChatOwnAdminRights
	invite rpc.Chat
	known  bool
}

func (f *adminTelegram) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	if ref.Kind == chatref.ID && ref.ID > 0 {
		return rpc.Chat{ID: ref.ID, Type: "user"}, nil
	}
	return f.fakeTelegram.Resolve(ctx, ref)
}

func (f *adminTelegram) Join(context.Context, chatref.Ref) (rpc.ChatJoinResult, error) {
	f.calls++
	return rpc.ChatJoinResult{}, nil
}
func (f *adminTelegram) Leave(context.Context, chatref.Ref) (rpc.ChatLeaveResult, error) {
	f.calls++
	return rpc.ChatLeaveResult{Left: true}, nil
}
func (f *adminTelegram) CreateChat(context.Context, rpc.ChatCreateParams) (rpc.ChatCreateResult, error) {
	f.calls++
	return rpc.ChatCreateResult{ID: -1000000000042, Type: "supergroup"}, nil
}
func (f *adminTelegram) EditChat(context.Context, chatref.Ref, rpc.ChatEditParams) (rpc.ChatEditResult, error) {
	f.calls++
	return rpc.ChatEditResult{}, nil
}
func (f *adminTelegram) Kick(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, nil
}
func (f *adminTelegram) Ban(context.Context, chatref.Ref, chatref.Ref, string) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, nil
}
func (f *adminTelegram) Unban(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, nil
}
func (f *adminTelegram) Restrict(context.Context, chatref.Ref, chatref.Ref, []string, bool, string) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, nil
}
func (f *adminTelegram) SetDefaultRights(context.Context, chatref.Ref, []string, bool) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, nil
}
func (f *adminTelegram) SetSlowMode(context.Context, chatref.Ref, int) (rpc.ChatAdminResult, error) {
	f.calls++
	return rpc.ChatAdminResult{}, nil
}
func (f *adminTelegram) OwnAdminRights(context.Context, chatref.Ref) (rpc.ChatOwnAdminRights, error) {
	return f.rights, nil
}
func (f *adminTelegram) AdminInviteChat(context.Context, chatref.Ref) (rpc.Chat, bool, error) {
	return f.invite, f.known, nil
}
func TestChatAdminBarrierAndJournal(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f, rights: rpc.ChatOwnAdminRights{Creator: true}}
	d.opts.Telegram = a
	d.opts.Paths.Dir = t.TempDir()
	f.resolved = &rpc.Chat{ID: -42, Type: "group"}
	title := "new"
	p := rpc.ChatEditParams{Chat: "-42", Title: &title}
	run := func() (any, error) { return d.chatAdmin(context.Background(), "edit", params(t, p)) }
	if _, err := run(); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatal(err)
	}
	if a.calls != 0 {
		t.Fatal("write before permission")
	}
	writeAccess(t, path, `{"admin":[-42]}`)
	p.DryRun = true
	if _, err := run(); err != nil {
		t.Fatal(err)
	}
	if a.calls != 0 {
		t.Fatal("dry run wrote")
	}
	p.DryRun = false
	if _, err := run(); err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 {
		t.Fatal("missing edit")
	}
	writeAccess(t, path, `{"write":[-42]}`)
	_, err := d.chatAdmin(context.Background(), "leave", params(t, rpc.ChatLeaveParams{Chat: "-42"}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(d.opts.Paths.Dir, "moderation.jsonl"))
	if err != nil || len(data) == 0 {
		t.Fatalf("journal %q %v", data, err)
	}
}

func TestChatAdminDeniedBeforeWrites(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f}
	d.opts.Telegram = a
	f.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	title := "new"
	cases := []struct {
		action string
		p      any
	}{{"join", rpc.ChatJoinParams{Chat: "-42"}}, {"leave", rpc.ChatLeaveParams{Chat: "-42"}}, {"create", rpc.ChatCreateParams{Title: "new", Type: "supergroup"}}, {"edit", rpc.ChatEditParams{Chat: "-42", Title: &title}}, {"kick", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}}, {"ban", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}}, {"unban", rpc.ChatMemberActionParams{Chat: "-42", User: "77"}}, {"restrict", rpc.ChatRestrictParams{Chat: "-42", User: "77", Clear: true}}, {"permissions", rpc.ChatPermissionsParams{Chat: "-42", Clear: true}}, {"slow-mode", rpc.ChatSlowModeParams{Chat: "-42", Seconds: 30}}}
	for _, tc := range cases {
		_, err := d.chatAdmin(context.Background(), tc.action, params(t, tc.p))
		if writeCode(t, err) != rpc.CodeWriteDisabled {
			t.Errorf("%s: %v", tc.action, err)
		}
	}
	if a.calls != 0 {
		t.Fatal("write without permission")
	}
	writeAccess(t, path, `{"block":[-42],"admin":[-42],"write":[-42]}`)
	if _, err := d.chatAdmin(context.Background(), "leave", params(t, rpc.ChatLeaveParams{Chat: "-42"})); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatal(err)
	}
	if a.calls != 0 {
		t.Fatal("write to blocked chat")
	}
}
func TestChatAdminJournalFailureStopsWrite(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f}
	d.opts.Telegram = a
	f.resolved = &rpc.Chat{ID: -42, Type: "group"}
	writeAccess(t, path, `{"write":[-42]}`)
	d.opts.Paths.Dir = filepath.Join(t.TempDir(), "missing")
	if _, err := d.chatAdmin(context.Background(), "leave", params(t, rpc.ChatLeaveParams{Chat: "-42"})); err == nil {
		t.Fatal("missing journal accepted")
	}
	if a.calls != 0 {
		t.Fatal("write after failed intent")
	}
}

func TestChatAdminDryRunRightsAndMemberVisibility(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f}
	d.opts.Telegram = a
	f.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	writeAccess(t, path, `{"allow":[-42],"admin":[-42],"block":[78]}`)
	p := rpc.ChatMemberActionParams{Chat: "-42", User: "77", DryRun: true}
	if _, err := d.chatAdmin(context.Background(), "kick", params(t, p)); writeCode(t, err) != rpc.CodeTelegram {
		t.Fatalf("missing admin rights: %v", err)
	}
	a.rights = rpc.ChatOwnAdminRights{BanUsers: true}
	got, err := d.chatAdmin(context.Background(), "kick", params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if got.(adminDryRun).UserID != 77 || a.calls != 0 {
		t.Fatalf("dry run: %+v", got)
	}
	p.User = "78"
	if _, err = d.chatAdmin(context.Background(), "kick", params(t, p)); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("blocked user: %v", err)
	}
	p.User = "42"
	if _, err = d.chatAdmin(context.Background(), "kick", params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("self: %v", err)
	}
}

func TestChatAdminCreateClosedWithAllowList(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f}
	d.opts.Telegram = a
	writeAccess(t, path, `{"allow":[-1000000000042],"create":true}`)
	result, err := d.chatAdmin(context.Background(), "create", params(t, rpc.ChatCreateParams{Title: "test", Type: "supergroup"}))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(rpc.ChatCreateResult)
	if !got.Closed || got.Title != "" || a.calls != 1 {
		t.Fatalf("result %+v calls=%d", got, a.calls)
	}
}

func TestChatAdminInviteRequiresKnownAllowedChat(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	a := &adminTelegram{fakeTelegram: f, invite: rpc.Chat{ID: -42, Type: "supergroup"}}
	d.opts.Telegram = a
	p := rpc.ChatJoinParams{Chat: "https://t.me/+AbCdEfGh1234", DryRun: true}
	writeAccess(t, path, `{"write":[-42]}`)
	if _, err := d.chatAdmin(context.Background(), "join", params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("unknown invite: %v", err)
	}
	a.known = true
	writeAccess(t, path, `{"write":[-42],"block":[-42]}`)
	if _, err := d.chatAdmin(context.Background(), "join", params(t, p)); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("blocked invite: %v", err)
	}
	writeAccess(t, path, `{"write":[-42]}`)
	if _, err := d.chatAdmin(context.Background(), "join", params(t, p)); err != nil {
		t.Fatal(err)
	}
	if a.calls != 0 {
		t.Fatal("dry run joined")
	}
}
