package tgclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// fileAPI is a fake Telegram for DownloadToFile: one message with a voice
// document, a part server and counters of what the client asked for.
type fileAPI struct {
	data       []byte
	reported   int64 // the size the document claims
	message    func() []tg.MessageClass
	serve      func(ctx context.Context, call int, req *tg.UploadGetFileRequest, out bin.Decoder) error
	messages   atomic.Int32
	gets       atomic.Int32
	foreground atomic.Int32 // requests that were not marked as background
}

func newFileAPI(data []byte) *fileAPI {
	f := &fileAPI{data: data, reported: int64(len(data))}
	f.message = func() []tg.MessageClass {
		doc := &tg.Document{ID: 9, DCID: 4, Size: f.reported, MimeType: "audio/ogg", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true, Duration: 3}}}
		return []tg.MessageClass{&tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaDocument{Document: doc}}}
	}
	return f
}

func (f *fileAPI) client(t *testing.T) *tg.Client {
	return tg.NewClient(writeInvoker(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if !throttle.BackgroundFromContext(ctx) {
			f.foreground.Add(1)
		}
		switch req := in.(type) {
		case *tg.MessagesGetMessagesRequest:
			f.messages.Add(1)
			return respond(out, &tg.MessagesMessages{Messages: f.message()})
		case *tg.UploadGetFileRequest:
			call := int(f.gets.Add(1))
			if f.serve != nil {
				return f.serve(ctx, call, req, out)
			}
			return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: partOf(f.data, req)})
		default:
			t.Errorf("unexpected request %T", in)
			return errors.New("unexpected request")
		}
	}))
}

func partOf(data []byte, req *tg.UploadGetFileRequest) []byte {
	start := min(req.Offset, int64(len(data)))
	end := min(start+int64(req.Limit), int64(len(data)))
	return data[start:end]
}

func testBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func (f *fileAPI) download(ctx context.Context, req FileDownload) (TempFile, error) {
	return downloadToFile(throttle.WithBackground(ctx), f.client(nil), nil, &tg.InputPeerUser{UserID: 42}, 42, 7, req, nil, nil, 0)
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestDownloadToFileWritesPrivateFile(t *testing.T) {
	data := testBytes(1200 << 10)
	api := newFileAPI(data)
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	got, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 2 << 20, ExpectedSize: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got.Size != int64(len(data)) || got.SHA256 != hex.EncodeToString(sum[:]) || got.MediaType != "voice" || got.MIME != "audio/ogg" {
		t.Fatalf("result = %+v", got)
	}
	content, err := os.ReadFile(got.Path)
	if err != nil || !bytes.Equal(content, data) {
		t.Fatalf("content differs: %v", err)
	}
	// The result path is the resolved one (on macOS /var is a link to /private/var).
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Dir(got.Path) != resolved {
		t.Fatalf("file %q is outside %q, %v", got.Path, resolved, err)
	}
	if info, err := os.Stat(got.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info.Mode(), err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v", info.Mode(), err)
	}
	if names := dirEntries(t, dir); len(names) != 1 || filepath.Join(resolved, names[0]) != got.Path {
		t.Fatalf("directory holds %v, want only the result", names)
	}
	if api.messages.Load() == 0 || api.gets.Load() == 0 || api.foreground.Load() != 0 {
		t.Fatalf("messages=%d gets=%d foreground=%d", api.messages.Load(), api.gets.Load(), api.foreground.Load())
	}
}

func TestDownloadToFileTightensExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := newFileAPI(testBytes(100)).download(context.Background(), FileDownload{Dir: dir, MaxBytes: 1000}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v", info.Mode(), err)
	}
}

func TestDownloadToFileRefusesBadDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"symlink": link, "file": plain, "relative": "tmp-voice", "empty": ""} {
		api := newFileAPI(testBytes(100))
		if _, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 1000}); err == nil {
			t.Errorf("%s directory accepted", name)
		}
		if api.messages.Load() != 0 {
			t.Errorf("%s directory: Telegram was asked", name)
		}
	}
	if names := dirEntries(t, target); len(names) != 0 {
		t.Fatalf("symlink target got files: %v", names)
	}
	api := newFileAPI(testBytes(100))
	for _, req := range []FileDownload{{Dir: root}, {Dir: root, MaxBytes: -1}, {Dir: root, MaxBytes: 10, ExpectedSize: -1}} {
		var re *rpc.Error
		if _, err := api.download(context.Background(), req); !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams {
			t.Errorf("request %+v: %v", req, err)
		}
	}
}

