package fileroot

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadDirConfinesAndDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if d, err := OpenDownloadDir([]string{root}, filepath.Join(root, "escape")); err == nil {
		d.Close()
		t.Fatal("outside accepted")
	}
	d, err := OpenDownloadDir([]string{root}, sub)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	write := func(w io.Writer) error { _, err := io.WriteString(w, "data"); return err }
	if _, err = d.Write("file", 4, write); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Write("file", 4, write); err == nil {
		t.Fatal("overwrote file")
	}
	if _, err = d.Write("../escape/file", 4, write); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err = d.Write("big", 3, write); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, err = os.Stat(filepath.Join(root, ".big.part")); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(sub, "file"))
	if err != nil || strings.TrimSpace(string(got)) != "data" {
		t.Fatalf("file=%q err=%v", got, err)
	}
}

func TestDownloadDirStaysOnOpenedDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	original := filepath.Join(root, "dir")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDownloadDir([]string{root}, original)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = os.Rename(original, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, original); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Write("test", 10, func(w io.Writer) error { _, err := io.WriteString(w, "x"); return err }); err == nil {
		t.Fatal("accepted replaced output directory")
	}
	if _, err = os.Stat(filepath.Join(outside, "test")); !os.IsNotExist(err) {
		t.Fatalf("escaped root: %v", err)
	}
}

func TestDownloadDirectoryBoundaries(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		ok   bool
	}{{root, true}, {nested, true}, {link, true}, {file, false}, {filepath.Join(root, "missing"), false}, {"relative", false}} {
		d, err := OpenDownloadDir([]string{root}, tc.path)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if d != nil {
			_ = d.Close()
		}
	}
	d, err := OpenDownloadDir([]string{root}, nested)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = d.Write("cancel", 10, func(w io.Writer) error { _, _ = w.Write([]byte("x")); return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".cancel.part")); !os.IsNotExist(err) {
		t.Fatalf("part remains: %v", err)
	}
	if _, err = d.Write("mode", 10, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(nested, "mode"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, err)
	}
}
