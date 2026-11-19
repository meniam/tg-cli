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
	"tg-cli/internal/sessionstr"
)

func auditSessionRaw(t *testing.T, userID int64) []byte {
	t.Helper()
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	data := map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:]}
	if userID != 0 {
		data["UserID"] = userID
	}
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": data})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAudit06SessionExportUserID(t *testing.T) {
	cases := []struct {
		name     string
		stored   int64
		wantCode int // 0 means success
		wantUser int64
	}{
		{"same user", 42, 0, 42},
		{"session without a user id gets the logged-in user", 0, 0, 42},
		{"session of another user", 7, rpc.CodeNotAuthorized, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, _, _ := accessTestDaemon(t)
			d.opts.Telegram = exportSessionFake{Telegram: tg, data: auditSessionRaw(t, tc.stored)}
			out := filepath.Join(t.TempDir(), "session")
			answer, err := d.sessionExport(context.Background(), params(t, rpc.SessionExportParams{Path: out, Yes: true}))
			if tc.wantCode != 0 {
				if err == nil || writeCode(t, err) != tc.wantCode {
					t.Fatalf("err = %v, want code %d", err, tc.wantCode)
				}
				if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
					t.Fatalf("a refused export left a file: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := answer.(rpc.SessionExportResult); got.UserID != tc.wantUser {
				t.Fatalf("answer user = %d, want %d", got.UserID, tc.wantUser)
			}
			encoded, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := sessionstr.Decode(strings.TrimSpace(string(encoded)))
			if err != nil || decoded.UserID != tc.wantUser {
				t.Fatalf("file user = %+v %v, want %d", decoded, err, tc.wantUser)
			}
		})
	}
}
