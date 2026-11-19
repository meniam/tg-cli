package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

func TestAuthorizationLossKeepsDaemonAvailable(t *testing.T) {
	for _, kind := range []string{"key_invalid", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			d, tg, _, _ := accessTestDaemon(t)
			d.login = newLoginManager(context.Background(), tg)
			path := filepath.Join(t.TempDir(), "session.json")
			original := []byte("synthetic session")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			d.opts.Paths.Session = path
			d.setAuthLost(kind)
			value, err := d.status(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			status := value.(rpc.StatusResult)
			if status.AuthLost != kind || status.Authorized {
				t.Fatalf("status: %+v", status)
			}
			for _, method := range []string{rpc.MethodChatList, rpc.MethodContactList, rpc.MethodAccountProfile} {
				err = d.guardRequest(rpc.Request{Method: method})
				var re *rpc.Error
				if !errors.As(err, &re) || re.Code != rpc.CodeNotAuthorized || !strings.Contains(re.Message, kind) {
					t.Fatalf("%s: %v", method, err)
				}
			}
			if err = d.guardRequest(rpc.Request{Method: rpc.MethodAuthStart}); err != nil {
				t.Fatalf("auth is blocked: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// tgDone is unbuffered: the send below returns only after
			// waitForShutdown has taken the client failure.
			tgDone := make(chan error)
			serveDone := make(chan error, 1)
			done := make(chan struct{})
			var stoppedBeforeCancel bool
			go func() {
				_, _ = d.waitForShutdown(ctx, tgDone, serveDone)
				stoppedBeforeCancel = ctx.Err() == nil
				close(done)
			}()
			tgDone <- errors.New("client stopped")
			// Negative check: the window only has to be long enough for an
			// early return to show up. stoppedBeforeCancel catches one that
			// the window misses.
			select {
			case <-done:
				t.Fatal("daemon stopped after authorization loss")
			case <-time.After(50 * time.Millisecond):
			}
			if d.getAuthLost() != kind {
				t.Fatalf("authorization state = %q, want %q", d.getAuthLost(), kind)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("daemon did not stop")
			}
			if stoppedBeforeCancel {
				t.Fatal("daemon stopped before the context was canceled")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(original) {
				t.Fatal("session file changed")
			}
		})
	}
}

func TestUnexpectedClientFailureStopsDaemon(t *testing.T) {
	d := &daemon{}
	tgDone := make(chan error, 1)
	tgDone <- errors.New("failed")
	err, finished := d.waitForShutdown(context.Background(), tgDone, make(chan error))
	if err == nil || !finished {
		t.Fatal("unexpected client failure was ignored")
	}
}

func TestAuthorizationLossLoginRestartsClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, _, _, _ := accessTestDaemon(t)
	initial := newFake()
	d.opts.Telegram = initial
	d.opts.NewTelegram = func(context.Context, TelegramConfig) (Telegram, error) { return nil, errors.New("unused") }
	d.admission.unblock(initial)
	var next *fakeTelegram
	count := 0
	factory := func(context.Context) (Telegram, error) {
		count++
		client := newFake()
		if next != nil {
			client.user, _ = next.Self(context.Background())
		}
		next = client
		return client, nil
	}
	d.supervisor = newClientSupervisor(ctx, &d.admission, initial, factory, func(Telegram) {}, func() bool { return d.getAuthLost() != "" }, nil, nil, time.Second)
	d.login = newLoginManager(ctx, initial)
	d.login.acquire = d.acquireTelegram
	d.login.onUser = d.account.set
	authorized := make(chan struct{}, 1)
	d.login.onAuthorized = func() error { return d.onAuthorized(ctx, authorized) }
	d.setAuthLost("revoked")
	result, err := d.authStart(ctx, params(t, rpc.AuthStartParams{Phone: "+123"}))
	if err != nil || result.(rpc.AuthResult).State != rpc.AuthCodeRequired {
		t.Fatalf("start: %v %v", result, err)
	}
	result, err = d.authCode(ctx, params(t, rpc.AuthCodeParams{Code: "12345"}))
	if err != nil || result.(rpc.AuthResult).State != rpc.AuthAuthorized {
		t.Fatalf("code: %v %v", result, err)
	}
	if d.getAuthLost() != "" {
		t.Fatal("authorization loss was not cleared")
	}
	if count != 2 {
		t.Fatalf("created %d clients", count)
	}
	select {
	case <-next.Ready():
	case <-time.After(time.Second):
		t.Fatal("replacement did not start")
	}
	value, err := d.status(ctx, nil)
	if err != nil || !value.(rpc.StatusResult).Authorized {
		t.Fatalf("status: %+v %v", value, err)
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := d.supervisor.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}
