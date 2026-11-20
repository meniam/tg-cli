package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type indexCLIRun struct {
	code           int
	stdout, stderr string
	methods        []string
	params         []json.RawMessage
}

// runIndexCLI runs the CLI against a daemon that answers every listed method
// with reply and records the calls.
func runIndexCLI(t *testing.T, args []string, methods []string, reply func(string, json.RawMessage) (any, error)) indexCLIRun {
	t.Helper()
	home := audit01Isolate(t)
	serverSide, clientSide := net.Pipe()
	server := rpc.NewServer(nil)
	var result indexCLIRun
	var mu sync.Mutex
	for _, method := range methods {
		server.Handle(method, func(_ context.Context, raw json.RawMessage) (any, error) {
			mu.Lock()
			result.methods = append(result.methods, method)
			result.params = append(result.params, raw)
			mu.Unlock()
			return reply(method, raw)
		})
	}
	done := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(done) }()
	var stdout, stderr bytes.Buffer
	result.code = Execute(context.Background(), args, Deps{
		Stdout: &stdout, Stderr: &stderr,
		ConfigPath: filepath.Join(home, "config.toml"),
		Getenv: func(key string) string {
			if key == "TG_CLI_HOME" {
				return home
			}
			return ""
		},
		DialRPC: func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(clientSide), nil },
	})
	clientSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC server did not stop")
	}
	server.Close()
	result.stdout, result.stderr = stdout.String(), stderr.String()
	return result
}

func indexStatusFixture() rpc.SemanticIndexStatus {
	started := int64(1795176000)
	return rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexIndexing, Model: "bge-m3:latest", ModelDigest: "abc", Indexed: 10, Total: 40, Embedded: 5, StartedAt: &started}
}

func TestSearchIndexCommandsCallTheirMethods(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		method string
	}{
		{"start", []string{"search", "index", "start"}, rpc.MethodSearchIndexStart},
		{"status", []string{"search", "index", "status"}, rpc.MethodSearchIndexStatus},
		{"cancel", []string{"search", "index", "cancel"}, rpc.MethodSearchIndexCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runIndexCLI(t, tc.args, []string{tc.method}, func(string, json.RawMessage) (any, error) { return indexStatusFixture(), nil })
			if run.code != 0 || len(run.methods) != 1 || run.methods[0] != tc.method {
				t.Fatalf("code %d methods %v stderr %q", run.code, run.methods, run.stderr)
			}
			var env struct {
				OK   bool                    `json:"ok"`
				Data rpc.SemanticIndexStatus `json:"data"`
			}
			if err := json.Unmarshal([]byte(run.stdout), &env); err != nil || !env.OK {
				t.Fatalf("stdout %q: %v", run.stdout, err)
			}
			if env.Data.State != rpc.SemanticIndexIndexing || env.Data.Indexed != 10 || env.Data.Total != 40 || env.Data.Model != "bge-m3:latest" {
				t.Errorf("data %+v", env.Data)
			}
		})
	}
}

func TestSearchIndexStatusHumanPrintsTheState(t *testing.T) {
	run := runIndexCLI(t, []string{"--human", "search", "index", "status"}, []string{rpc.MethodSearchIndexStatus}, func(string, json.RawMessage) (any, error) { return indexStatusFixture(), nil })
	if run.code != 0 || !strings.Contains(run.stdout, "indexing") || !strings.Contains(run.stdout, "bge-m3:latest") || strings.HasPrefix(strings.TrimSpace(run.stdout), "{") {
		t.Errorf("code %d stdout %q", run.code, run.stdout)
	}
}

func TestSearchIndexStartWhileOffExitsWithUsage(t *testing.T) {
	off := rpc.NewError(rpc.CodeInvalidParams, "semantic search is off: set [semantic_search] enabled = true")
	run := runIndexCLI(t, []string{"search", "index", "start"}, []string{rpc.MethodSearchIndexStart}, func(string, json.RawMessage) (any, error) { return nil, off })
	if run.code != exitcode.Usage || !strings.Contains(run.stdout+run.stderr, "semantic search is off") {
		t.Errorf("code %d stdout %q stderr %q, want exit %d with the explanation", run.code, run.stdout, run.stderr, exitcode.Usage)
	}
}

// A query that is the word "index" is still searchable: -- ends the options and
// the word is an argument, not the subcommand.
func TestSearchStillFindsTheWordIndexAfterTheDoubleDash(t *testing.T) {
	run := runIndexCLI(t, []string{"search", "--", "index"}, []string{rpc.MethodSearch}, func(string, json.RawMessage) (any, error) {
		return rpc.SearchResult{Hits: []rpc.SearchHit{}}, nil
	})
	if run.code != 0 || len(run.methods) != 1 || run.methods[0] != rpc.MethodSearch {
		t.Fatalf("code %d methods %v stderr %q", run.code, run.methods, run.stderr)
	}
	var p rpc.SearchParams
	if err := json.Unmarshal(run.params[0], &p); err != nil || p.Query != "index" {
		t.Errorf("query %q, %v", p.Query, err)
	}
}

func TestSearchWithAQueryStillWorks(t *testing.T) {
	run := runIndexCLI(t, []string{"search", "--scope", "messages", "доставка"}, []string{rpc.MethodSearch}, func(string, json.RawMessage) (any, error) {
		return rpc.SearchResult{Hits: []rpc.SearchHit{}}, nil
	})
	if run.code != 0 || len(run.methods) != 1 {
		t.Fatalf("code %d methods %v stderr %q", run.code, run.methods, run.stderr)
	}
}
