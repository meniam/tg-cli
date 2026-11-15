package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestAuthStepsWithoutTerminal(t *testing.T) {
	for _, tc := range []struct {
		args                       []string
		input, method, want, state string
	}{
		{[]string{"auth", "login", "--phone", "+15550001111", "--no-input"}, "", rpc.MethodAuthStart, "", "code_required"},
		{[]string{"auth", "code"}, "CODE777\n", rpc.MethodAuthCode, "CODE777", "password_required"},
		{[]string{"auth", "password", "--password-stdin"}, "  PASS888  \n", rpc.MethodAuthPassword, "  PASS888  ", "authorized"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			server, client := net.Pipe()
			srv := rpc.NewServer(nil)
			called := false
			srv.Handle(tc.method, func(_ context.Context, raw json.RawMessage) (any, error) {
				called = true
				if tc.want != "" {
					var params map[string]string
					if json.Unmarshal(raw, &params) != nil || (params["code"] != tc.want && params["password"] != tc.want) {
						t.Error("incorrect secret input")
					}
				}
				return rpc.AuthResult{State: tc.state}, nil
			})
			done := make(chan struct{})
			go func() { srv.ServeConn(server); close(done) }()
			var out, stderr bytes.Buffer
			deps := Deps{Stdout: &out, Stderr: &stderr, Stdin: strings.NewReader(tc.input), Getenv: func(string) string { return "/tmp/tg-auth-test" }, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(client), nil }}
			code := Execute(context.Background(), tc.args, deps)
			client.Close()
			<-done
			srv.Close()
			if code != exitcode.OK || !called || !strings.Contains(out.String(), tc.state) {
				t.Fatalf("step failed: %d %s", code, out.String())
			}
			if tc.want != "" && (strings.Contains(out.String(), tc.want) || strings.Contains(stderr.String(), tc.want)) {
				t.Fatal("secret leaked")
			}
		})
	}
}
