package gigaam

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractArchive unpacks the tar.bz2 at archivePath into dst. Every entry is
// validated, kept or not: names must stay inside a.TopDir, and links, devices
// and pipes are rejected. Only a.Keep is written; directories get 0700, files
// 0600 and files under bin/ 0700.
func extractArchive(ctx context.Context, archivePath, dst string, a Artifact) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	tr := tar.NewReader(bzip2.NewReader(ctxReader{ctx: ctx, r: f}))

	var declared int64
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: read archive: %v", ErrUnsafeArchive, err)
		}

		rel, skip, err := entryPath(hdr, a.TopDir)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("%w: entry %q has type %q, only files and directories are allowed", ErrUnsafeArchive, hdr.Name, string(hdr.Typeflag))
		}

		if hdr.Size < 0 {
			return fmt.Errorf("%w: entry %q has a negative size", ErrUnsafeArchive, hdr.Name)
		}
		declared += hdr.Size
		if declared > a.MaxUnpacked {
			return fmt.Errorf("%w: unpacked size exceeds %d bytes", ErrUnsafeArchive, a.MaxUnpacked)
		}
		if !keep(rel, a.Keep) {
			continue
		}
		if seen[rel] {
			return fmt.Errorf("%w: duplicate entry %q", ErrUnsafeArchive, hdr.Name)
		}
		seen[rel] = true
		if err := writeEntry(tr, dst, rel, hdr.Size); err != nil {
			return err
		}
	}
}

// entryPath validates an entry name and returns it relative to topDir. skip is
// true for the top directory itself and for pax global headers.
func entryPath(hdr *tar.Header, topDir string) (rel string, skip bool, err error) {
	if hdr.Typeflag == tar.TypeXGlobalHeader {
		return "", true, nil
	}
	name := hdr.Name
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) ||
		strings.HasPrefix(name, "/") {
		return "", false, fmt.Errorf("%w: bad entry name %q", ErrUnsafeArchive, name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false, fmt.Errorf("%w: entry %q leaves the target directory", ErrUnsafeArchive, name)
		}
	}
	clean := path.Clean(name)
	if clean == topDir {
		if hdr.Typeflag != tar.TypeDir {
			return "", false, fmt.Errorf("%w: top-level entry %q is not a directory", ErrUnsafeArchive, name)
		}
		return "", true, nil
	}
	rest, ok := strings.CutPrefix(clean, topDir+"/")
	if !ok {
		return "", false, fmt.Errorf("%w: entry %q is outside %q", ErrUnsafeArchive, name, topDir)
	}
	return rest, false, nil
}

func keep(rel string, keepList []string) bool {
	if len(keepList) == 0 {
		return true
	}
	for _, k := range keepList {
		if strings.HasSuffix(k, "/") {
			if strings.HasPrefix(rel, k) {
				return true
			}
		} else if rel == k {
			return true
		}
	}
	return false
}

func writeEntry(r io.Reader, dst, rel string, size int64) error {
	full := filepath.Join(dst, filepath.FromSlash(rel))
	// Join cleans the path, but check the result anyway: this is the last
	// line of defence against writing outside dst.
	if inner, err := filepath.Rel(dst, full); err != nil || inner == ".." || strings.HasPrefix(inner, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: entry %q leaves the target directory", ErrUnsafeArchive, rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if strings.HasPrefix(rel, "bin/") {
		mode = 0o700
	}
	out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, size))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("%w: entry %q is truncated", ErrUnsafeArchive, rel)
	}
	return os.Chmod(full, mode)
}

// ctxReader stops reading when the context ends, so a cancelled install does
// not keep decompressing.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
