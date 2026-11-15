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

func TestArchiveConfinementAndCleanup(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDownloadDir([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, err := d.OpenArchive("archive", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := d.OpenArchive("archive", true); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing %v", err)
	}
	size, hash, err := a.Write(context.Background(), "data", 4, func(w io.Writer) error { _, err := io.WriteString(w, "data"); return err })
	expected := sha256.Sum256([]byte("data"))
	if err != nil || size != 4 || hash != hex.EncodeToString(expected[:]) {
		t.Fatalf("write %d %s %v", size, hash, err)
	}
	if _, _, err = a.Write(context.Background(), "large", 2, func(w io.Writer) error { _, err := io.WriteString(w, "long"); return err }); err == nil {
		t.Fatal("size limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = a.Write(ctx, "cancel", 4, func(w io.Writer) error { _, err := io.WriteString(w, "data"); return err }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel %v", err)
	}
	for _, name := range []string{"large", ".large.part", "cancel", ".cancel.part"} {
		if _, err := os.Stat(filepath.Join(root, "archive", name)); !os.IsNotExist(err) {
			t.Fatalf("left %s: %v", name, err)
		}
	}
	if info, err := os.Stat(filepath.Join(root, "archive")); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(root, "archive", "data")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode %v %v", info, err)
	}
	if err = os.Symlink(filepath.Join(root, "archive", "data"), filepath.Join(root, "archive", "link")); err != nil {
		t.Fatal(err)
	}
	if f, err := a.Open("link"); err == nil {
		f.Close()
		t.Fatal("opened symlink")
	}
	if err = os.Remove(filepath.Join(root, "archive", "link")); err != nil {
		t.Fatal(err)
	}
	if err = a.RemoveOwned(); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(root, "archive")); err != nil {
		t.Fatal(err)
	}
	if a, err := d.OpenArchive("archive", false); err == nil {
		a.Close()
		t.Fatal("opened archive symlink")
	}
}

func TestArchiveCleanupDoesNotRemoveReplacement(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDownloadDir([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, err := d.OpenArchive("archive", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = os.Rename(filepath.Join(root, "archive"), filepath.Join(root, "original")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "archive"), 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "archive", "keep")
	if err = os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.RemoveOwned(); err == nil {
		t.Fatal("accepted replaced directory")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatalf("replacement touched: %q %v", data, err)
	}
}

func TestArchiveRejectsReplacedOutputDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(root, "output")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDownloadDir([]string{root}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = os.Rename(path, filepath.Join(root, "original")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if a, err := d.OpenArchive("archive", true); err == nil {
		a.Close()
		t.Fatal("accepted replaced output")
	}
	if _, err = os.Stat(filepath.Join(outside, "archive")); !os.IsNotExist(err) {
		t.Fatalf("wrote outside root: %v", err)
	}
}

func TestArchiveStopsWritingAfterDirectoryMove(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(root, "output")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDownloadDir([]string{root}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, err := d.OpenArchive("archive", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = os.Rename(path, filepath.Join(outside, "moved")); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Write(context.Background(), "file", 10, func(w io.Writer) error { _, err := io.WriteString(w, "data"); return err })
	if err == nil {
		t.Fatal("wrote after directory moved outside root")
	}
	if _, err = os.Stat(filepath.Join(outside, "moved", "archive", "file")); !os.IsNotExist(err) {
		t.Fatalf("file exists: %v", err)
	}
}

func TestArchiveRejectsSymlinkInRequestedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	d, err := OpenArchiveDownloadDir([]string{root}, link)
	if err == nil {
		d.Close()
		t.Fatal("accepted existing symlink in output path")
	}
}

func TestArchiveSwapOutputForSymlinkDuringWrite(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	dir := filepath.Join(root, "output")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenArchiveDownloadDir([]string{root}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, err := d.OpenArchive("archive", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = os.Mkdir(filepath.Join(outside, "archive"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Write(context.Background(), "manifest.json", 1024, func(w io.Writer) error {
		if err := os.Rename(dir, filepath.Join(root, "old-output")); err != nil {
			return err
		}
		if err := os.Symlink(outside, dir); err != nil {
			return err
		}
		_, err := io.WriteString(w, "manifest")
		return err
	})
	if err == nil {
		t.Fatal("published through replacement symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "archive", "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("write escaped root: %v", err)
	}
}

func TestArchiveRejectsInternalSymlinkSwapDuringWrite(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "output")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenArchiveDownloadDir([]string{root}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, err := d.OpenArchive("archive", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	other := filepath.Join(root, "other")
	if err = os.MkdirAll(filepath.Join(other, "archive"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Write(context.Background(), "manifest.json", 100, func(w io.Writer) error {
		if err := os.Rename(dir, filepath.Join(root, "old-output")); err != nil {
			return err
		}
		if err := os.Symlink("other", dir); err != nil {
			return err
		}
		_, err := io.WriteString(w, "manifest")
		return err
	})
	if err == nil {
		t.Fatal("published through an internal symlink")
	}
	if _, err = os.Stat(filepath.Join(other, "archive", "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("wrote through symlink: %v", err)
	}
}
