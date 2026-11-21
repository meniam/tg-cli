package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func TestSendMediaCLIValidatesBeforeDaemon(t *testing.T) {
	base := []string{"message", "send-media", "42"}
	eleven := append(append([]string{}, base...), "--as", "photo")
	for range 11 {
		eleven = append(eleven, "--file", "a.png")
	}
	cases := []struct {
		name string
		args []string
	}{
		{"missing as", append(append([]string{}, base...), "--file", "a.png")},
		{"missing file", append(append([]string{}, base...), "--as", "photo")},
		{"invalid as", append(append([]string{}, base...), "--as", "gif", "--file", "a.gif")},
		{"empty file", append(append([]string{}, base...), "--as", "photo", "--file", "")},
		{"voice album", append(append([]string{}, base...), "--as", "voice", "--file", "a.ogg", "--file", "b.ogg")},
		{"eleven", eleven},
		{"caption 1025", append(append([]string{}, base...), "--as", "photo", "--file", "a.png", "--caption", strings.Repeat("я", 1025))},
		{"negative duration", append(append([]string{}, base...), "--as", "photo", "--file", "a.png", "--duration", "-1")},
		{"negative width", append(append([]string{}, base...), "--as", "photo", "--file", "a.png", "--width", "-1")},
		{"negative height", append(append([]string{}, base...), "--as", "photo", "--file", "a.png", "--height", "-1")},
		{"negative reply", append(append([]string{}, base...), "--as", "photo", "--file", "a.png", "--reply-to", "-1")},
		{"negative topic", append(append([]string{}, base...), "--as", "photo", "--file", "a.png", "--topic", "-1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, tc.args, "unused stdin", nil, nil)
			if r.code != exitcode.Usage || r.method != "" {
				t.Fatalf("validation: %+v", r)
			}
		})
	}
}

func TestSendMediaCLIArgumentsAndFormats(t *testing.T) {
	first, err := filepath.Abs("relative-a.png")
	if err != nil {
		t.Fatal(err)
	}
	second, err := filepath.Abs("relative-b.png")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"message", "send-media", "--as", "photo", "--file", "relative-a.png", "--file", "relative-b.png",
		"--caption", "caption", "--reply-to", "7", "--topic", "8", "--silent", "--duration", "9", "--width", "10", "--height", "11", "--", "-1001234567890"}
	r := runWriteCLI(t, args, "ignored stdin", func(_ string, _ json.RawMessage) (any, error) {
		return rpc.SendMediaResult{ChatID: -1001234567890, Messages: []rpc.MessageRef{{ID: 11, Date: "2026-11-14T10:00:00Z"}, {ID: 12, Date: "2026-11-14T10:00:01Z"}}}, nil
	}, nil)
	if r.code != 0 || r.method != rpc.MethodMessageSendMedia || !strings.Contains(r.stderr, "sent 2 message(s): 11, 12") {
		t.Fatalf("send: %+v", r)
	}
	var p rpc.SendMediaParams
	if err := json.Unmarshal(r.params, &p); err != nil || p.Chat != "-1001234567890" || p.As != "photo" || !reflect.DeepEqual(p.Files, []string{first, second}) || p.Caption != "caption" || p.ReplyTo != 7 || p.Topic != 8 || !p.Silent || p.Duration != 9 || p.Width != 10 || p.Height != 11 {
		t.Fatalf("params: %+v err=%v", p, err)
	}
	var envelope struct {
		OK   bool                `json:"ok"`
		Data rpc.SendMediaResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil || !envelope.OK || len(envelope.Data.Messages) != 2 {
		t.Fatalf("envelope: %s err=%v", r.stdout, err)
	}
	for _, format := range []string{"jsonl", "toon", "human"} {
		t.Run(format, func(t *testing.T) {
			args := []string{"message", "send-media", "42", "--as", "document", "--file", first, "--format", format}
			result := runWriteCLI(t, args, "", nil, nil)
			if result.code != 0 || result.stdout == "" {
				t.Fatalf("format %s: %+v", format, result)
			}
			if format == "jsonl" {
				var item rpc.MessageRef
				if err := json.Unmarshal([]byte(strings.TrimSpace(result.stdout)), &item); err != nil || item.ID != 11 {
					t.Fatalf("jsonl: %s %v", result.stdout, err)
				}
			}
		})
	}
}

