package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type downloadFake struct {
	*writeTelegram
	downloads int
	fetches   int
	fetchErr  error
	size      int64
}

func (f *downloadFake) Download(_ context.Context, _ chatref.Ref, ids []int, visit func(rpc.DownloadedFile, func(io.Writer) error) error) (int64, []rpc.ItemError, error) {
	f.downloads++
	var failures []rpc.ItemError
	size := f.size
	if size == 0 {
		size = 4
	}
	for _, id := range ids {
		err := visit(rpc.DownloadedFile{MessageID: id, Size: size, MediaType: "photo", MIME: "image/jpeg"}, func(w io.Writer) error {
			f.fetches++
			_, err := io.WriteString(w, "test")
			if err != nil {
				return err
			}
			return f.fetchErr
		})
		if err != nil {
			failures = append(failures, rpc.ItemErrorFrom(id, err))
		}
	}
	return 42, failures, nil
}
func TestDownloadAccessAndDryRun(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	f := &downloadFake{writeTelegram: base}
	base.resolved = &rpc.Chat{ID: 42}
	d.opts.Telegram = f
	root := t.TempDir()
	p := rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: root}
	_, err := d.messageDownload(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeWriteDisabled || base.resolveCalls != 0 || f.downloads != 0 {
		t.Fatalf("disabled: %v", err)
	}
	b, _ := json.Marshal(map[string]any{"downloads": []string{root}})
	writeAccess(t, path, string(b))
	p.Output = t.TempDir()
	_, err = d.messageDownload(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeInvalidParams || base.resolveCalls != 0 {
		t.Fatalf("outside: %v", err)
	}
	p.Output = root
	p.DryRun = true
	v, err := d.messageDownload(context.Background(), params(t, p))
	if err != nil || len(v.(rpc.DownloadResult).Files) != 1 {
		t.Fatalf("dry run: %v %v", v, err)
	}
	if _, err = os.Stat(filepath.Join(root, "42_1.jpg")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote file: %v", err)
	}
	p.DryRun = false
	_, err = d.messageDownload(context.Background(), params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(root, "42_1.jpg"))
	if err != nil || string(b) != "test" {
		t.Fatalf("file %q: %v", b, err)
	}
	_, err = d.messageDownload(context.Background(), params(t, p))
	if err == nil {
		t.Fatal("overwrite accepted")
	}
}

func TestDownloadTotalLimit(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	f := &downloadFake{writeTelegram: base, size: 100 * 1024 * 1024}
	base.resolved = &rpc.Chat{ID: 42}
	d.opts.Telegram = f
	root := t.TempDir()
	b, _ := json.Marshal(map[string]any{"downloads": []string{root}})
	writeAccess(t, path, string(b))
	v, err := d.messageDownload(context.Background(), params(t, rpc.DownloadParams{Chat: "42", IDs: []int{1, 2, 3, 4, 5, 6}, Output: root, DryRun: true}))
	if err != nil {
		t.Fatal(err)
	}
	got := v.(rpc.DownloadResult)
	if !got.Partial || len(got.Files) != 5 || len(got.Errors) != 1 || got.Errors[0].ID != 6 {
		t.Fatalf("result: %+v", got)
	}
}

func TestDownloadRejectsClosedAndOversized(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	f := &downloadFake{writeTelegram: base, size: 100*1024*1024 + 1}
	base.resolved = &rpc.Chat{ID: 42}
	d.opts.Telegram = f
	root := t.TempDir()
	b, _ := json.Marshal(map[string]any{"downloads": []string{root}, "block": []int64{42}})
	writeAccess(t, path, string(b))
	p := rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: root}
	_, err := d.messageDownload(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeNotFound || f.downloads != 0 {
		t.Fatalf("closed: %v calls %d", err, f.downloads)
	}
	b, _ = json.Marshal(map[string]any{"downloads": []string{root}})
	writeAccess(t, path, string(b))
	_, err = d.messageDownload(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeInvalidParams || f.fetches != 0 {
		t.Fatalf("oversized: %v, fetches=%d", err, f.fetches)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("files: %v %v", entries, err)
	}
}

func TestDownloadFloodRemovesPartial(t *testing.T) {
	d, base, _, path := writeDaemon(t)
	f := &downloadFake{writeTelegram: base, fetchErr: &rpc.Error{Code: rpc.CodeFloodWait, Message: "wait", Data: map[string]any{"retry_after_seconds": 30}}}
	base.resolved = &rpc.Chat{ID: 42}
	d.opts.Telegram = f
	root := t.TempDir()
	b, _ := json.Marshal(map[string]any{"downloads": []string{root}})
	writeAccess(t, path, string(b))
	_, err := d.messageDownload(context.Background(), params(t, rpc.DownloadParams{Chat: "42", IDs: []int{1}, Output: root}))
	if writeCode(t, err) != rpc.CodeFloodWait || f.fetches != 1 {
		t.Fatalf("error=%v fetches=%d", err, f.fetches)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("remaining files=%v error=%v", entries, err)
	}
}