func TestDownloadToFileLimitsStopBeforeDownloading(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	t.Run("expected size over the limit asks nothing", func(t *testing.T) {
		api := newFileAPI(testBytes(1000))
		_, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 500, ExpectedSize: 1000})
		if !errors.Is(err, ErrFileTooLarge) || api.messages.Load() != 0 || api.gets.Load() != 0 {
			t.Fatalf("err=%v messages=%d gets=%d", err, api.messages.Load(), api.gets.Load())
		}
	})
	t.Run("size reported by Telegram over the limit downloads nothing", func(t *testing.T) {
		api := newFileAPI(testBytes(1000))
		_, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 500})
		if !errors.Is(err, ErrFileTooLarge) || api.gets.Load() != 0 {
			t.Fatalf("err=%v gets=%d", err, api.gets.Load())
		}
	})
	t.Run("size reported by Telegram differs from the expected one", func(t *testing.T) {
		api := newFileAPI(testBytes(1000))
		_, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 2000, ExpectedSize: 900})
		if !errors.Is(err, ErrFileSizeMismatch) || api.gets.Load() != 0 {
			t.Fatalf("err=%v gets=%d", err, api.gets.Load())
		}
	})
	t.Run("exactly the limit passes", func(t *testing.T) {
		api := newFileAPI(testBytes(500))
		if got, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 500, ExpectedSize: 500}); err != nil || got.Size != 500 {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})
	for _, name := range dirEntries(t, dir) {
		if name[0] == '.' {
			t.Errorf("partial file %q left behind", name)
		}
	}
}

func TestDownloadToFileCancellationLeavesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	api := newFileAPI(testBytes(2 << 20))
	started := make(chan struct{})
	api.serve = func(ctx context.Context, call int, req *tg.UploadGetFileRequest, out bin.Decoder) error {
		if call == 1 {
			return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: partOf(api.data, req)})
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := api.download(ctx, FileDownload{Dir: dir, MaxBytes: 4 << 20})
		done <- err
	}()
	// The deadlines are safety nets only: a healthy run never waits for them.
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("directory holds %v after cancellation", names)
	}
}

func TestDownloadToFileBreakLeavesNothingAndRetryWorks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	data := testBytes(1200 << 10)
	api := newFileAPI(data)
	api.serve = func(_ context.Context, call int, req *tg.UploadGetFileRequest, out bin.Decoder) error {
		if call == 2 {
			return errors.New("connection reset")
		}
		return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: partOf(api.data, req)})
	}
	if _, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 4 << 20}); err == nil {
		t.Fatal("a broken download succeeded")
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("directory holds %v after a break", names)
	}
	// The retry starts clean and succeeds with the same names.
	api.gets.Store(100)
	got, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(got.Path); err != nil || !bytes.Equal(content, data) {
		t.Fatalf("retried content differs: %v", err)
	}
}

func TestDownloadToFileWrongSizeLeavesNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reported int64
		served   int
	}{
		{"fewer bytes than reported", 1000, 600},
		{"more bytes than reported", 100, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tmp-voice")
			api := newFileAPI(testBytes(tc.served))
			api.reported = tc.reported
			_, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 300 + tc.reported})
			if !errors.Is(err, ErrFileSizeMismatch) {
				t.Fatalf("err = %v, want size mismatch", err)
			}
			if names := dirEntries(t, dir); len(names) != 0 {
				t.Fatalf("directory holds %v", names)
			}
		})
	}
}

func TestDownloadToFileFloodWaitReturnsTheWait(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	api := newFileAPI(testBytes(100))
	api.serve = func(context.Context, int, *tg.UploadGetFileRequest, bin.Decoder) error {
		return tgerr.New(420, "FLOOD_WAIT_30")
	}
	_, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 1000})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeFloodWait || api.gets.Load() != 1 {
		t.Fatalf("err=%v gets=%d", err, api.gets.Load())
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("directory holds %v", names)
	}
}

