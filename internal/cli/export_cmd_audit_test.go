package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// TestAudit02_ExportRefusesBadPagesFromTheDaemon checks the guards of the page
// loop: each bad page ends the export with a failure after a bounded number of
// calls, instead of looping or printing out-of-order data.
func TestAudit02_ExportRefusesBadPagesFromTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		pages []rpc.ExportPage
		want  string
		calls int
	}{
		{"empty page with a cursor", nil, []rpc.ExportPage{{NextAfter: 5}}, "empty export page with a cursor", 1},
		{"messages out of order", nil, []rpc.ExportPage{{Messages: []rpc.ExportMessage{exportMessage(3, false), exportMessage(2, false)}}}, "messages out of order", 1},
		{"first message not after the cursor", []string{"--after", "5"}, []rpc.ExportPage{{Messages: []rpc.ExportMessage{exportMessage(5, false)}}}, "messages out of order", 1},
		{"cursor is not the last id", nil, []rpc.ExportPage{{Messages: []rpc.ExportMessage{exportMessage(1, false), exportMessage(2, false)}, NextAfter: 1}}, "invalid export cursor", 1},
		{"second page repeats the first", nil, []rpc.ExportPage{
			{Messages: []rpc.ExportMessage{exportMessage(1, false)}, NextAfter: 1},
			{Messages: []rpc.ExportMessage{exportMessage(1, false)}, NextAfter: 1},
		}, "messages out of order", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := 0
			r := runExportCLI(t, append([]string{"message", "export", "42", "--local"}, tc.args...), func(rpc.ExportParams) (rpc.ExportPage, error) {
				if next >= len(tc.pages) {
					return rpc.ExportPage{}, rpc.NewError(rpc.CodeInternal, "the CLI asked for more pages than the fixture has")
				}
				next++
				return tc.pages[next-1], nil
			}, nil)
			if r.code != exitcode.Failure || len(r.params) != tc.calls || !strings.Contains(r.stdout, tc.want) {
				t.Fatalf("want a failure with %q after %d call(s), got %+v", tc.want, tc.calls, r)
			}
		})
	}
}

// TestAudit02_MembersAllRefusesANonGrowingOffset stops a daemon that keeps
// answering with the same offset, and forwards --search and --filter.
func TestAudit02_MembersAllRefusesANonGrowingOffset(t *testing.T) {
	var offsets []int
	r := runDiscoveryCLI(t, []string{"chat", "members", "42", "--all", "--format", "jsonl", "--search", "an", "--filter", "admins"}, func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.MembersParams
		if err := json.Unmarshal(raw, &p); err != nil || p.Chat != "42" || p.Search != "an" || p.Filter != "admins" || p.Limit != 200 {
			return audit02Reject(t, "members params: %+v %v", p, err)
		}
		offsets = append(offsets, p.Offset)
		if len(offsets) > 5 { // a CLI that does not stop must fail the test, not hang it
			return rpc.MembersResult{}, nil
		}
		return rpc.MembersResult{Members: []rpc.Member{{ID: int64(len(offsets))}}, NextOffset: 200}, nil
	}, nil)
	if r.code != exitcode.Failure || !strings.Contains(r.stdout, "invalid members offset") || fmt.Sprint(offsets) != "[0 200]" {
		t.Fatalf("offsets %v: %+v", offsets, r)
	}
	r = runDiscoveryCLI(t, []string{"chat", "members", "42", "--all", "--format", "jsonl", "--filter", "nobody"}, nil, nil)
	if r.code != exitcode.Usage || r.method != "" || !strings.Contains(r.stdout, "--filter must be recent, admins, or bots") {
		t.Fatalf("bad filter: %+v", r)
	}
}

// TestAudit02_DownloadLimitsAndPartialResult covers the 20 id cap and the exit
// code of a batch that downloaded only some of the files.
func TestAudit02_DownloadLimitsAndPartialResult(t *testing.T) {
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = fmt.Sprint(i + 1)
	}
	r := runDiscoveryCLI(t, append([]string{"message", "download", "42"}, append(ids, "--output", "/tmp")...), nil, nil)
	if r.code != exitcode.Usage || r.method != "" || !strings.Contains(r.stdout, "provide at most 20 message ids") {
		t.Fatalf("21 ids: %+v", r)
	}
	r = runDiscoveryCLI(t, append([]string{"message", "download", "42"}, append(ids[:20], "--output", "/tmp")...), func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.DownloadParams
		if err := json.Unmarshal(raw, &p); err != nil || len(p.IDs) != 20 {
			return audit02Reject(t, "download params: %+v %v", p, err)
		}
		return rpc.DownloadResult{ChatID: 42, Files: []rpc.DownloadedFile{{MessageID: 1, Path: "/tmp/42_1.jpg"}}}, nil
	}, nil)
	if r.code != exitcode.OK || r.method != rpc.MethodMessageDownload {
		t.Fatalf("20 ids: %+v", r)
	}
	r = runDiscoveryCLI(t, []string{"message", "download", "42", "1", "2", "--output", "/tmp"}, func(string, json.RawMessage) (any, error) {
		return rpc.DownloadResult{Batch: rpc.Batch{Partial: true, Errors: []rpc.ItemError{{ID: 2, Code: "not_found"}}}, ChatID: 42, Files: []rpc.DownloadedFile{{MessageID: 1, Path: "/tmp/42_1.jpg"}}}, nil
	}, nil)
	if r.code != exitcode.Failure || !strings.Contains(r.stdout, `"message_id":1`) || !strings.Contains(r.stdout, `"partial":true`) {
		t.Fatalf("partial: the result must be printed and the exit code must be failure: %+v", r)
	}
}
