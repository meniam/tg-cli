package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func monthFixture(t *testing.T) (*daemon, *store.DB, string, rpc.ExportMonthParams) {
	t.Helper()
	d, db, access := exportTestDaemon(t)
	root := t.TempDir()
	raw, err := json.Marshal(map[string]any{"downloads": []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	writeAccess(t, access, string(raw))
	d.account.set(&rpc.User{ID: 42})
	if err := db.UpsertMessages(context.Background(), []store.Message{{ChatID: 42, ID: 1, Date: time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC).Unix(), Text: "<test &>", Raw: []byte(`{}`)}, {ChatID: 42, ID: 2, Date: time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC).Unix(), Text: "outside", Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	return d, db, root, rpc.ExportMonthParams{Chats: []string{"42"}, Month: "2023-11", Output: root}
}
func TestExportMonthRoundTrip(t *testing.T) {
	d, _, _, p := monthFixture(t)
	ctx := context.Background()
	got, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	res := got.(rpc.ExportMonthResult)
	if res.Existing || len(res.Files) != 1 || res.Files[0].Messages != 1 {
		t.Fatalf("result: %+v", res)
	}
	// The fixture has no history sync state, so the start of the month is not
	// known to be covered.
	if res.Files[0].MonthStartCovered {
		t.Fatalf("month start covered without sync state: %+v", res.Files[0])
	}
	page := exportPage(t, d, rpc.ExportParams{Chat: "42", Since: "2023-11-01T00:00:00Z", Until: "2023-11-30T23:59:59Z"})
	var expected bytes.Buffer
	if err := output.Lines(&expected, page.Messages, ""); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(res.Dir, "42.jsonl")
	data, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(data, expected.Bytes()) {
		t.Fatalf("jsonl %s expected %s: %v", data, expected.Bytes(), err)
	}
	before, err := os.ReadFile(filepath.Join(res.Dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil || !again.(rpc.ExportMonthResult).Existing {
		t.Fatalf("existing=%+v err=%v", again, err)
	}
	after, err := os.ReadFile(filepath.Join(res.Dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("manifest changed")
	}
	v, err := d.messageExportVerify(ctx, params(t, rpc.ExportVerifyParams{Dir: res.Dir}))
	if err != nil || !v.(rpc.ExportVerifyResult).OK {
		t.Fatalf("verify %+v %v", v, err)
	}
	if err = os.WriteFile(file, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err = d.messageExportVerify(ctx, params(t, rpc.ExportVerifyParams{Dir: res.Dir}))
	if err != nil || v.(rpc.ExportVerifyResult).OK {
		t.Fatalf("corruption %+v %v", v, err)
	}
	if _, err = d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeInternal {
		t.Fatalf("corrupt existing: %v", err)
	}
}
func TestExportMonthBoundaries(t *testing.T) {
	d, _, root, p := monthFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"2023-1", "2023-13", "9999-01", time.Now().UTC().Format("2006-01")} {
		p.Month = bad
		if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("month %s: %v", bad, err)
		}
	}
	p.Month = "2023-11"
	p.Chats = []string{"42", "42"}
	if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	p.Chats = []string{"99"}
	if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeNotFound {
		t.Fatal(err)
	}
	p.Chats = []string{"42"}
	p.Output = t.TempDir()
	if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	p.Output = root
	old := exportMonthLimit
	exportMonthLimit = 8
	t.Cleanup(func() { exportMonthLimit = old })
	if _, err := d.messageExportMonth(ctx, params(t, p)); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("size error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tg-export-42-2023-11")); !os.IsNotExist(err) {
		t.Fatalf("partial archive remains: %v", err)
	}
}
func TestExportMonthIncompleteAndExtra(t *testing.T) {
	d, _, root, p := monthFixture(t)
	ctx := context.Background()
	path := filepath.Join(root, "tg-export-42-2023-11")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeInternal {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "keep")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if _, err := d.messageExportMonth(ctx, params(t, p)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "extra"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := d.messageExportVerify(ctx, params(t, rpc.ExportVerifyParams{Dir: path}))
	if err != nil || v.(rpc.ExportVerifyResult).OK || len(v.(rpc.ExportVerifyResult).Extra) != 1 {
		t.Fatalf("extra %+v %v", v, err)
	}
	if err := os.Remove(filepath.Join(path, "42.jsonl")); err != nil {
		t.Fatal(err)
	}
	v, err = d.messageExportVerify(ctx, params(t, rpc.ExportVerifyParams{Dir: path}))
	if err != nil || v.(rpc.ExportVerifyResult).OK || len(v.(rpc.ExportVerifyResult).Missing) != 1 {
		t.Fatalf("missing %+v %v", v, err)
	}
}
func TestExportMonthOversizeLine(t *testing.T) {
	d, db, root, p := monthFixture(t)
	if err := db.UpsertMessages(context.Background(), []store.Message{{ChatID: 42, ID: 9, Date: 1700000000, Text: strings.Repeat("x", maxPageBytes), Raw: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	_, err := d.messageExportMonth(context.Background(), params(t, p))
	if rpcCode(t, err) != rpc.CodeInternal || !strings.Contains(fmt.Sprint(err), "9") {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tg-export-42-2023-11")); !os.IsNotExist(err) {
		t.Fatal("archive remains")
	}
}

func TestExportMonthDeletedAndCoverage(t *testing.T) {
	d, db, _, p := monthFixture(t)
	ctx := context.Background()
	old := store.Message{ChatID: 42, ID: 3, Date: time.Date(2023, 10, 31, 23, 59, 59, 0, time.UTC).Unix(), Text: "older", Raw: []byte(`{}`)}
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{old}, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, 42, []int{1}); err != nil {
		t.Fatal(err)
	}
	p.IncludeDeleted = true
	got, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	res := got.(rpc.ExportMonthResult)
	if res.Files[0].Messages != 1 || !res.Files[0].MonthStartCovered {
		t.Fatalf("coverage: %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(res.Dir, "42.jsonl"))
	if err != nil || !bytes.Contains(data, []byte(`"deleted":true`)) {
		t.Fatalf("deleted: %s %v", data, err)
	}
}
func TestExportMonthPermissionAndSymlinks(t *testing.T) {
	d, _, root, p := monthFixture(t)
	ctx := context.Background()
	writeAccess(t, d.opts.Paths.Dir+"/access.json", `{}`)
	if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"downloads": []string{root}, "block": []int{42}})
	if err != nil {
		t.Fatal(err)
	}
	writeAccess(t, d.opts.Paths.Dir+"/access.json", string(data))
	if _, err := d.messageExportMonth(ctx, params(t, p)); rpcCode(t, err) != rpc.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tg-export-42-2023-11")); !os.IsNotExist(err) {
		t.Fatalf("created for denied chat: %v", err)
	}
	data, err = json.Marshal(map[string]any{"downloads": []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	writeAccess(t, d.opts.Paths.Dir+"/access.json", string(data))
	got, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	path := got.(rpc.ExportMonthResult).Dir
	// The link points at a file outside the archive that holds the exact
	// original bytes: reading through the link would verify, so only a
	// refusal to follow it makes the file missing.
	original, err := os.ReadFile(filepath.Join(path, "42.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "42.jsonl")
	if err = os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(path, "42.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(path, "42.jsonl")); err != nil {
		t.Fatal(err)
	}
	v, err := d.messageExportVerify(ctx, params(t, rpc.ExportVerifyParams{Dir: path}))
	if res, _ := v.(rpc.ExportVerifyResult); err != nil || res.OK || !slices.Equal(res.Missing, []string{"42.jsonl"}) {
		t.Fatalf("symlink verify %+v %v", v, err)
	}
}

func TestExportMonthMultipleChatsDeterministic(t *testing.T) {
	d, db, root, p := monthFixture(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 7, Type: "group"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 7, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	p.Chats = []string{"42", "7"}
	first, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	one := first.(rpc.ExportMonthResult)
	if len(one.Files) != 2 || one.Files[1].ChatID != 7 || one.Files[1].Messages != 0 || !one.Files[1].MonthStartCovered {
		t.Fatalf("files %+v", one.Files)
	}
	secondRoot := filepath.Join(root, "second")
	if err = os.Mkdir(secondRoot, 0700); err != nil {
		t.Fatal(err)
	}
	p.Output = secondRoot
	second, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	two := second.(rpc.ExportMonthResult)
	for _, name := range []string{"42.jsonl", "7.jsonl", "SHA256SUMS"} {
		a, e := os.ReadFile(filepath.Join(one.Dir, name))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(two.Dir, name))
		if e != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s differs: %v", name, e)
		}
	}
}

func TestExportMonthChatCount(t *testing.T) {
	d, _, _, p := monthFixture(t)
	for _, chats := range [][]string{nil, make([]string, 51)} {
		p.Chats = chats
		_, err := d.messageExportMonth(context.Background(), params(t, p))
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("%d chats: %v", len(chats), err)
		}
	}
}
