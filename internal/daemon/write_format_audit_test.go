package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

// auditServeJournal routes one call through a real rpc.Server on a pipe, so the
// request journal of the server and the daemon takes part, and returns what
// that journal wrote.
func auditServeJournal(t *testing.T, d *daemon, method string, p any) (string, error) {
	t.Helper()
	var journal bytes.Buffer
	d.log = slog.New(slog.NewTextHandler(&journal, nil))
	d.srv = rpc.NewServer(d.log)
	d.register()
	clientSide, serverSide := net.Pipe()
	client := rpc.NewClient(clientSide)
	served := make(chan struct{})
	go func() { d.srv.ServeConn(serverSide); close(served) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result any
	err := client.Call(ctx, method, p, &result)
	_ = client.Close()
	<-served
	return journal.String(), err
}

// A failing local store is the one place a successful write logs anything, so
// the text must stay out of that line too.
func TestAudit06WriteStorageFailureKeepsTextOutOfJournal(t *testing.T) {
	const source = "private <b>text</b>"
	for _, edit := range []bool{false, true} {
		name := map[bool]string{false: "send", true: "edit"}[edit]
		t.Run(name, func(t *testing.T) {
			d, fake, db, path := writeDaemon(t)
			writeAccess(t, path, `{"write":[42]}`)
			if edit {
				storedMessage(t, db, 42, 11, "prior")
			}
			fake.check = rpc.TextCheck{Mode: "html", PlainLength: 12}
			fake.sendResult = rpc.SendResult{ID: 11, Date: "2026-11-14T10:00:00Z"}
			fake.editResult = fake.sendResult
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			var journal string
			var err error
			if edit {
				journal, err = auditServeJournal(t, d, rpc.MethodMessageEdit, rpc.EditParams{Chat: "42", ID: 11, Text: source, Parse: "html"})
			} else {
				journal, err = auditServeJournal(t, d, rpc.MethodMessageSend, rpc.SendParams{Chat: "42", Text: source, Parse: "html"})
			}
			if err != nil {
				t.Fatalf("a storage failure must not change the Telegram result: %v", err)
			}
			if !strings.Contains(journal, "local storage failed") || strings.Contains(journal, "private") || strings.Contains(journal, "<b>") {
				t.Fatalf("journal: %s", journal)
			}
		})
	}
}
