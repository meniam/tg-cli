package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func TestDownloadResponseFixture(t *testing.T) {
	fixture := rpc.DownloadResult{ChatID: 42, Files: []rpc.DownloadedFile{{MessageID: 7, Path: "/tmp/42_7.jpg", Size: 3, MediaType: "photo", MIME: "image/jpeg", FileName: "foreign.jpg"}}}
	r := runDiscoveryCLI(t, []string{"message", "download", "42", "7", "--output", "/tmp"}, func(_ string, _ json.RawMessage) (any, error) { return fixture, nil }, nil)
	if r.code != 0 || r.method != rpc.MethodMessageDownload || !strings.Contains(r.stdout, `"file_name":"foreign.jpg"`) {
		t.Fatalf("response: %+v", r)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	fields := schemaCommands(t)["message download"].Fields
	paths, err := schema.Paths(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	if missing := schema.Missing(paths, fields); len(missing) > 0 {
		t.Fatalf("missing schema fields: %v", missing)
	}
	r = runDiscoveryCLI(t, []string{"message", "download", "42", "7", "--output", "/tmp", "--format", "jsonl"}, func(_ string, _ json.RawMessage) (any, error) { return fixture, nil }, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"message_id":7`) {
		t.Fatalf("jsonl: %+v", r)
	}
}

func TestDownloadArgumentsAndValidation(t *testing.T) {
	for _, args := range [][]string{{"message", "download", "42", "7"}, {"message", "download", "42", "7", "--output", "relative"}, {"message", "download", "42", "0", "--output", "/tmp"}, {"message", "download", "bad-link", "--output", "/tmp"}} {
		r := runDiscoveryCLI(t, args, nil, nil)
		if r.code != 2 || r.method != "" {
			t.Fatalf("args %v: %+v", args, r)
		}
	}
	r := runDiscoveryCLI(t, []string{"message", "download", "https://t.me/example/7", "--output", "/tmp", "--dry-run"}, func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.DownloadParams
		if err := json.Unmarshal(raw, &p); err != nil || p.Chat != "@example" || len(p.IDs) != 1 || p.IDs[0] != 7 || !p.DryRun {
			t.Errorf("params %+v error %v", p, err)
		}
		return rpc.DownloadResult{Files: []rpc.DownloadedFile{}, DryRun: true}, nil
	}, nil)
	if r.code != 0 {
		t.Fatalf("link: %+v", r)
	}
}
