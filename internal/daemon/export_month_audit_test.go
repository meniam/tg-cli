package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// audit05ExportArchive exports the fixture month for chats 42 and 7 and
// returns the archive directory.
func audit05ExportArchive(t *testing.T, d *daemon, db *store.DB, p rpc.ExportMonthParams) string {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 7, Type: "group"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 7, nil, nil, true, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	p.Chats = []string{"42", "7"}
	got, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	return got.(rpc.ExportMonthResult).Dir
}

func audit05Verify(t *testing.T, d *daemon, dir string) (rpc.ExportVerifyResult, error) {
	t.Helper()
	v, err := d.messageExportVerify(context.Background(), params(t, rpc.ExportVerifyParams{Dir: dir}))
	res, _ := v.(rpc.ExportVerifyResult)
	return res, err
}

// An incomplete history sync whose oldest message is inside the month does not
// prove the month start is covered.
func TestAudit05_ExportMonthStartNotCoveredByIncompleteSync(t *testing.T) {
	d, db, _, p := monthFixture(t)
	ctx := context.Background()
	inside := store.Message{ChatID: 42, ID: 1, Date: time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC).Unix(), Text: "<test &>", Raw: []byte(`{}`)}
	if _, err := db.SaveHistoryPage(ctx, 42, []store.Message{inside}, nil, false, store.GapPass{}); err != nil {
		t.Fatal(err)
	}
	got, err := d.messageExportMonth(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	file := got.(rpc.ExportMonthResult).Files[0]
	if file.Messages != 1 || file.MonthStartCovered {
		t.Fatalf("incomplete sync with the oldest message inside the month: %+v", file)
	}
}

func TestAudit05_ExportMonthUnknownAccount(t *testing.T) {
	d, _, _, p := monthFixture(t)
	d.account.set(nil)
	_, err := d.messageExportMonth(context.Background(), params(t, p))
	if rpcCode(t, err) != rpc.CodeNotAuthorized {
		t.Fatalf("unknown account: %v", err)
	}
	entries, readErr := os.ReadDir(p.Output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tg-export-") {
			t.Fatalf("archive created without an account: %s", e.Name())
		}
	}
}

func TestAudit05_ExportVerifyRequiresAbsolutePath(t *testing.T) {
	d, _, _, _ := monthFixture(t)
	_, err := d.messageExportVerify(context.Background(), params(t, rpc.ExportVerifyParams{Dir: "tg-export-42-2023-11"}))
	if rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("relative dir: %v", err)
	}
}

// Every damaged manifest is refused as invalid, whatever else is intact.
func TestAudit05_ExportVerifyRejectsInvalidManifest(t *testing.T) {
	validHash := strings.Repeat("a", 64)
	cases := []struct {
		name   string
		mutate func(m map[string]any, files []any)
	}{
		{"format", func(m map[string]any, _ []any) { m["format"] = "other" }},
		{"version", func(m map[string]any, _ []any) { m["version"] = 2 }},
		{"no files", func(m map[string]any, _ []any) { m["files"] = []any{} }},
		{"too many files", func(m map[string]any, files []any) {
			many := make([]any, 51)
			for i := range many {
				many[i] = files[0]
			}
			m["files"] = many
		}},
		{"name does not match the chat", func(_ map[string]any, files []any) { files[0].(map[string]any)["name"] = "43.jsonl" }},
		{"names out of order", func(m map[string]any, files []any) { m["files"] = []any{files[1], files[0]} }},
		{"short hash", func(_ map[string]any, files []any) { files[0].(map[string]any)["sha256"] = validHash[:63] }},
		{"hash not hex", func(_ map[string]any, files []any) { files[0].(map[string]any)["sha256"] = strings.Repeat("z", 64) }},
		{"negative size", func(_ map[string]any, files []any) { files[0].(map[string]any)["bytes"] = -1 }},
		{"size over the limit", func(_ map[string]any, files []any) { files[0].(map[string]any)["bytes"] = exportMonthLimit + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, db, _, p := monthFixture(t)
			dir := audit05ExportArchive(t, d, db, p)
			manifest := filepath.Join(dir, "manifest.json")
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err = json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			tc.mutate(m, m["files"].([]any))
			if err = os.WriteFile(manifest, audit05MustJSON(t, m), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = d.messageExportVerify(context.Background(), params(t, rpc.ExportVerifyParams{Dir: dir}))
			if rpcCode(t, err) != rpc.CodeInternal || !strings.Contains(err.Error(), "invalid archive manifest") {
				t.Fatalf("damaged manifest accepted: %v", err)
			}
		})
	}
}

func TestAudit05_ExportVerifyChecksSHA256SUMS(t *testing.T) {
	d, db, _, p := monthFixture(t)
	dir := audit05ExportArchive(t, d, db, p)
	sums := filepath.Join(dir, "SHA256SUMS")
	original, err := os.ReadFile(sums)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := audit05Verify(t, d, dir); err != nil || !res.OK {
		t.Fatalf("intact archive: %+v %v", res, err)
	}
	if err = os.WriteFile(sums, []byte(strings.ToUpper(string(original))), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := audit05Verify(t, d, dir)
	if err != nil || res.OK || len(res.Missing) != 0 || len(res.Extra) != 0 {
		t.Fatalf("altered SHA256SUMS: %+v %v", res, err)
	}
	if err = os.Remove(sums); err != nil {
		t.Fatal(err)
	}
	res, err = audit05Verify(t, d, dir)
	if err != nil || res.OK || !slices.Equal(res.Missing, []string{"SHA256SUMS"}) {
		t.Fatalf("missing SHA256SUMS: %+v %v", res, err)
	}
}
