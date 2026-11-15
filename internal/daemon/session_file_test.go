package daemon

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionFileAndDuplicate(t *testing.T) {
	base := t.TempDir()
	p := Paths{Dir: filepath.Join(base, "one")}
	p.Lock = filepath.Join(p.Dir, "daemon.lock")
	p.Session = filepath.Join(p.Dir, "session.json")
	q := Paths{Dir: filepath.Join(base, "two")}
	q.Lock = filepath.Join(q.Dir, "daemon.lock")
	q.Session = filepath.Join(q.Dir, "session.json")
	var err error
	key := make([]byte, 256)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	b, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:]}})
	if err != nil {
		t.Fatal(err)
	}
	if err = WithProfileLock(p, func() error { return WriteSession(p, b) }); err != nil {
		t.Fatal(err)
	}
	if err = WithProfileLock(q, func() error { return WriteSession(q, b) }); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(q.Session)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("session permissions: %v %v", st, err)
	}
	release, err := acquireLock(p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = guardSessionStart(q); err == nil {
		t.Fatal("running duplicate accepted")
	}
	if err = WithProfileLock(p, func() error { return nil }); err == nil {
		t.Fatal("running profile accepted")
	}
	files, err := filepath.Glob(filepath.Join(q.Dir, ".session-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary files left")
	}
}
