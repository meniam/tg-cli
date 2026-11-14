package fileroot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPathPolicy(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "allowed")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{root, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	insideFile := filepath.Join(root, "file")
	outsideFile := filepath.Join(outside, "file")
	for _, path := range []string{insideFile, outsideFile} {
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fileLink := filepath.Join(root, "file-link")
	dirLink := filepath.Join(root, "dir-link")
	rootLink := filepath.Join(base, "root-link")
	for link, target := range map[string]string{fileLink: outsideFile, dirLink: outside, rootLink: root} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		roots []string
		path  string
		want  error
	}{
		{"inside", []string{root}, insideFile, nil},
		{"dotdot outside", []string{root}, root + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside" + string(filepath.Separator) + "file", ErrOutsideRoots},
		{"absolute outside", []string{root}, outsideFile, ErrOutsideRoots},
		{"relative", []string{root}, "file", ErrNotAbsolute},
		{"file symlink outside", []string{root}, fileLink, ErrOutsideRoots},
		{"directory symlink outside", []string{root}, filepath.Join(dirLink, "file"), ErrOutsideRoots},
		{"root symlink inward", []string{rootLink}, filepath.Join(rootLink, "file"), nil},
		{"missing root", []string{filepath.Join(base, "missing")}, filepath.Join(base, "missing", "file"), ErrOutsideRoots},
		{"missing file", []string{root}, filepath.Join(root, "missing"), ErrNotFound},
		{"directory", []string{root}, root, ErrNotRegular},
		{"no roots", nil, insideFile, ErrOutsideRoots},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, size, err := Open(tc.roots, tc.path, MaxFileSize)
			if tc.want != nil {
				if !errors.Is(err, tc.want) || file != nil {
					t.Fatalf("Open() = file %v, size %d, err %v; want %v", file, size, err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = file.Close() })
			if size != 4 {
				t.Fatalf("size = %d", size)
			}
		})
	}
	var outsideTexts []string
	for _, tc := range cases {
		if tc.want != ErrOutsideRoots {
			continue
		}
		_, _, err := Open(tc.roots, tc.path, MaxFileSize)
		outsideTexts = append(outsideTexts, err.Error())
	}
	for _, text := range outsideTexts[1:] {
		if text != outsideTexts[0] {
			t.Fatalf("outside errors differ: %q vs %q", outsideTexts[0], text)
		}
	}
}

func TestOpenSizeAndType(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name string
		size int64
		want error
	}{
		{"empty", 0, ErrEmpty},
		{"at limit", MaxFileSize, nil},
		{"over limit", MaxFileSize + 1, ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name)
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(tc.size); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			opened, size, err := Open([]string{root}, path, MaxFileSize)
			if tc.want != nil {
				if !errors.Is(err, tc.want) || opened != nil {
					t.Fatalf("Open() = %v, %d, %v; want %v", opened, size, err, tc.want)
				}
				return
			}
			if err != nil || size != tc.size {
				t.Fatalf("Open() = %v, %d, %v", opened, size, err)
			}
			t.Cleanup(func() { _ = opened.Close() })
		})
	}
}

func TestOpenDetectsReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement")
	if err := os.WriteFile(replacement, []byte("new content"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := open([]string{root}, path, MaxFileSize, func(name string) (*os.File, error) {
		if err := os.Rename(replacement, name); err != nil {
			return nil, err
		}
		return os.Open(name)
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("replaced file: %v", err)
	}
}
