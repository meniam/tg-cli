package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// The commands that read the local database print the transcription beside the
// message, and a message without one prints no such key.
func TestContextAndExportPrintTheTranscription(t *testing.T) {
	voice := rpc.Message{ID: 7, ChatID: 42, Date: "2026-11-21T10:00:00Z", MediaType: "voice"}
	plain := rpc.Message{ID: 8, ChatID: 42, Date: "2026-11-21T10:01:00Z", Text: "ok"}
	text := &rpc.MessageTranscription{Text: "привет, как дела", Source: "local", Engine: "gigaam-v3"}

	r := runDiscoveryCLI(t, []string{"message", "context", "42", "7", "--local", "--format", "jsonl"}, func(string, json.RawMessage) (any, error) {
		return rpc.ContextResult{Messages: []rpc.ContextMessage{{Message: voice, Transcription: text}, {Message: plain}}}, nil
	}, nil)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != exitcode.OK || len(lines) != 2 ||
		!strings.Contains(lines[0], `"transcription":{"text":"привет, как дела","source":"local","engine":"gigaam-v3"}`) || strings.Contains(lines[1], "transcription") {
		t.Fatalf("context: exit %d, %q", r.code, r.stdout)
	}

	exported := runExportCLI(t, []string{"message", "export", "42", "--local"}, func(rpc.ExportParams) (rpc.ExportPage, error) {
		return rpc.ExportPage{Messages: []rpc.ExportMessage{{Message: voice, Transcription: text}, {Message: plain}}}, nil
	}, nil)
	lines = strings.Split(strings.TrimSpace(exported.stdout), "\n")
	if exported.code != exitcode.OK || len(lines) != 2 ||
		!strings.Contains(lines[0], `"transcription":{"text":"привет, как дела","source":"local","engine":"gigaam-v3"}`) || strings.Contains(lines[1], "transcription") {
		t.Fatalf("export: exit %d, %q", exported.code, exported.stdout)
	}

	// The human format frames the text of a transcription as untrusted, like the
	// text of a message.
	human := runExportCLI(t, []string{"message", "export", "42", "--local", "--human"}, func(rpc.ExportParams) (rpc.ExportPage, error) {
		return rpc.ExportPage{Messages: []rpc.ExportMessage{{Message: voice, Transcription: text}}}, nil
	}, nil)
	if human.code != exitcode.OK || !strings.Contains(human.stdout, "untrusted") || !strings.Contains(human.stdout, "привет, как дела") {
		t.Fatalf("export --human: exit %d, %q", human.code, human.stdout)
	}
}