func TestDownloadToFileMessageProblems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message []tg.MessageClass
		code    int
	}{
		{"missing", nil, rpc.CodeNotFound},
		{"protected", []tg.MessageClass{&tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Noforwards: true, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 9, Size: 4}}}}, rpc.CodeTelegram},
		{"no media", []tg.MessageClass{&tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Message: "text"}}, rpc.CodeTelegram},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tmp-voice")
			api := newFileAPI(testBytes(100))
			api.message = func() []tg.MessageClass { return tc.message }
			_, err := api.download(context.Background(), FileDownload{Dir: dir, MaxBytes: 1000})
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != tc.code || api.gets.Load() != 0 {
				t.Fatalf("err=%v gets=%d", err, api.gets.Load())
			}
		})
	}
}

func TestDownloadToFileReplacesLeftoversOfACrashedAttempt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp-voice")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"42_7.media", ".42_7.media.part"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := testBytes(300)
	got, err := newFileAPI(data).download(context.Background(), FileDownload{Dir: dir, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(got.Path); err != nil || !bytes.Equal(content, data) {
		t.Fatalf("content = %q, %v", content, err)
	}
	if names := dirEntries(t, dir); len(names) != 1 {
		t.Fatalf("directory holds %v", names)
	}
}

// The production path goes through the shared pool: media DC, transfer slots
// and the Telegram part hash check.
func TestDownloadToFileUsesSharedPool(t *testing.T) {
	data := testBytes(300)
	sum := sha256.Sum256(data)
	for _, tc := range []struct {
		name  string
		hash  [32]byte
		ok    bool
		mimic string
	}{
		{"matching hashes", sum, true, ""},
		{"wrong hashes", sha256.Sum256([]byte("else")), false, "downloaded file does not match telegram hashes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tmp-voice")
			var background atomic.Int32 // requests that were not marked as background
			parts := &fakeFileInvokers{inv: writeInvoker(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
				if !throttle.BackgroundFromContext(ctx) {
					background.Add(1)
				}
				switch req := in.(type) {
				case *tg.UploadGetFileHashesRequest:
					hashes := []tg.FileHash(nil)
					if req.Offset == 0 {
						hashes = []tg.FileHash{{Offset: 0, Limit: len(data), Hash: tc.hash[:]}}
					}
					return respond(out, &tg.FileHashVector{Elems: hashes})
				case *tg.UploadGetFileRequest:
					return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: partOf(data, req)})
				default:
					return fmt.Errorf("unexpected %T", in)
				}
			})}
			api := newFileAPI(data).client(t)
			tr := newTransfers()
			got, err := downloadToFile(throttle.WithBackground(context.Background()), api, nil, &tg.InputPeerUser{UserID: 42}, 42, 7, FileDownload{Dir: dir, MaxBytes: 1000, ExpectedSize: 300}, tr, parts, 10*time.Second)
			if parts.dc != 4 || len(tr.slots) != 0 || background.Load() != 0 {
				t.Fatalf("dc=%d slots=%d foreground=%d", parts.dc, len(tr.slots), background.Load())
			}
			if tc.ok {
				if err != nil || got.SHA256 != hex.EncodeToString(sum[:]) {
					t.Fatalf("got=%+v err=%v", got, err)
				}
				return
			}
			var re *rpc.Error
			if err == nil || !errors.As(err, &re) || re.Message != tc.mimic {
				t.Fatalf("err = %v, want %q", err, tc.mimic)
			}
			if names := dirEntries(t, dir); len(names) != 0 {
				t.Fatalf("directory holds %v", names)
			}
		})
	}
}

func TestCleanTempDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tmp-voice")
	if err := CleanTempDir(dir); err != nil {
		t.Fatalf("missing directory: %v", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1_2.media", ".1_2.media.part"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "deep", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CleanTempDir(dir); err != nil {
		t.Fatal(err)
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("directory holds %v", names)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory itself changed: %v, %v", info, err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Fatalf("a symbolic link led the cleanup outside: %q, %v", content, err)
	}
}

func TestCleanTempDirRefusesLinkAndFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanTempDir(link); err == nil {
		t.Error("a symbolic link was cleaned")
	}
	if err := CleanTempDir(plain); err == nil {
		t.Error("a regular file was accepted")
	}
	if names := dirEntries(t, target); len(names) != 1 {
		t.Fatalf("the link target lost files: %v", names)
	}
}
