package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/sessionstr"
)

type exportSessionFake struct {
	Telegram
	data []byte
}

func (s exportSessionFake) SessionBytes(context.Context) ([]byte, error) { return s.data, nil }
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
	d.opts.Telegram = exportSessionFake{Telegram: tg, data: raw}
	out := filepath.Join(t.TempDir(), "session")
	for _, request := range []rpc.SessionExportParams{{Path: out}, {Path: "-", Yes: true}} {
		if _, err := d.sessionExport(context.Background(), params(t, request)); err == nil || writeCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("unconfirmed or stdout export %+v: %v, want CodeInvalidParams", request, err)
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
	before, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.sessionExport(context.Background(), params(t, rpc.SessionExportParams{Path: out, Yes: true})); err == nil || writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("existing file overwritten: %v", err)
	}
	if after, err := os.ReadFile(out); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing export changed: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode: %v %v", info, err)
	}
	decoded, err := sessionstr.Decode(strings.TrimSpace(string(secret)))
	if err != nil || decoded.UserID != 42 || decoded.DC != 2 {
		t.Fatalf("exported session: %+v %v", decoded, err)
	}
}
