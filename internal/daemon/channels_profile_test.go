package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func TestReadersFiltersBlockedPeople(t *testing.T) {
	d, tg, _, path := accessTestDaemon(t)
	writeAccess(t, path, `{"block":[5]}`)
	tg.resolved = &rpc.Chat{ID: -10, Type: "group"}
	result, err := d.messageReaders(context.Background(), params(t, rpc.MessageGetParams{Chat: "-10", ID: 11}))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(rpc.MessageReadersResult); len(got.Readers) != 0 {
		t.Fatalf("blocked reader leaked: %+v", got)
	}
}
func TestReadersRejectsChannelAndClosedChat(t *testing.T) {
	d, tg, _, path := accessTestDaemon(t)
	tg.resolved = &rpc.Chat{ID: -10, Type: "channel"}
	_, err := d.messageReaders(context.Background(), params(t, rpc.MessageGetParams{Chat: "-10", ID: 11}))
	if err == nil {
		t.Fatal("channel accepted")
	}
	writeAccess(t, path, `{"block":[-10]}`)
	_, err = d.messageReaders(context.Background(), params(t, rpc.MessageGetParams{Chat: "-10", ID: 11}))
	sameRPCError(t, err, rpc.ErrUnknownChatID())
}

type requestActionTelegram struct {
	*adminTelegram
	requestCalls int
}

