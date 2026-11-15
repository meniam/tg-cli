package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"tg-cli/internal/rpc"
)

func TestExportMonthCLI(t *testing.T) {
	fixture := rpc.ExportMonthResult{Dir: "/tmp/tg-export-42-2023-11", Month: "2023-11", Files: []rpc.ExportMonthFile{{Name: "42.jsonl", ChatID: 42, Messages: 2}}}
	r := runDiscoveryCLI(t, []string{"message", "export-month", "42", "--month", "2023-11", "--output", "/tmp", "--include-deleted", "--format", "jsonl"}, func(method string, raw json.RawMessage) (any, error) {
		var p rpc.ExportMonthParams
		if err := json.Unmarshal(raw, &p); err != nil || p.Month != "2023-11" || p.Output != "/tmp" || !p.IncludeDeleted || len(p.Chats) != 1 {
			t.Errorf("request: %+v %v", p, err)
		}
		return fixture, nil
	}, nil)
	if r.code != 0 || r.method != rpc.MethodMessageExportMonth || !strings.Contains(r.stdout, `"name":"42.jsonl"`) {
		t.Fatalf("result: %+v", r)
	}
	r = runDiscoveryCLI(t, []string{"message", "export-verify", "/tmp/archive"}, func(string, json.RawMessage) (any, error) {
		return rpc.ExportVerifyResult{OK: false, Files: []rpc.ExportVerifyFile{}}, nil
	}, nil)
	if r.code != 1 || !strings.Contains(r.stdout, `"ok":false`) {
		t.Fatalf("verify: %+v", r)
	}
	for _, args := range [][]string{{"message", "export-month", "42", "--month", "2023-1", "--output", "/tmp"}, {"message", "export-month", "42", "--month", "2023-11", "--output", "relative"}, {"message", "export-verify", "relative"}} {
		r = runDiscoveryCLI(t, args, nil, nil)
		if r.code != 2 || r.method != "" {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}
