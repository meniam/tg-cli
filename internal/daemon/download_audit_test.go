package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tg-cli/internal/rpc"
)

// audit04DownloadSetup builds a daemon that may download into the returned
// directory, with a fake that reports chat 42.
func audit04DownloadSetup(t *testing.T) (*daemon, *downloadFake, string) {
	t.Helper()
	d, base, _, path := writeDaemon(t)
	f := &downloadFake{writeTelegram: base}
	base.resolved = &rpc.Chat{ID: 42}
	d.opts.Telegram = f
	root := t.TempDir()
	b, _ := json.Marshal(map[string]any{"downloads": []string{root}})
	writeAccess(t, path, string(b))
	return d, f, root
}

func audit04Entries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestAudit04_DownloadRejectsInvalidParams covers the validation before any
// Telegram call: the id list, the ids and the output directory.
func TestAudit04_DownloadRejectsInvalidParams(t *testing.T) {
	d, f, root := audit04DownloadSetup(t)
	tooMany := make([]int, 21)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	for _, tc := range []struct {
		name string
		p    rpc.DownloadParams
	}{
		{"no ids", rpc.DownloadParams{Chat: "42", Output: root}},
		{"more than twenty ids", rpc.DownloadParams{Chat: "42", IDs: tooMany, Output: root}},
		{"relative output", rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: "downloads"}},
		{"empty output", rpc.DownloadParams{Chat: "42", IDs: []int{1}}},
		{"negative id", rpc.DownloadParams{Chat: "42", IDs: []int{1, -2}, Output: root}},
		{"zero id", rpc.DownloadParams{Chat: "42", IDs: []int{0}, Output: root}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.messageDownload(context.Background(), params(t, tc.p))
			if code := writeCode(t, err); code != rpc.CodeInvalidParams {
				t.Fatalf("code = %d, want %d (%v)", code, rpc.CodeInvalidParams, err)
			}
		})
	}
	if f.downloads != 0 || f.resolveCalls != 0 {
		t.Errorf("an invalid request reached Telegram: downloads=%d resolves=%d", f.downloads, f.resolveCalls)
	}
	if names := audit04Entries(t, root); len(names) != 0 {
		t.Errorf("an invalid request wrote %v", names)
	}
}

// TestAudit04_DownloadResultBranches covers what the daemon does with the answer of
// the client: a wrong size, another chat, a missing message and a partial result.
func TestAudit04_DownloadResultBranches(t *testing.T) {
	ctx := context.Background()
	t.Run("size does not match", func(t *testing.T) {
		d, f, root := audit04DownloadSetup(t)
		f.payload = "far too long for four bytes"
		_, err := d.messageDownload(ctx, params(t, rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: root}))
		if code := writeCode(t, err); code != rpc.CodeTelegram {
			t.Fatalf("code = %d, want %d (%v)", code, rpc.CodeTelegram, err)
		}
		if names := audit04Entries(t, root); len(names) != 0 {
			t.Errorf("a download of the wrong size left %v", names)
		}
	})
	t.Run("another chat answers", func(t *testing.T) {
		d, f, root := audit04DownloadSetup(t)
		f.chatID = 77
		_, err := d.messageDownload(ctx, params(t, rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: root}))
		sameRPCError(t, err, rpc.ErrChatNotFound())
	})
	t.Run("message is gone", func(t *testing.T) {
		d, f, root := audit04DownloadSetup(t)
		f.fetchErr = rpc.NewError(rpc.CodeNotFound, "message has no media")
		_, err := d.messageDownload(ctx, params(t, rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: root}))
		if code := writeCode(t, err); code != rpc.CodeNotFound {
			t.Fatalf("code = %d, want %d (%v)", code, rpc.CodeNotFound, err)
		}
	})
	t.Run("partial result", func(t *testing.T) {
		d, f, root := audit04DownloadSetup(t)
		f.failID = map[int]error{2: rpc.NewError(rpc.CodeNotFound, "message has no media")}
		got, err := d.messageDownload(ctx, params(t, rpc.DownloadParams{Chat: "42", IDs: []int{1, 2}, Output: root}))
		if err != nil {
			t.Fatal(err)
		}
		result := got.(rpc.DownloadResult)
		if !result.Partial || len(result.Files) != 1 || result.Files[0].MessageID != 1 || len(result.Errors) != 1 || result.Errors[0].ID != 2 {
			t.Fatalf("result = %+v, want message 1 downloaded and message 2 failed", result)
		}
		if names := audit04Entries(t, root); len(names) != 1 || names[0] != filepath.Base(result.Files[0].Path) {
			t.Errorf("files = %v, want only the downloaded one", names)
		}
	})
}
