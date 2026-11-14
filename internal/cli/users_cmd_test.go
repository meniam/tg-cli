package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestUserContactCLI(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		method   string
		wantCode int
	}{
		{[]string{"user", "profile", "77"}, rpc.MethodUserProfile, 0},
		{[]string{"user", "block", "77", "--dry-run"}, rpc.MethodUserBlock, 0},
		{[]string{"user", "unblock", "77"}, rpc.MethodUserUnblock, 0},
		{[]string{"contact", "add", "77", "--first-name", "Name"}, rpc.MethodContactAdd, 0},
		{[]string{"contact", "delete", "77"}, "", exitcode.Usage},
		{[]string{"contact", "delete", "77", "--yes"}, rpc.MethodContactDelete, 0},
		{[]string{"contact", "delete", "77", "--dry-run"}, rpc.MethodContactDelete, 0},
		{[]string{"contact", "list", "--limit", "0"}, "", exitcode.Usage},
		{[]string{"contact", "list", "--offset", "-1"}, "", exitcode.Usage},
		{[]string{"contact", "add", "+12345678"}, "", exitcode.Usage},
		{[]string{"contact", "search", "Name", "--local"}, rpc.MethodContactSearch, 0},
	} {
		t.Run(tc.args[0]+"/"+tc.args[1]+"/"+tc.args[len(tc.args)-1], func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			srv := rpc.NewServer(nil)
			calls := make(chan string, 1)
			if tc.method != "" {
				srv.Handle(tc.method, func(_ context.Context, _ json.RawMessage) (any, error) {
					calls <- tc.method
					switch tc.method {
					case rpc.MethodContactSearch:
						return rpc.ContactSearchResult{Contacts: []rpc.ContactHit{}}, nil
					default:
						return map[string]any{}, nil
					}
				})
			}
			done := make(chan struct{})
			go func() { defer close(done); srv.ServeConn(serverConn) }()
			var out, errout bytes.Buffer
			code := Execute(context.Background(), tc.args, Deps{Stdout: &out, Stderr: &errout, Getenv: func(string) string { return "" }, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientConn), nil }})
			clientConn.Close()
			<-done
			if code != tc.wantCode {
				t.Fatalf("code=%d out=%s err=%s", code, out.String(), errout.String())
			}
			select {
			case method := <-calls:
				if method != tc.method {
					t.Fatal(method)
				}
			default:
				if tc.method != "" {
					t.Fatal("no RPC call")
				}
			}
		})
	}
}

func TestContactListJSONLPage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	srv := rpc.NewServer(nil)
	srv.Handle(rpc.MethodContactList, func(_ context.Context, raw json.RawMessage) (any, error) {
		var p rpc.ContactListParams
		if err := json.Unmarshal(raw, &p); err != nil || p.Limit != 1 || !p.Local {
			t.Errorf("params %+v: %v", p, err)
		}
		return rpc.ContactListResult{Contacts: []rpc.Contact{{ID: 7, FirstName: "Name"}}, NextOffset: 1}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); srv.ServeConn(serverConn) }()
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"contact", "list", "--local", "--limit", "1", "--format", "jsonl", "--fields", "id"}, Deps{Stdout: &out, Stderr: &stderr, Getenv: func(string) string { return "" }, DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientConn), nil }})
	clientConn.Close()
	<-done
	if code != 0 || out.String() != "{\"id\":7}\n{\"next_offset\":1}\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}
