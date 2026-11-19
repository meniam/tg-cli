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

func TestUserContactCLI(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		method     string
		wantParams string
		wantCode   int
		wantMsg    string
	}{
		{args: []string{"user", "profile", "77"}, method: rpc.MethodUserProfile, wantParams: `{"user":"77"}`},
		{args: []string{"user", "block", "77", "--dry-run"}, method: rpc.MethodUserBlock, wantParams: `{"user":"77","dry_run":true}`},
		{args: []string{"user", "unblock", "77"}, method: rpc.MethodUserUnblock, wantParams: `{"user":"77"}`},
		{args: []string{"contact", "add", "77", "--first-name", "Name"}, method: rpc.MethodContactAdd, wantParams: `{"user":"77","first_name":"Name"}`},
		{args: []string{"contact", "delete", "77"}, wantCode: exitcode.Usage, wantMsg: "--yes is required to delete a contact"},
		{args: []string{"contact", "delete", "77", "--yes"}, method: rpc.MethodContactDelete, wantParams: `{"user":"77","yes":true}`},
		{args: []string{"contact", "delete", "77", "--dry-run"}, method: rpc.MethodContactDelete, wantParams: `{"user":"77","dry_run":true}`},
		{args: []string{"contact", "list", "--limit", "0"}, wantCode: exitcode.Usage, wantMsg: "--limit must be at least 1"},
		{args: []string{"contact", "list", "--offset", "-1"}, wantCode: exitcode.Usage, wantMsg: "--offset must not be negative"},
		{args: []string{"contact", "add", "+12345678"}, wantCode: exitcode.Usage, wantMsg: "invalid user reference"},
		{args: []string{"contact", "search", "Name", "--local"}, method: rpc.MethodContactSearch, wantParams: `{"query":"Name","limit":20,"local":true}`},
	} {
		t.Run(tc.args[0]+"/"+tc.args[1]+"/"+tc.args[len(tc.args)-1], func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			srv := rpc.NewServer(nil)
			type call struct {
				method string
				params string
			}
			calls := make(chan call, 1)
			if tc.method != "" {
				srv.Handle(tc.method, func(_ context.Context, raw json.RawMessage) (any, error) {
					calls <- call{tc.method, string(raw)}
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
			code := Execute(context.Background(), tc.args, Deps{Stdout: &out, Stderr: &errout, ConfigPath: audit03ConfigPath(t), Getenv: audit03HomeEnv(audit03ShortDir(t)), DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientConn), nil }})
			clientConn.Close()
			<-done
			if code != tc.wantCode {
				t.Fatalf("code=%d out=%s err=%s", code, out.String(), errout.String())
			}
			if tc.wantMsg != "" && !strings.Contains(out.String()+errout.String(), tc.wantMsg) {
				t.Fatalf("want message %q, out=%s err=%s", tc.wantMsg, out.String(), errout.String())
			}
			select {
			case got := <-calls:
				if got.method != tc.method {
					t.Fatal(got.method)
				}
				if !audit03SameJSON(t, got.params, tc.wantParams) {
					t.Fatalf("params = %s, want %s", got.params, tc.wantParams)
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
	code := Execute(context.Background(), []string{"contact", "list", "--local", "--limit", "1", "--format", "jsonl", "--fields", "id"}, Deps{Stdout: &out, Stderr: &stderr, ConfigPath: audit03ConfigPath(t), Getenv: audit03HomeEnv(audit03ShortDir(t)), DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientConn), nil }})
	clientConn.Close()
	<-done
	if code != 0 || out.String() != "{\"id\":7}\n{\"next_offset\":1}\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}
