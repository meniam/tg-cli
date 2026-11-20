package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestAudit06_TerminateSessionAmbiguousID(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &sessionFake{fakeTelegram: base, list: rpc.SessionsResult{Sessions: []rpc.Session{{Hash: 2}, {Hash: 2}, {Hash: 3}}}}
	d.opts.Telegram = f
	writeAccess(t, path, `{"sessions":true}`)
	_, err := d.terminateSession(context.Background(), params(t, rpc.TerminateParams{ID: sessionID(2), Yes: true}))
	var e *rpc.Error
	if !errors.As(err, &e) || e.Code != rpc.CodeTelegram || !strings.Contains(e.Message, "ambiguous") || f.calls != 0 {
		t.Fatalf("err = %v, writes = %d", err, f.calls)
	}
}

func TestAudit06_TerminateSessionTelegramFailureIsJournaled(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	failure := errors.New("telegram refused")
	f := &sessionFake{fakeTelegram: base, list: rpc.SessionsResult{Sessions: []rpc.Session{{Hash: 2, DeviceModel: "device"}}}, err: failure}
	d.opts.Telegram = f
	writeAccess(t, path, `{"sessions":true}`)
	_, err := d.terminateSession(context.Background(), params(t, rpc.TerminateParams{ID: sessionID(2), Yes: true}))
	if !errors.Is(err, failure) || f.calls != 1 {
		t.Fatalf("err = %v, writes = %d", err, f.calls)
	}
	data, err := os.ReadFile(filepath.Join(d.opts.Paths.Dir, "account.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("journal lines = %d: %s", len(lines), data)
	}
	var intent, result map[string]any
	if err := json.Unmarshal(lines[0], &intent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &result); err != nil {
		t.Fatal(err)
	}
	if intent["phase"] != "intent" || intent["outcome"] != "pending" || result["phase"] != "result" || result["outcome"] != "failed" {
		t.Fatalf("journal: %s", data)
	}
}
