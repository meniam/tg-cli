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
			tgDone := make(chan error, 1)
			serveDone := make(chan error, 1)
			done := make(chan struct{})
			go func() { d.waitForShutdown(ctx, tgDone, serveDone); close(done) }()
			tgDone <- errors.New("client stopped")
			select {
			case <-done:
				t.Fatal("daemon stopped after authorization loss")
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("daemon did not stop")
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
