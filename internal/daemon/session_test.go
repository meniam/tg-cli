package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

type sessionFake struct {
	Telegram
	data []byte
}

func (s sessionFake) SessionBytes(context.Context) ([]byte, error) { return s.data, nil }
func TestSessionExportDoesNotReturnSecret(t *testing.T) {
	d, tg, _, _ := accessTestDaemon(t)
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": 42}})
	if err != nil {
		t.Fatal(err)
	}
	d.opts.Telegram = sessionFake{Telegram: tg, data: raw}
	out := filepath.Join(t.TempDir(), "session")
	for _, request := range []rpc.SessionExportParams{{Path: out}, {Path: "-", Yes: true}} {
		if _, err := d.sessionExport(context.Background(), params(t, request)); err == nil {
			t.Fatal("unconfirmed or stdout export accepted")
		}
	}
	answer, err := d.sessionExport(context.Background(), params(t, rpc.SessionExportParams{Path: out, Yes: true}))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response), strings.TrimSpace(string(secret))) || strings.Contains(string(response), "session") {
		t.Fatal("session was returned in JSON")
	}
	if _, err = d.sessionExport(context.Background(), params(t, rpc.SessionExportParams{Path: out, Yes: true})); err == nil {
		t.Fatal("existing file overwritten")
	}
}
