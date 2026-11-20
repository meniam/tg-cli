package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tg-cli/internal/buildinfo"
	"tg-cli/internal/fileroot"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

var exportMonthLimit int64 = 1 << 30

func (d *daemon) exportDir(path string) (*fileroot.DownloadDir, error) {
	policy := d.policy()
	if !policy.DownloadsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, "downloads are disabled: ask the owner to set access.downloads")
	}
	dir, err := fileroot.OpenArchiveDownloadDir(policy.DownloadRoots(), path)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "directory must be an existing directory within downloads")
	}
	return dir, nil
}
func (d *daemon) messageExportMonth(ctx context.Context, raw json.RawMessage) (result any, err error) {
	var p rpc.ExportMonthParams
	if err = decodeParams(raw, &p); err != nil {
		return nil, err
	}
	start, end, err := rpc.ParseExportMonth(p.Month, time.Now())
	if err != nil {
		return nil, err
	}
	if len(p.Chats) < 1 || len(p.Chats) > 50 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "provide 1 to 50 chats")
	}
	dir, err := d.exportDir(p.Output)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	ids := make([]int64, 0, len(p.Chats))
	seen := map[int64]bool{}
	for _, chat := range p.Chats {
		id, _, e := d.localChatID(ctx, chat)
		if e != nil {
			return nil, e
		}
		known, e := d.store.ChatKnown(ctx, id)
		if e != nil {
			return nil, e
		}
		if !known {
			return nil, rpc.ErrChatNotFound()
		}
		if seen[id] {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "duplicate chat")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	d.account.mu.RLock()
	var account int64
	if d.account.user != nil {
		account = d.account.user.ID
	}
	d.account.mu.RUnlock()
	if account <= 0 {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "account is unknown")
	}
	name := fmt.Sprintf("tg-export-%d-%s", account, p.Month)
	path := dir.Path(name)
	ar, err := dir.OpenArchive(name, true)
	if errors.Is(err, os.ErrExist) {
		ar, err = dir.OpenArchive(name, false)
		if err != nil {
			return nil, rpc.NewError(rpc.CodeInternal, "cannot open existing archive")
		}
		defer ar.Close()
		m, hash, v, e := d.verifyArchive(ctx, ar, path)
		if e != nil {
			return nil, e
		}
		if !v.OK {
			return nil, rpc.NewError(rpc.CodeInternal, fmt.Sprintf("existing archive failed verification (%d missing, %d extra); run message export-verify for details", len(v.Missing), len(v.Extra)))
		}
		return monthResult(path, m, hash, true), nil
	}
	if err != nil {
		return nil, rpc.NewError(rpc.CodeInternal, "cannot create archive")
	}
	defer ar.Close()
	done := false
	defer func() {
		if !done {
			_ = ar.RemoveOwned()
		}
	}()
	m := rpc.Manifest{Format: "tg-cli.export-month", Version: 1, AccountID: account, Month: p.Month, Since: start.Format(time.RFC3339), Until: end.Format(time.RFC3339), IncludeDeleted: p.IncludeDeleted, CreatedAt: time.Now().UTC().Format(time.RFC3339), ToolVersion: buildinfo.Get().Version, Files: make([]rpc.ManifestFile, 0, len(ids))}
	sort.Slice(ids, func(i, j int) bool { return fmt.Sprintf("%d.jsonl", ids[i]) < fmt.Sprintf("%d.jsonl", ids[j]) })
	var total int64
	for _, id := range ids {
		if !d.policy().Allowed(id) {
			return nil, rpc.ErrChatNotFound()
		}
		mf := rpc.ManifestFile{Name: fmt.Sprintf("%d.jsonl", id), ChatID: id}
		state, found, e := d.store.SyncState(ctx, id)
		if e != nil {
			return nil, e
		}
		if found {
			mf.Sync = rpc.ExportSync{Complete: state.Complete, OldestID: state.OldestID, NewestID: state.NewestID, MonthStartCovered: state.Complete}
			if !state.Complete && state.OldestID != nil {
				old, e := d.store.GetMessage(ctx, id, *state.OldestID)
				if e != nil && !errors.Is(e, store.ErrNotFound) {
					return nil, e
				}
				if e == nil && old.Date < start.Unix() {
					mf.Sync.MonthStartCovered = true
				}
			}
		}
		size, hash, e := ar.Write(ctx, mf.Name, exportMonthLimit-total, func(w io.Writer) error {
			after := 0
			for {
				if !d.policy().Allowed(id) {
					return rpc.ErrChatNotFound()
				}
				page, e := d.store.ExportMessages(ctx, store.ExportQuery{ChatID: id, After: after, Since: start, Until: end.Add(-time.Second), IncludeDeleted: p.IncludeDeleted, Limit: store.MaxExportPageSize})
				if e != nil {
					return e
				}
				for _, item := range page.Messages {
					var line bytes.Buffer
					enc := json.NewEncoder(&line)
					enc.SetEscapeHTML(false)
					e := enc.Encode(rpc.ExportMessage{Message: rpcStoredMessage(item), Deleted: item.DeletedAt != nil})
					encoded := line.Bytes()
					if e != nil {
						return e
					}
					if len(encoded) > maxPageBytes {
						return rpc.NewError(rpc.CodeInternal, fmt.Sprintf("chat %d message %d exceeds line limit", id, item.ID))
					}
					if _, e = w.Write(encoded); e != nil {
						return e
					}
					mf.Messages++
					if mf.FirstID == 0 {
						mf.FirstID = item.ID
					}
					mf.LastID = item.ID
					after = item.ID
				}
				if page.Last {
					return nil
				}
			}
		})
		if e != nil {
			return nil, e
		}
		mf.Bytes = size
		mf.SHA256 = hash
		total += size
		m.Files = append(m.Files, mf)
	}
	for _, id := range ids {
		if !d.policy().Allowed(id) {
			return nil, rpc.ErrChatNotFound()
		}
	}
	sums := manifestSums(m)
	if _, _, err = ar.Write(ctx, "SHA256SUMS", exportMonthLimit-total, func(w io.Writer) error { _, e := io.WriteString(w, sums); return e }); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	_, hash, err := ar.Write(ctx, "manifest.json", exportMonthLimit-total-int64(len(sums)), func(w io.Writer) error { _, e := w.Write(data); return e })
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !d.policy().Allowed(id) {
			return nil, rpc.ErrChatNotFound()
		}
	}
	done = true
	return monthResult(path, m, hash, false), nil
}
func manifestSums(m rpc.Manifest) string {
	var b strings.Builder
	for _, f := range m.Files {
		fmt.Fprintf(&b, "%s  %s\n", f.SHA256, f.Name)
	}
	return b.String()
}
func monthResult(path string, m rpc.Manifest, hash string, existing bool) rpc.ExportMonthResult {
	r := rpc.ExportMonthResult{Dir: path, Month: m.Month, Existing: existing, ManifestSHA256: hash, Files: make([]rpc.ExportMonthFile, 0, len(m.Files))}
	for _, f := range m.Files {
		r.Files = append(r.Files, rpc.ExportMonthFile{Name: f.Name, ChatID: f.ChatID, Messages: f.Messages, Bytes: f.Bytes, SHA256: f.SHA256, MonthStartCovered: f.Sync.MonthStartCovered})
	}
	return r
}
func (d *daemon) messageExportVerify(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ExportVerifyParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(p.Dir) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "archive directory must be absolute")
	}
	dir, err := d.exportDir(filepath.Dir(filepath.Clean(p.Dir)))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	ar, err := dir.OpenArchive(filepath.Base(filepath.Clean(p.Dir)), false)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "cannot open archive directory")
	}
	defer ar.Close()
	_, _, result, err := d.verifyArchive(ctx, ar, p.Dir)
	return result, err
}
func (d *daemon) verifyArchive(ctx context.Context, ar *fileroot.Archive, path string) (rpc.Manifest, string, rpc.ExportVerifyResult, error) {
	var m rpc.Manifest
	r := rpc.ExportVerifyResult{Dir: path, OK: true, Files: []rpc.ExportVerifyFile{}, Missing: []string{}, Extra: []string{}}
	f, err := ar.Open("manifest.json")
	if err != nil {
		return m, "", r, rpc.NewError(rpc.CodeInternal, "incomplete archive: manifest missing or invalid; remove it manually")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &m) != nil || m.Format != "tg-cli.export-month" || m.Version != 1 || len(m.Files) < 1 || len(m.Files) > 50 {
		return m, "", r, rpc.NewError(rpc.CodeInternal, "invalid archive manifest")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	r.Month = m.Month
	expected := map[string]bool{"manifest.json": true, "SHA256SUMS": true}
	last := ""
	var total int64
	for _, entry := range m.Files {
		if entry.Name != fmt.Sprintf("%d.jsonl", entry.ChatID) || entry.Name <= last || expected[entry.Name] || entry.Bytes < 0 || len(entry.SHA256) != 64 {
			return m, "", r, rpc.NewError(rpc.CodeInternal, "invalid archive manifest")
		}
		if _, e := hex.DecodeString(entry.SHA256); e != nil || entry.Bytes > exportMonthLimit-total {
			return m, "", r, rpc.NewError(rpc.CodeInternal, "invalid archive manifest")
		}
		total += entry.Bytes
		if !d.policy().Allowed(entry.ChatID) {
			return m, "", r, rpc.ErrChatNotFound()
		}
		last = entry.Name
		expected[entry.Name] = true
		v := rpc.ExportVerifyFile{Name: entry.Name, ExpectedSHA256: entry.SHA256}
		f, e := ar.Open(entry.Name)
		if e != nil {
			r.Missing = append(r.Missing, entry.Name)
			r.OK = false
			r.Files = append(r.Files, v)
			continue
		}
		h := sha256.New()
		v.Bytes, e = io.Copy(&exportHashWriter{ctx: ctx, writer: h}, io.LimitReader(f, entry.Bytes+1))
		f.Close()
		if e != nil {
			return m, "", r, e
		}
		v.ActualSHA256 = hex.EncodeToString(h.Sum(nil))
		v.OK = v.Bytes == entry.Bytes && v.ActualSHA256 == entry.SHA256
		if !v.OK {
			r.OK = false
		}
		r.Files = append(r.Files, v)
		if err := ctx.Err(); err != nil {
			return m, "", r, err
		}
	}
	f, err = ar.Open("SHA256SUMS")
	if err != nil {
		r.Missing = append(r.Missing, "SHA256SUMS")
		r.OK = false
	} else {
		data, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if e != nil {
			return m, "", r, e
		}
		if !bytes.Equal(data, []byte(manifestSums(m))) {
			r.OK = false
		}
	}
	names, err := ar.Names()
	if err != nil {
		return m, "", r, err
	}
	for _, name := range names {
		if !expected[name] {
			r.Extra = append(r.Extra, name)
			r.OK = false
		}
	}
	sort.Strings(r.Missing)
	sort.Strings(r.Extra)
	return m, hash, r, nil
}

// exportHashWriter stops verification promptly when its request is cancelled.
type exportHashWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w *exportHashWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}
