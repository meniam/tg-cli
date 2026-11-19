package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// audit02Envelope splits a JSON result into the envelope flag and the data.
func audit02Envelope(t *testing.T, stdout string) (bool, json.RawMessage) {
	t.Helper()
	var envelope struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %q: %v", stdout, err)
	}
	return envelope.OK, envelope.Data
}

// audit02CompareWithSchema fails when the response has a path the schema of the
// command does not declare, or has no fields at all.
func audit02CompareWithSchema(t *testing.T, command string, data json.RawMessage, wantPaths ...string) {
	t.Helper()
	paths, err := schema.Paths(data)
	if err != nil {
		t.Fatal(err)
	}
	if missing := schema.Missing(paths, schemaCommands(t)[command].Fields); len(missing) > 0 {
		t.Fatalf("%s: the response has %v, the schema does not", command, missing)
	}
	have := map[string]bool{}
	for _, path := range paths {
		have[path] = true
	}
	for _, want := range wantPaths {
		if !have[want] {
			t.Fatalf("%s: the response fixture has no %q, the comparison would be empty for it (paths %v)", command, want, paths)
		}
	}
}

func TestExportMonthCLI(t *testing.T) {
	fixture := rpc.ExportMonthResult{Dir: "/tmp/tg-export-42-2023-11", Month: "2023-11", Files: []rpc.ExportMonthFile{{Name: "42.jsonl", ChatID: 42, Messages: 2, Bytes: 10, SHA256: "ab", MonthStartCovered: true}}}
	args := []string{"message", "export-month", "42", "--month", "2023-11", "--output", "/tmp", "--include-deleted"}
	r := runDiscoveryCLI(t, args, func(method string, raw json.RawMessage) (any, error) {
		var p rpc.ExportMonthParams
		if err := json.Unmarshal(raw, &p); err != nil || p.Month != "2023-11" || p.Output != "/tmp" || !p.IncludeDeleted || len(p.Chats) != 1 || p.Chats[0] != "42" {
			return audit02Reject(t, "request: %+v %v", p, err)
		}
		return fixture, nil
	}, nil)
	if r.code != exitcode.OK || r.method != rpc.MethodMessageExportMonth {
		t.Fatalf("result: %+v", r)
	}
	ok, data := audit02Envelope(t, r.stdout)
	if !ok {
		t.Fatalf("envelope is not ok: %s", r.stdout)
	}
	audit02CompareWithSchema(t, "message export-month", data, "dir", "month", "files.name", "files.chat_id", "files.messages")
	var got rpc.ExportMonthResult
	if err := json.Unmarshal(data, &got); err != nil || got.Month != "2023-11" || len(got.Files) != 1 || got.Files[0].Name != "42.jsonl" {
		t.Fatalf("data: %s %v", data, err)
	}
	r = runDiscoveryCLI(t, append(args, "--format", "jsonl"), func(string, json.RawMessage) (any, error) { return fixture, nil }, nil)
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"name":"42.jsonl"`) {
		t.Fatalf("jsonl: %+v", r)
	}
	for _, args := range [][]string{{"message", "export-month", "42", "--month", "2023-1", "--output", "/tmp"}, {"message", "export-month", "42", "--month", "2023-11", "--output", "relative"}, {"message", "export-verify", "relative"}} {
		r = runDiscoveryCLI(t, args, nil, nil)
		if r.code != exitcode.Usage || r.method != "" {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}

// TestAudit02_ExportVerifyCLI separates the envelope flag (the call worked) from the
// result flag data.ok (the archive is not intact): both keys are named ok.
func TestAudit02_ExportVerifyCLI(t *testing.T) {
	fixture := rpc.ExportVerifyResult{Dir: "/tmp/archive", Month: "2023-11", OK: false, Files: []rpc.ExportVerifyFile{{Name: "42.jsonl", OK: false, ExpectedSHA256: "ab", ActualSHA256: "cd", Bytes: 10}}, Missing: []string{"43.jsonl"}, Extra: []string{}}
	r := runDiscoveryCLI(t, []string{"message", "export-verify", "/tmp/archive"}, func(string, json.RawMessage) (any, error) { return fixture, nil }, nil)
	if r.code != exitcode.Failure || r.method != rpc.MethodMessageExportVerify {
		t.Fatalf("verify: %+v", r)
	}
	var p rpc.ExportVerifyParams
	if err := json.Unmarshal(r.params, &p); err != nil || p.Dir != "/tmp/archive" {
		t.Fatalf("params: %s %v", r.params, err)
	}
	ok, data := audit02Envelope(t, r.stdout)
	if !ok {
		t.Fatalf("the call worked, the envelope must be ok: %s", r.stdout)
	}
	audit02CompareWithSchema(t, "message export-verify", data, "dir", "month", "ok", "files.name", "files.ok", "missing")
	var got rpc.ExportVerifyResult
	if err := json.Unmarshal(data, &got); err != nil || got.OK || len(got.Files) != 1 {
		t.Fatalf("data: %s %v", data, err)
	}
	fixture.OK, fixture.Files, fixture.Missing = true, []rpc.ExportVerifyFile{{Name: "42.jsonl", OK: true}}, nil
	r = runDiscoveryCLI(t, []string{"message", "export-verify", "/tmp/archive"}, func(string, json.RawMessage) (any, error) { return fixture, nil }, nil)
	if r.code != exitcode.OK {
		t.Fatalf("intact archive: %+v", r)
	}
	if ok, data = audit02Envelope(t, r.stdout); !ok || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("intact archive data: %s", r.stdout)
	}
}