func (f *requestActionTelegram) HandleJoinRequest(context.Context, chatref.Ref, chatref.Ref, bool) error {
	f.requestCalls++
	return nil
}
func TestJoinRequestBarriersAndDryRun(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	f.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	tg := &requestActionTelegram{adminTelegram: &adminTelegram{fakeTelegram: f, rights: rpc.ChatOwnAdminRights{InviteUsers: true}}}
	d.opts.Telegram = tg
	p := rpc.JoinRequestActionParams{Chat: "-42", User: "77"}
	if _, err := d.approveJoinRequest(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("denied: %v", err)
	}
	if tg.requestCalls != 0 {
		t.Fatal("write without admin")
	}
	writeAccess(t, path, `{"admin":[-42]}`)
	p.DryRun = true
	if _, err := d.approveJoinRequest(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.requestCalls != 0 {
		t.Fatal("dry run wrote")
	}
	p.DryRun = false
	if _, err := d.declineJoinRequest(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.requestCalls != 1 {
		t.Fatal("missing write")
	}
	writeAccess(t, path, `{"admin":[-42],"block":[77]}`)
	if _, err := d.approveJoinRequest(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("blocked person: %v", err)
	}
	if tg.requestCalls != 1 {
		t.Fatal("write to blocked person")
	}
}

type settingTelegram struct {
	*adminTelegram
	settingCalls int
}

func (f *settingTelegram) SetChannelSetting(_ context.Context, _ chatref.Ref, flag, _ string, _, _ *string) (bool, error) {
	f.settingCalls++
	if flag == "prehistory" {
		return false, rpc.NewError(rpc.CodeTelegram, "rejected")
	}
	return true, nil
}
func TestChatSetBarrierAndPartial(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	f.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	tg := &settingTelegram{adminTelegram: &adminTelegram{fakeTelegram: f, rights: rpc.ChatOwnAdminRights{ChangeInfo: true}}}
	d.opts.Telegram = tg
	on, hidden := "on", "hidden"
	p := rpc.ChatSetParams{Chat: "-42", Forum: &on, Prehistory: &hidden}
	if _, err := d.chatSet(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("denied: %v", err)
	}
	if tg.settingCalls != 0 {
		t.Fatal("write without admin")
	}
	writeAccess(t, path, `{"admin":[-42]}`)
	p.DryRun = true
	if _, err := d.chatSet(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.settingCalls != 0 {
		t.Fatal("dry run wrote")
	}
	p.DryRun = false
	r, err := d.chatSet(context.Background(), params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	result := r.(rpc.ChatSetResult)
	if !result.Partial || len(result.Changes) != 1 || len(result.Errors) != 1 || tg.settingCalls != 2 {
		t.Fatalf("result %+v, calls %d", result, tg.settingCalls)
	}
}

type accountSettingTelegram struct {
	*fakeTelegram
	calls int
}

func (f *accountSettingTelegram) SetAccountField(context.Context, string, string, string, *chatref.Ref) (bool, error) {
	f.calls++
	return true, nil
}
func TestAccountSetBarrierAndDryRun(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	tg := &accountSettingTelegram{fakeTelegram: f}
	d.opts.Telegram = tg
	none := "none"
	p := rpc.AccountSetParams{Birthday: &none}
	if _, err := d.accountSet(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("denied: %v", err)
	}
	if tg.calls != 0 {
		t.Fatal("write without profile")
	}
	writeAccess(t, path, `{"profile":true}`)
	p.DryRun = true
	if _, err := d.accountSet(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.calls != 0 {
		t.Fatal("dry run wrote")
	}
	p.DryRun = false
	if _, err := d.accountSet(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.calls != 1 {
		t.Fatalf("calls %d", tg.calls)
	}
}

func TestChatRequestsFiltersBlocked(t *testing.T) {
	d, f, db, path := accessTestDaemon(t)
	f.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	writeAccess(t, path, `{"block":[5]}`)
	r, err := d.chatRequests(context.Background(), params(t, rpc.JoinRequestsParams{Chat: "-42"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.(rpc.JoinRequestsResult).Requests) != 0 {
		t.Fatal("blocked applicant leaked")
	}
	if _, err := db.GetPerson(context.Background(), 5); err == nil {
		t.Fatal("blocked applicant saved")
	}
}

type privacyTelegram struct {
	*fakeTelegram
	writes int
	rules  rpc.PrivacyRules
}

func (f *privacyTelegram) AccountPrivacyGet(context.Context, string) (rpc.PrivacyRules, error) {
	return f.rules, nil
}
func (f *privacyTelegram) AccountPrivacySet(_ context.Context, p rpc.PrivacySetParams, _, _, _, _ []chatref.Ref) (rpc.PrivacyRules, error) {
	f.writes++
	return rpc.PrivacyRules{Key: p.Key, Default: p.Default}, nil
}
func TestPrivacySetRequiresProfileAndHiddenConfirmation(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	tg := &privacyTelegram{fakeTelegram: f, rules: rpc.PrivacyRules{Key: "last-seen", Default: "nobody", AllowUsers: []int64{77}}}
	d.opts.Telegram = tg
	p := rpc.PrivacySetParams{Key: "last-seen", Default: "nobody", Yes: true}
	if _, err := d.accountPrivacySet(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("profile barrier: %v", err)
	}
	if tg.writes != 0 {
		t.Fatal("write without permission")
	}
	writeAccess(t, path, `{"profile":true,"block":[77]}`)
	if _, err := d.accountPrivacySet(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("hidden: %v", err)
	}
	if tg.writes != 0 {
		t.Fatal("write with hidden rule")
	}
	p.ReplaceHidden = true
	p.DryRun = true
	if _, err := d.accountPrivacySet(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.writes != 0 {
		t.Fatal("dry run wrote")
	}
	p.DryRun = false
	if _, err := d.accountPrivacySet(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if tg.writes != 1 {
		t.Fatalf("writes %d", tg.writes)
	}
}

type floodSettingsTelegram struct {
	*adminTelegram
	calls int
	flood bool
}

func (f *floodSettingsTelegram) SetChannelSetting(_ context.Context, _ chatref.Ref, _ string, _ string, _, _ *string) (bool, error) {
	f.calls++
	if f.flood {
		return false, rpc.NewError(rpc.CodeFloodWait, "wait")
	}
	return true, nil
}
func TestChatSetGroupsSignaturesAndStopsOnFlood(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	f.resolved = &rpc.Chat{ID: -42, Type: "channel"}
	tg := &floodSettingsTelegram{adminTelegram: &adminTelegram{fakeTelegram: f}}
	d.opts.Telegram = tg
	writeAccess(t, path, `{"admin":[-42]}`)
	on := "on"
	p := rpc.ChatSetParams{Chat: "-42", Signatures: &on, SignatureProfiles: &on, JoinRequests: &on}
	r, err := d.chatSet(context.Background(), params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.(rpc.ChatSetResult); len(got.Changes) != 3 || tg.calls != 2 {
		t.Fatalf("result %+v calls %d", got, tg.calls)
	}
	tg.calls = 0
	tg.flood = true
	if _, err := d.chatSet(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeFloodWait {
		t.Fatalf("flood: %v", err)
	}
	if tg.calls != 1 {
		t.Fatalf("retried or continued: %d", tg.calls)
	}
}

func TestSettingsJournalFailurePreventsWrites(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	f.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	tg := &settingTelegram{adminTelegram: &adminTelegram{fakeTelegram: f}}
	d.opts.Telegram = tg
	writeAccess(t, path, `{"admin":[-42]}`)
	d.opts.Paths.Dir = filepath.Join(t.TempDir(), "missing")
	on := "on"
	if _, err := d.chatSet(context.Background(), params(t, rpc.ChatSetParams{Chat: "-42", Forum: &on})); err == nil {
		t.Fatal("journal failure accepted")
	}
	if tg.settingCalls != 0 {
		t.Fatal("write after failed journal")
	}
}
