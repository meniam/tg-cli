package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestAuthStepsWithoutTerminal(t *testing.T) {
	for _, tc := range []struct {
		args                              []string
		input, method, field, want, state string
		secret                            bool
	}{
		{[]string{"auth", "login", "--phone", "+15550001111", "--no-input"}, "", rpc.MethodAuthStart, "phone", "+15550001111", "code_required", false},
		{[]string{"auth", "code"}, "CODE777\n", rpc.MethodAuthCode, "code", "CODE777", "password_required", true},
		{[]string{"auth", "password", "--password-stdin"}, "  PASS888  \n", rpc.MethodAuthPassword, "password", "  PASS888  ", "authorized", true},
	} {
		t.Run(tc.method, func(t *testing.T) {
			home := audit01Isolate(t)
			server, client := net.Pipe()
			srv := rpc.NewServer(nil)
			called := false
			srv.Handle(tc.method, func(_ context.Context, raw json.RawMessage) (any, error) {
				called = true
				var params map[string]string
				if err := json.Unmarshal(raw, &params); err != nil || len(params) != 1 || params[tc.field] != tc.want {
					t.Errorf("params for %s: want only %s=%q, got %s (%v)", tc.method, tc.field, tc.want, raw, err)
				}
				return rpc.AuthResult{State: tc.state}, nil
			})
			done := make(chan struct{})
			go func() { srv.ServeConn(server); close(done) }()
			var out, stderr bytes.Buffer
			deps := Deps{Stdout: &out, Stderr: &stderr, Stdin: strings.NewReader(tc.input), ConfigPath: filepath.Join(home, "config.toml"), Getenv: func(name string) string {
				if name == "TG_CLI_HOME" {
					return home
				}
				return ""
			}, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(client), nil }}
			code := Execute(context.Background(), tc.args, deps)
			client.Close()
			<-done
			srv.Close()
			if code != exitcode.OK || !called || !strings.Contains(out.String(), tc.state) {
				t.Fatalf("step failed: %d %s", code, out.String())
			}
			if tc.secret && (strings.Contains(out.String(), tc.want) || strings.Contains(stderr.String(), tc.want)) {
				t.Fatal("secret leaked")
			}
		})
	}
}
