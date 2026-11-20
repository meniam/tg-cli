package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"tg-cli/internal/rpc"
)

func TestSessionID(t *testing.T) {
	if got := sessionID(0); got != "af5570f5a1810b7a" {
		t.Fatalf("id = %s", got)
	}
	if sessionID(1) == sessionID(2) {
		t.Fatal("different hashes have the same id")
	}
}

type sessionFake struct {
	*fakeTelegram
	list  rpc.SessionsResult
	calls int
	err   error // returned by TerminateSession
}

func (f *sessionFake) Sessions(context.Context) (rpc.SessionsResult, error) { return f.list, nil }
func (f *sessionFake) TerminateSession(context.Context, int64) error        { f.calls++; return f.err }

func TestTerminateSessionBoundaries(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &sessionFake{fakeTelegram: base, list: rpc.SessionsResult{Sessions: []rpc.Session{{Hash: 1, Current: true}, {Hash: 2, DeviceModel: "device"}}}}
	d.opts.Telegram = f
	ctx := context.Background()
	run := func(p rpc.TerminateParams) error { _, err := d.terminateSession(ctx, params(t, p)); return err }
	for _, tc := range []struct {
		p    rpc.TerminateParams
		code int
	}{
		{rpc.TerminateParams{ID: sessionID(1), Yes: true}, rpc.CodeInvalidParams},
		{rpc.TerminateParams{ID: sessionID(3), Yes: true}, rpc.CodeNotFound},
		{rpc.TerminateParams{ID: sessionID(2), Yes: true}, rpc.CodeWriteDisabled},
		{rpc.TerminateParams{ID: sessionID(2)}, rpc.CodeInvalidParams},
	} {
		err := run(tc.p)
		var e *rpc.Error
		if !errors.As(err, &e) || e.Code != tc.code {
			t.Fatalf("%+v: %v", tc.p, err)
		}
	}
	if f.calls != 0 {
		t.Fatal("write called before permission")
	}
	writeAccess(t, path, `{"sessions":true}`)
	if err := run(rpc.TerminateParams{ID: sessionID(2), DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("dry-run wrote")
	}
	if _, err := os.Stat(filepath.Join(d.opts.Paths.Dir, "account.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dry-run journal: %v", err)
	}
	if err := run(rpc.TerminateParams{ID: sessionID(2), Yes: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(d.opts.Paths.Dir, "account.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || bytes.Index(data, []byte(`"phase":"intent"`)) < 0 || bytes.Index(data, []byte(`"phase":"result"`)) <= bytes.Index(data, []byte(`"phase":"intent"`)) {
		t.Fatalf("calls=%d journal=%s", f.calls, data)
	}
}

func TestTerminateSessionJournalFailureStopsWrite(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &sessionFake{fakeTelegram: base, list: rpc.SessionsResult{Sessions: []rpc.Session{{Hash: 2}}}}
	d.opts.Telegram = f
	writeAccess(t, path, `{"sessions":true}`)
	d.opts.Paths.Dir = filepath.Join(t.TempDir(), "missing")
	_, err := d.terminateSession(context.Background(), params(t, rpc.TerminateParams{ID: sessionID(2), Yes: true}))
	if err == nil || f.calls != 0 {
		t.Fatalf("err=%v writes=%d", err, f.calls)
	}
}