func TestSendMediaCLIDryRunAndHints(t *testing.T) {
	base := []string{"message", "send-media", "42", "--as", "document", "--file", "a.bin"}
	dry := runWriteCLI(t, append(append([]string{}, base...), "--dry-run"), "ignored", func(_ string, raw json.RawMessage) (any, error) {
		var p rpc.SendMediaParams
		if err := json.Unmarshal(raw, &p); err != nil || !p.DryRun || p.Caption != "" {
			t.Errorf("dry-run params: %+v %v", p, err)
		}
		return rpc.SendMediaResult{ChatID: 42, Messages: []rpc.MessageRef{}, DryRun: true, Action: "send_media", Count: 1, Types: []string{"document"}, Sizes: []int64{7}}, nil
	}, nil)
	if dry.code != 0 || !strings.Contains(dry.stdout, `"dry_run":true`) || !strings.Contains(dry.stdout, `"sizes":[7]`) || !strings.Contains(dry.stderr, "no files were sent") {
		t.Fatalf("dry run: %+v", dry)
	}
	quiet := runWriteCLI(t, append(append([]string{}, base...), "--quiet"), "", nil, nil)
	if quiet.code != 0 || strings.Contains(quiet.stderr, "sent 1 message") {
		t.Fatalf("quiet: %+v", quiet)
	}
	for _, tc := range []struct {
		name string
		err  error
		code int
		hint string
	}{
		{"files disabled", rpc.NewError(rpc.CodeWriteDisabled, "sending files is disabled: ask the owner to set access.files"), 9, "ask the owner"},
		{"chat disabled", rpc.ErrWriteDisabled(), 9, "access.write"},
		{"not found", rpc.NewError(rpc.CodeNotFound, "file not found"), 7, ""},
		{"telegram", rpc.NewError(rpc.CodeTelegram, "MEDIA_INVALID"), 5, ""},
		{"flood", &rpc.Error{Code: rpc.CodeFloodWait, Message: "FLOOD_WAIT", Data: map[string]any{"retry_after_seconds": 2}}, 6, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, base, "", func(string, json.RawMessage) (any, error) { return nil, tc.err }, nil)
			if r.code != tc.code || (tc.hint != "" && !strings.Contains(r.stderr, tc.hint)) {
				t.Fatalf("error mapping: %+v", r)
			}
			if tc.name == "files disabled" && !strings.Contains(r.stderr, "access.files") {
				t.Fatalf("file access hint: %+v", r)
			}
		})
	}
	filesQuiet := runWriteCLI(t, append(append([]string{}, base...), "--quiet"), "", func(string, json.RawMessage) (any, error) {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, "sending files is disabled: ask the owner to set access.files")
	}, nil)
	if filesQuiet.code != 9 || strings.Contains(filesQuiet.stderr, "ask the owner") {
		t.Fatalf("quiet files hint: %+v", filesQuiet)
	}
}

func TestSendMediaCLITimeoutAndSchema(t *testing.T) {
	a := &app{deps: Deps{CallTimeout: defaultCallTimeout}}
	if a.callTimeout(rpc.MethodMessageSendMedia) != 600*time.Second || a.callTimeout(rpc.MethodMessageSend) != 120*time.Second {
		t.Fatalf("timeouts: media=%s send=%s", a.callTimeout(rpc.MethodMessageSendMedia), a.callTimeout(rpc.MethodMessageSend))
	}
	cmd := schemaCommands(t)["message send-media"]
	if cmd.Safety != "account_write" || cmd.Dangerous || cmd.Result != "rpc.SendMediaResult" || cmd.Item == nil || cmd.Item.Path != "messages" || !reflect.DeepEqual(cmd.Formats, []string{"json", "jsonl", "toon", "human"}) {
		t.Fatalf("schema: %+v", cmd)
	}
	flags := map[string]flagDoc{}
	for _, flag := range cmd.Flags {
		flags[flag.Name] = flag
	}
	for _, name := range []string{"as", "file", "caption", "reply-to", "topic", "silent", "dry-run", "duration", "width", "height"} {
		if _, ok := flags[name]; !ok {
			t.Errorf("missing flag %s", name)
		}
	}
	if !reflect.DeepEqual(flags["as"].Values, mediaTypes) || !flags["file"].Repeatable || flags["file"].Type != "stringArray" {
		t.Fatalf("media flag schema: %+v %+v", flags["as"], flags["file"])
	}
	r := runWriteCLI(t, []string{"message", "send-media", "42", "--as", "photo", "--file", "a.png"}, "", nil, nil)
	if r.code != 0 {
		t.Fatalf("response: %+v", r)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	paths, err := schema.Paths(envelope.Data)
	if err != nil || len(schema.Missing(paths, cmd.Fields)) != 0 {
		t.Fatalf("schema fields: %v %v", paths, err)
	}
	jsonl := runWriteCLI(t, []string{"message", "send-media", "42", "--as", "photo", "--file", "a.png", "--format", "jsonl"}, "", nil, nil)
	if jsonl.code != 0 {
		t.Fatalf("jsonl: %+v", jsonl)
	}
	itemPaths, err := schema.Paths([]byte(strings.TrimSpace(jsonl.stdout)))
	if err != nil || len(schema.Missing(itemPaths, cmd.Item.Fields)) != 0 || !slices.Contains(cmd.Formats, "jsonl") {
		t.Fatalf("item schema: %v %v", itemPaths, err)
	}
}
