package fileroot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteParallelIntegrityAndCleanup(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDownloadDir([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	sum, err := d.WriteParallel(context.Background(), "ok", 10, 6, func(w io.Writer) error {
		at := w.(io.WriterAt)
		if _, err := at.WriteAt([]byte("def"), 3); err != nil {
			return err
		}
		_, err := at.WriteAt([]byte("abc"), 0)
		return err
	})
	expected := sha256.Sum256([]byte("abcdef"))
	if err != nil || sum != hex.EncodeToString(expected[:]) {
		t.Fatalf("sum=%s err=%v", sum, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "ok"))
	if err != nil || string(content) != "abcdef" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		expected int64
		write    func(io.Writer) error
	}{
		{"overflow", context.Background(), 1, func(w io.Writer) error { _, err := w.(io.WriterAt).WriteAt([]byte("x"), 10); return err }},
		{"short", context.Background(), 2, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }},
		{"hole", context.Background(), 2, func(w io.Writer) error { _, err := w.(io.WriterAt).WriteAt([]byte("x"), 1); return err }},
		{"canceled", ctx, 1, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }},
		{"ok", context.Background(), 1, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.WriteParallel(tc.ctx, tc.name, 10, tc.expected, tc.write)
			if err == nil {
				t.Fatal("expected failure")
			}
			if tc.name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "."+tc.name+".part")); !os.IsNotExist(err) {
				t.Fatalf("temporary file remains: %v", err)
			}
		})
	}
	content, _ = os.ReadFile(filepath.Join(root, "ok"))
	if string(content) != "abcdef" {
		t.Fatal("existing file changed")
	}
}

func TestWriteParallelDirectoryReplacement(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "child")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDownloadDir([]string{root}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	_, err = d.WriteParallel(context.Background(), "escape", 1, 1, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err })
	if err == nil {
		t.Fatal("escaped root")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Fatalf("outside file: %v", err)
	}
}
