package tgclient

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"
	"tg-cli/internal/sessionstr"
)

func TestSessionStringCompatibility(t *testing.T) {
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": 123}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := sessionstr.Encode(raw, "desktop", 123)
	if err != nil {
		t.Fatal(err)
	}
	d, err := sessionstr.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err = os.WriteFile(path, d.Session, 0600); err != nil {
		t.Fatal(err)
	}
	loader := session.Loader{Storage: &session.FileStorage{Path: path}}
	loaded, err := loader.Load(context.Background())
	if err != nil || string(loaded.AuthKey) != string(key) || string(loaded.AuthKeyID) != string(id[12:]) {
		t.Fatal("gotd session compatibility failed", err)
	}
	c := New(Config{SessionPath: path})
	b, err := c.SessionBytes(context.Background())
	if err != nil || string(b) != string(raw) {
		t.Fatal("storage read failed", err)
	}
}
