package fileroot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAudit06_WriteParallelRejectsBadArguments(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDownloadDir([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cases := []struct {
		name          string
		file          string
		max, expected int64
	}{
		{"empty name", "", 10, 1},
		{"path in name", "a/b", 10, 1},
		{"dot", ".", 10, 1},
		{"dotdot", "..", 10, 1},
		{"negative max", "f", -1, 0},
		{"negative expected", "f", 10, -1},
		{"expected above max", "f", 10, 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			_, err := d.WriteParallel(context.Background(), tc.file, tc.max, tc.expected, func(io.Writer) error { called = true; return nil })
			if !errors.Is(err, ErrDownloadSize) || called {
				t.Fatalf("err = %v, download called = %v", err, called)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatalf("files left behind: %v", entries)
			}
		})
	}
}

// TestAudit06_WriteParallelConcurrentWriteAt writes disjoint ranges from many
// goroutines, the way a parallel download does. Run under -race it fails when
// the range bookkeeping is not guarded by the mutex.
func TestAudit06_WriteParallelConcurrentWriteAt(t *testing.T) {
	const chunk, chunks = 64, 256
	root := t.TempDir()
	d, err := OpenDownloadDir([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want := make([]byte, chunk*chunks)
	for i := range want {
		want[i] = byte(i % 251)
	}
	sum, err := d.WriteParallel(context.Background(), "big", int64(len(want)), int64(len(want)), func(w io.Writer) error {
		at := w.(io.WriterAt)
		var wg sync.WaitGroup
		errs := make(chan error, chunks)
		for i := range chunks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := at.WriteAt(want[i*chunk:(i+1)*chunk], int64(i*chunk)); err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		return <-errs
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(want)
	if sum != hex.EncodeToString(digest[:]) {
		t.Fatalf("sum = %s", sum)
	}
	got, err := os.ReadFile(filepath.Join(root, "big"))
	if err != nil || string(got) != string(want) {
		t.Fatalf("content differs, err = %v", err)
	}
}
