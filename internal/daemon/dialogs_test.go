package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type dialogFake struct {
	*fakeTelegram
	writes int
}

func (f *dialogFake) ArchiveDialogs(context.Context, []chatref.Ref, bool) error {
	f.writes++
	return nil
}
func (f *dialogFake) PinDialog(context.Context, chatref.Ref, bool) error { f.writes++; return nil }
func TestDialogPermissionAndDryRun(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &dialogFake{fakeTelegram: base}
	d.opts.Telegram = f
	call := func(p rpc.DialogParams) (any, error) {
		return d.changeDialog(context.Background(), params(t, p), "archive")
	}
	_, err := call(rpc.DialogParams{Chats: []string{"77"}})
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeWriteDisabled || f.writes != 0 {
		t.Fatalf("without permission: %v writes=%d", err, f.writes)
	}
	writeAccess(t, path, `{"dialogs":true,"block":[88]}`)
	_, err = call(rpc.DialogParams{Chats: []string{"77", "88"}})
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound || f.writes != 0 {
		t.Fatalf("closed chat: %v writes=%d", err, f.writes)
	}
	res, err := call(rpc.DialogParams{Chats: []string{"77"}, DryRun: true})
	if err != nil || !res.(rpc.DialogArchiveResult).DryRun || f.writes != 0 {
		t.Fatalf("dry run: %v %v", res, err)
	}
}

func TestNotifyAccessAndDryRun(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &dialogFake{fakeTelegram: base}
	d.opts.Telegram = f
	silent := true
	p := rpc.NotifyParams{Chat: "77", Silent: &silent, DryRun: true}
	_, err := d.changeNotify(context.Background(), params(t, p))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeWriteDisabled {
		t.Fatalf("without dialogs permission: %v", err)
	}
	writeAccess(t, path, `{"dialogs":true,"block":[88]}`)
	p.Chat = "88"
	_, err = d.changeNotify(context.Background(), params(t, p))
	if !errors.As(err, &re) || re.Code != rpc.CodeNotFound {
		t.Fatalf("closed: %v", err)
	}
	p.Chat = "77"
	got, err := d.changeNotify(context.Background(), params(t, p))
	if err != nil || !got.(rpc.NotifyResult).DryRun {
		t.Fatalf("dry-run: %v %v", got, err)
	}
}

func TestFolderInviteAccess(t *testing.T) {
	d, _, _, path := accessTestDaemon(t)
	p := rpc.FolderInviteParams{Link: "t.me/addlist/AbCdEfGh"}
	writeAccess(t, path, `{"allow":[42]}`)
	_, err := d.folderInviteInfo(context.Background(), params(t, p))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeNotFound {
		t.Fatalf("allow list: %v", err)
	}
	writeAccess(t, path, `{"block":[77]}`)
	got, err := d.folderInviteInfo(context.Background(), params(t, p))
	if err != nil || len(got.(rpc.FolderInviteResult).Chats) != 0 {
		t.Fatalf("block list: %v %v", got, err)
	}
}

type folderFake struct {
	*fakeTelegram
	writes int
}

func (f *folderFake) Folders(context.Context) (rpc.FoldersResult, error) {
	return rpc.FoldersResult{Folders: []rpc.Folder{{ID: 2, Kind: "filter", Title: "test", Include: []rpc.FolderPeer{{ID: 88}}}}}, nil
}
func (f *folderFake) WriteFolder(_ context.Context, p rpc.FolderWriteParams, action string) (rpc.FolderWriteResult, error) {
	if !p.DryRun {
		f.writes++
	}
	return rpc.FolderWriteResult{ID: 2, DryRun: p.DryRun}, nil
}
func TestFolderWriteBarriersAndHidden(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	fake := &folderFake{fakeTelegram: base}
	d.opts.Telegram = fake
	ctx := context.Background()
	title := "New"
	p := rpc.FolderWriteParams{ID: 2, Title: &title, DryRun: true}
	_, err := d.folderWrite(ctx, params(t, p), "edit")
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeWriteDisabled {
		t.Fatalf("without permission: %v", err)
	}
	writeAccess(t, path, `{"dialogs":true,"block":[88]}`)
	v, err := d.folderWrite(ctx, params(t, p), "edit")
	if err != nil || v.(rpc.FolderWriteResult).Hidden != 1 || fake.writes != 0 {
		t.Fatalf("hidden: %+v %v", v, err)
	}
	_, err = d.folderWrite(ctx, params(t, rpc.FolderWriteParams{ID: 2}), "delete")
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams || fake.writes != 0 {
		t.Fatalf("confirmation: %v", err)
	}
	d.opts.Paths.Dir = filepath.Join(t.TempDir(), "missing")
	_, err = d.folderWrite(ctx, params(t, rpc.FolderWriteParams{ID: 2, Yes: true}), "delete")
	if err == nil || fake.writes != 0 {
		t.Fatalf("journal error: %v writes %d", err, fake.writes)
	}
}

func TestDialogWriteAfterPermission(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	fake := &dialogFake{fakeTelegram: base}
	d.opts.Telegram = fake
	writeAccess(t, path, `{"dialogs":true}`)
	out, err := d.changeDialog(context.Background(), params(t, rpc.DialogParams{Chats: []string{"77"}}), "archive")
	if err != nil || fake.writes != 1 || !out.(rpc.DialogArchiveResult).Chats[0].Archived {
		t.Fatalf("result %+v writes=%d err=%v", out, fake.writes, err)
	}
}

func TestFolderDeleteJournalsDefinitionAndResult(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	fake := &folderFake{fakeTelegram: base}
	d.opts.Telegram = fake
	writeAccess(t, path, `{"dialogs":true,"block":[88]}`)
	_, err := d.folderWrite(context.Background(), params(t, rpc.FolderWriteParams{ID: 2, Yes: true}), "delete")
	if err != nil || fake.writes != 1 {
		t.Fatalf("write=%d err=%v", fake.writes, err)
	}
	data, err := os.ReadFile(filepath.Join(d.opts.Paths.Dir, "account.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"id":88`)) || !bytes.Contains(data, []byte(`"phase":"intent"`)) || !bytes.Contains(data, []byte(`"phase":"result"`)) {
		t.Fatalf("journal %s", data)
	}
}
