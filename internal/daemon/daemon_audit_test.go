package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/store"
)

func listenUnix(path string) (net.Listener, error) { return net.Listen("unix", path) }

func TestRunDatabaseFailureBeforeSocket(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Database, 0700); err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), Options{Profile: "test", Paths: paths, Telegram: newFake()})
	if err == nil || !strings.Contains(err.Error(), "open database") {
		t.Fatalf("Run error=%v", err)
	}
	if _, err := os.Stat(paths.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket exists after database failure: %v", err)
	}
}

func TestRunStoreOwnershipBeforeListening(t *testing.T) {
	for _, injected := range []bool{false, true} {
		name := "owned"
		if injected {
			name = "injected"
		}
		t.Run(name, func(t *testing.T) {
			paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(paths.Socket, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.Socket, "keep"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			opts := Options{Profile: "test", Paths: paths, Telegram: newFake()}
			if injected {
				// An injected store is opened by the test, so its directory has
				// to exist: Run does not create it for an injected store.
				if err := os.MkdirAll(paths.DatabaseDir, 0o700); err != nil {
					t.Fatal(err)
				}
				opts.Store, err = store.Open(paths.Database)
				if err != nil {
					t.Fatal(err)
				}
				defer opts.Store.Close()
			}
			if err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "remove stale socket") {
				t.Fatalf("Run error = %v", err)
			}
			if injected {
				if _, err := opts.Store.Version(); err != nil {
					t.Errorf("injected store was closed: %v", err)
				}
			} else {
				opened, err := store.Open(paths.Database)
				if err != nil {
					t.Fatalf("Run did not create database before socket: %v", err)
				}
				opened.Close()
			}
		})
	}
}
