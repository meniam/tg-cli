package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/fileroot"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type sendMediaTelegram struct {
	*writeTelegram
	calls  int
	seen   []rpc.MediaFile
	result rpc.SendMediaResult
	err    error
}

func (f *sendMediaTelegram) SendMedia(_ context.Context, _ chatref.Ref, _ rpc.SendMediaParams, files []rpc.MediaFile) (rpc.SendMediaResult, error) {
	f.calls++
	f.seen = append([]rpc.MediaFile(nil), files...)
	if f.err != nil {
		return rpc.SendMediaResult{}, f.err
	}
	if f.result.Messages == nil {
		refs := make([]rpc.MessageRef, len(files))
		for i := range refs {
			refs[i] = rpc.MessageRef{ID: 100 + i, Date: "2026-11-14T10:00:00Z"}
		}
		return rpc.SendMediaResult{Messages: refs}, nil
	}
	return f.result, nil
}

type mediaCase struct {
	t                                *testing.T
	d                                *daemon
	tg                               *sendMediaTelegram
	db                               *store.DB
	base, root, accessPath, document string
	p                                rpc.SendMediaParams
	opened                           []*os.File
}

func newMediaCase(t *testing.T) *mediaCase {
	t.Helper()
	d, fake, db, accessPath := writeDaemon(t)
	base := t.TempDir()
	root := filepath.Join(base, "allowed")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(root, "document.unknown")
	if err := os.WriteFile(doc, []byte("plain data"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &sendMediaTelegram{writeTelegram: fake}
	d.opts.Telegram = m
	c := &mediaCase{t: t, d: d, tg: m, db: db, base: base, root: root, accessPath: accessPath, document: doc,
		p: rpc.SendMediaParams{Chat: "42", As: "document", Files: []string{doc}}}
	d.openMediaFile = func(roots []string, path string, maxSize int64) (*os.File, int64, error) {
		file, size, err := fileroot.Open(roots, path, maxSize)
		if err == nil {
			c.opened = append(c.opened, file)
		}
		return file, size, err
	}
	c.policy(true, root)
	return c
}

func (c *mediaCase) policy(write bool, roots ...string) {
	c.t.Helper()
	value := map[string]any{"files": append([]string{}, roots...)}
	if write {
		value["write"] = []int64{42}
	}
	data, err := json.Marshal(value)
	if err != nil {
		c.t.Fatal(err)
	}
	writeAccess(c.t, c.accessPath, string(data))
}

func (c *mediaCase) file(name string, data []byte) string {
	c.t.Helper()
	path := filepath.Join(c.root, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		c.t.Fatal(err)
	}
	return path
}

func (c *mediaCase) sparse(name string, size int64) string {
	c.t.Helper()
	path := filepath.Join(c.root, name)
	f, err := os.Create(path)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		c.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		c.t.Fatal(err)
	}
	return path
}

func (c *mediaCase) run() (any, error) {
	return c.d.messageSendMedia(context.Background(), params(c.t, c.p))
}

func TestSendMediaDenialsBeforeTelegram(t *testing.T) {
	type denial struct {
		name    string
		change  func(*mediaCase)
		code    int
		message string
	}
	cases := []denial{
		{"invalid type", func(c *mediaCase) { c.p.As = "other" }, rpc.CodeInvalidParams, ""},
		{"no files", func(c *mediaCase) { c.p.Files = nil }, rpc.CodeInvalidParams, ""},
		{"negative metadata", func(c *mediaCase) { c.p.Duration = -1 }, rpc.CodeInvalidParams, ""},
		{"not logged in", func(c *mediaCase) { c.tg.user = nil }, rpc.CodeNotAuthorized, ""},
		{"closed chat", func(c *mediaCase) {
			writeAccess(c.t, c.accessPath, fmt.Sprintf(`{"allow":[77],"write":[42],"files":[%q]}`, c.root))
		}, rpc.CodeNotFound, ""},
		{"outside write", func(c *mediaCase) { c.policy(false, c.root) }, rpc.CodeWriteDisabled, ""},
		{"files absent", func(c *mediaCase) { writeAccess(c.t, c.accessPath, `{"write":[42]}`) }, rpc.CodeWriteDisabled, "files"},
		{"files empty", func(c *mediaCase) { c.policy(true) }, rpc.CodeWriteDisabled, "files"},
		{"relative", func(c *mediaCase) { c.p.Files = []string{"document.unknown"} }, rpc.CodeInvalidParams, mediaOutsideRoots},
		{"outside root", func(c *mediaCase) { c.p.Files = []string{filepath.Join(c.base, "outside")} }, rpc.CodeInvalidParams, mediaOutsideRoots},
		{"dotdot outside", func(c *mediaCase) { c.p.Files = []string{c.root + "/../outside"} }, rpc.CodeInvalidParams, mediaOutsideRoots},
		{"symlink outside", func(c *mediaCase) {
			outside := filepath.Join(c.base, "outside")
			if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
				c.t.Fatal(err)
			}
			link := filepath.Join(c.root, "link")
			if err := os.Symlink(outside, link); err != nil {
				c.t.Fatal(err)
			}
			c.p.Files = []string{link}
		}, rpc.CodeInvalidParams, mediaOutsideRoots},
		{"missing root", func(c *mediaCase) {
			missing := filepath.Join(c.base, "missing")
			c.policy(true, missing)
			c.p.Files = []string{filepath.Join(missing, "file")}
		}, rpc.CodeInvalidParams, mediaOutsideRoots},
		{"missing file", func(c *mediaCase) { c.p.Files = []string{filepath.Join(c.root, "missing")} }, rpc.CodeNotFound, "file not found"},
		{"directory", func(c *mediaCase) { c.p.Files = []string{c.root} }, rpc.CodeInvalidParams, ""},
		{"empty", func(c *mediaCase) { c.p.Files = []string{c.file("empty", nil)} }, rpc.CodeInvalidParams, "file is empty"},
		{"over size", func(c *mediaCase) { c.p.Files = []string{c.sparse("large", fileroot.MaxFileSize+1)} }, rpc.CodeInvalidParams, "file exceeds 100 MiB"},
		{"album total over size", func(c *mediaCase) { c.p.Files = []string{c.sparse("first", 60<<20), c.sparse("second", 50<<20)} }, rpc.CodeInvalidParams, "files exceed 100 MiB in total"},
		{"voice album", func(c *mediaCase) {
			// Two valid Opus files: only the one-file rule for voice can refuse them.
			c.p.As = "voice"
			c.p.Files = []string{c.file("one.ogg", opusVoice()), c.file("two.ogg", opusVoice())}
		}, rpc.CodeInvalidParams, "voice requires one file"},
		{"eleven files", func(c *mediaCase) {
			c.p.Files = make([]string, 11)
			for i := range c.p.Files {
				c.p.Files[i] = c.document
			}
		}, rpc.CodeInvalidParams, ""},
		{"caption too long", func(c *mediaCase) { c.p.Caption = strings.Repeat("я", 1025) }, rpc.CodeInvalidParams, ""},
		{"wrong photo", func(c *mediaCase) { c.p.As = "photo" }, rpc.CodeInvalidParams, "--as document"},
		{"Vorbis voice", func(c *mediaCase) { c.p.As = "voice"; c.p.Files = []string{c.file("vorbis.ogg", vorbisPage())} }, rpc.CodeInvalidParams, "--as document"},
		{"video metadata missing", func(c *mediaCase) { c.p.As = "video"; c.p.Files = []string{c.file("clip.mp4", minimalFTYP())} }, rpc.CodeInvalidParams, "pass --duration --width --height"},
		{"unknown audio", func(c *mediaCase) { c.p.As = "audio" }, rpc.CodeInvalidParams, "--as document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newMediaCase(t)
			tc.change(c)
			_, err := c.run()
			var rpcErr *rpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != tc.code || (tc.message != "" && rpcErr.Message != tc.message && !strings.Contains(rpcErr.Message, tc.message)) || c.tg.calls != 0 {
				t.Fatalf("denial: err=%v calls=%d", err, c.tg.calls)
			}
			c.assertClosed()
		})
	}
}

func minimalFTYP() []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint32(b[:4], 20)
	copy(b[4:8], "ftyp")
	copy(b[8:12], "isom")
	copy(b[16:20], "avc1")
	return b
}

func vorbisPage() []byte {
	packet := append([]byte{1}, []byte("vorbis............")...)
	b := make([]byte, 28+len(packet))
	copy(b[:4], "OggS")
	b[5], b[26], b[27] = 2, 1, byte(len(packet))
	copy(b[28:], packet)
	return b
}

func oggTestPage(flags byte, granule uint64, packet []byte) []byte {
	b := make([]byte, 28+len(packet))
	copy(b[:4], "OggS")
	b[5], b[26], b[27] = flags, 1, byte(len(packet))
	binary.LittleEndian.PutUint64(b[6:14], granule)
	binary.LittleEndian.PutUint32(b[14:18], 7)
	copy(b[28:], packet)
	return b
}

func opusVoice() []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	binary.LittleEndian.PutUint16(head[10:12], 312)
	return append(oggTestPage(2, 0, head), oggTestPage(4, 312+48000, []byte("data"))...)
}

func pngFile(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestSendMediaDryRunSuccessAndPersistence(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		c := newMediaCase(t)
		c.p.DryRun = true
		c.p.Caption = "preview"
		result, err := c.run()
		if err != nil {
			t.Fatal(err)
		}
		got := result.(rpc.SendMediaResult)
		if !got.DryRun || got.Action != "send_media" || got.Count != 1 || got.ChatID != 42 || len(got.Messages) != 0 || len(got.Sizes) != 1 || got.Sizes[0] != 10 || len(got.Types) != 1 || got.Types[0] != "document" || got.CaptionLength != 7 || c.tg.calls != 0 {
			t.Fatalf("dry run: %+v calls=%d", got, c.tg.calls)
		}
		c.assertClosed()
	})
	for _, n := range []int{1, 3} {
		t.Run(fmt.Sprintf("sent_%d", n), func(t *testing.T) {
			c := newMediaCase(t)
			c.p.As = "photo"
			c.p.Caption = "first caption"
			c.p.ReplyTo, c.p.Topic = 7, 8
			c.p.Files = nil
			for i := range n {
				c.p.Files = append(c.p.Files, c.file(fmt.Sprintf("%d.png", i), pngFile(t)))
			}
			result, err := c.run()
			if err != nil {
				t.Fatal(err)
			}
			got := result.(rpc.SendMediaResult)
			if got.ChatID != 42 || len(got.Messages) != n || c.tg.calls != 1 || len(c.tg.seen) != n {
				t.Fatalf("send: %+v calls=%d", got, c.tg.calls)
			}
			for i, ref := range got.Messages {
				if ref.ID != 100+i || ref.Date == "" {
					t.Fatalf("ref %d: %+v", i, ref)
				}
				if _, err := c.tg.seen[i].File.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("file %d not closed: %v", i, err)
				}
				stored, err := c.db.GetMessage(context.Background(), 42, ref.ID)
				wantText := ""
				if i == 0 {
					wantText = c.p.Caption
				}
				if err != nil || stored.Text != wantText || stored.MediaType == nil || *stored.MediaType != "photo" || stored.Out == nil || !*stored.Out || stored.ReplyTo == nil || *stored.ReplyTo != 7 || stored.TopicID == nil || *stored.TopicID != 8 {
					t.Fatalf("stored %d: %+v %v", i, stored, err)
				}
			}
		})
	}
	t.Run("video flags", func(t *testing.T) {
		c := newMediaCase(t)
		c.p.As = "video"
		c.p.Files = []string{c.file("clip.mp4", minimalFTYP())}
		c.p.Duration, c.p.Width, c.p.Height = 2, 640, 360
		_, err := c.run()
		if err != nil || c.tg.calls != 1 || c.tg.seen[0].MIME != "video/mp4" {
			t.Fatalf("video flags: %v calls=%d", err, c.tg.calls)
		}
	})
}

func (c *mediaCase) assertClosed() {
	c.t.Helper()
	for i, file := range c.opened {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			c.t.Fatalf("opened file %d is not closed: %v", i, err)
		}
	}
}

func TestSendMediaRecognizedAudioAndVoice(t *testing.T) {
	for _, tc := range []struct {
		name, as, mime string
		data           []byte
		duration       int
	}{
		{"MP3", "audio", "audio/mpeg", []byte("ID3\x04\x00\x00audio"), 0},
		{"Opus", "voice", "audio/ogg", opusVoice(), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newMediaCase(t)
			c.p.As = tc.as
			c.p.Files = []string{c.file(tc.name, tc.data)}
			_, err := c.run()
			if err != nil || c.tg.calls != 1 || len(c.tg.seen) != 1 || c.tg.seen[0].MIME != tc.mime || c.tg.seen[0].Duration != tc.duration {
				t.Fatalf("%s: err=%v calls=%d files=%+v", tc.name, err, c.tg.calls, c.tg.seen)
			}
		})
	}
	t.Run("document MIME from extension", func(t *testing.T) {
		c := newMediaCase(t)
		c.p.Files = []string{c.file("plain.txt", []byte("plain text"))}
		_, err := c.run()
		if err != nil || c.tg.calls != 1 || c.tg.seen[0].MIME != "text/plain" {
			t.Fatalf("document MIME: %v, %+v", err, c.tg.seen)
		}
	})
}

func TestSendMediaErrorClosesFilesAndHidesContent(t *testing.T) {
	c := newMediaCase(t)
	c.p.Caption = "private caption"
	c.tg.err = &rpc.Error{Code: rpc.CodeFloodWait, Message: "FLOOD_WAIT: wait before the next request", Data: map[string]any{"retry_after_seconds": 2}}
	var journal bytes.Buffer
	c.d.log = slog.New(slog.NewTextHandler(&journal, nil))
	c.d.srv = rpc.NewServer(c.d.log)
	c.d.register()
	clientSide, serverSide := net.Pipe()
	client := rpc.NewClient(clientSide)
	serverDone := make(chan struct{})
	go func() { c.d.srv.ServeConn(serverSide); close(serverDone) }()
	var result rpc.SendMediaResult
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := client.Call(ctx, rpc.MethodMessageSendMedia, c.p, &result)
	_ = client.Close()
	<-serverDone
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait || c.tg.calls != 1 || len(c.tg.seen) != 1 {
		t.Fatalf("flood: err=%v calls=%d", err, c.tg.calls)
	}
	data, ok := rpcErr.Data.(map[string]any)
	if !ok || data["retry_after_seconds"] != float64(2) {
		t.Fatalf("flood wait seconds: %+v", rpcErr.Data)
	}
	if _, err := c.tg.seen[0].File.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("file not closed: %v", err)
	}
	if strings.Contains(journal.String(), c.document) || strings.Contains(journal.String(), c.p.Caption) || !strings.Contains(journal.String(), rpc.MethodMessageSendMedia) {
		t.Fatalf("journal leaked content: %s", journal.String())
	}
}

func TestSendMediaStorageFailureOnlyLogs(t *testing.T) {
	c := newMediaCase(t)
	var journal bytes.Buffer
	c.d.log = slog.New(slog.NewTextHandler(&journal, nil))
	if err := c.db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := c.run()
	if err != nil || len(result.(rpc.SendMediaResult).Messages) != 1 || !strings.Contains(journal.String(), "local storage failed") {
		t.Fatalf("storage failure changed Telegram result: result=%+v err=%v journal=%s", result, err, journal.String())
	}
}

func TestSendMediaSelfDestructRejectsGroupBeforeOpening(t *testing.T) {
	c := newMediaCase(t)
	c.p.As = "photo"
	c.p.SelfDestruct = "view-once"
	_, err := c.d.messageSendMedia(context.Background(), params(t, c.p))
	if writeCode(t, err) != rpc.CodeInvalidParams || len(c.opened) != 0 || c.tg.calls != 0 {
		t.Fatalf("group: err=%v opened=%d writes=%d", err, len(c.opened), c.tg.calls)
	}
}

func TestAudit06_SendMediaTelegramAnswerChecks(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*mediaCase)
		code    int
		message string
	}{
		{"fewer ids than files", func(c *mediaCase) {
			c.tg.result = rpc.SendMediaResult{Messages: []rpc.MessageRef{{ID: 100, Date: "2026-11-14T10:00:00Z"}}}
			c.p.Files = []string{c.document, c.file("second.unknown", []byte("more data"))}
		}, rpc.CodeTelegram, "Telegram did not return all sent message ids"},
		{"zero id", func(c *mediaCase) {
			c.tg.result = rpc.SendMediaResult{Messages: []rpc.MessageRef{{ID: 0, Date: "2026-11-14T10:00:00Z"}}}
		}, rpc.CodeTelegram, "Telegram did not return a message id and date"},
		{"missing date", func(c *mediaCase) {
			c.tg.result = rpc.SendMediaResult{Messages: []rpc.MessageRef{{ID: 100}}}
		}, rpc.CodeTelegram, "Telegram did not return a message id and date"},
		{"foreign error is replaced", func(c *mediaCase) {
			c.tg.err = errors.New("dial tcp 10.0.0.1: secret transport detail")
		}, rpc.CodeTelegram, "media send failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newMediaCase(t)
			tc.change(c)
			_, err := c.run()
			var rpcErr *rpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != tc.code || rpcErr.Message != tc.message {
				t.Fatalf("err = %v, want code %d message %q", err, tc.code, tc.message)
			}
			if strings.Contains(err.Error(), "secret transport detail") {
				t.Fatalf("transport detail leaked: %v", err)
			}
			c.assertClosed()
		})
	}
}

func TestSendMediaRefusesRevokedPermissionAfterFileOpen(t *testing.T) {
	for _, kind := range []string{"write", "files"} {
		t.Run(kind, func(t *testing.T) {
			c := newMediaCase(t)
			ctx := context.Background()
			c.d.opts.Access = nil
			values := map[string]json.RawMessage{"access.write": []byte(`[42]`)}
			roots, _ := json.Marshal([]string{c.root})
			values["access.files"] = roots
			if err := c.db.SetSettings(ctx, values); err != nil {
				t.Fatal(err)
			}
			c.d.refreshAccess(ctx)
			c.d.openMediaFile = func(roots []string, path string, max int64) (*os.File, int64, error) {
				f, size, err := fileroot.Open(roots, path, max)
				if err != nil {
					return nil, 0, err
				}
				revoked := map[string]json.RawMessage{"access." + kind: []byte(`[]`)}
				if err := c.db.SetSettings(ctx, revoked); err != nil {
					f.Close()
					return nil, 0, err
				}
				c.d.refreshAccess(ctx)
				return f, size, nil
			}
			_, err := c.d.messageSendMedia(ctx, params(t, c.p))
			if writeCode(t, err) != rpc.CodeWriteDisabled {
				t.Fatalf("revoked %s: %v", kind, err)
			}
			if c.tg.calls != 0 {
				t.Fatal("sent after permission was revoked")
			}
		})
	}
}

func TestEditMediaRefusesRevokedPermissionAfterFileOpen(t *testing.T) {
	c := newMediaCase(t)
	ctx := context.Background()
	c.d.opts.Access = nil
	c.tg.messages = []rpc.Message{{ID: 7, ChatID: 42, MediaType: "photo"}}
	f := &audit05EditMediaTelegram{sendMediaTelegram: c.tg}
	c.d.opts.Telegram = f
	roots, _ := json.Marshal([]string{c.root})
	if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`), "access.files": roots}); err != nil {
		t.Fatal(err)
	}
	c.d.refreshAccess(ctx)
	c.d.openMediaFile = func(roots []string, path string, max int64) (*os.File, int64, error) {
		file, size, err := fileroot.Open(roots, path, max)
		if err != nil {
			return nil, 0, err
		}
		if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[]`)}); err != nil {
			file.Close()
			return nil, 0, err
		}
		c.d.refreshAccess(ctx)
		return file, size, nil
	}
	_, err := c.d.messageEditMedia(ctx, params(t, rpc.EditMediaParams{Chat: "42", ID: 7, As: "document", File: c.document}))
	if writeCode(t, err) != rpc.CodeWriteDisabled || f.edits != 0 {
		t.Fatalf("edit after revocation: %v calls=%d", err, f.edits)
	}
}

func TestMediaDryRunDoesNotExposeRevokedFiles(t *testing.T) {
	c := newMediaCase(t)
	ctx := context.Background()
	c.d.opts.Access = nil
	roots, _ := json.Marshal([]string{c.root})
	if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`), "access.files": roots}); err != nil {
		t.Fatal(err)
	}
	c.d.refreshAccess(ctx)
	c.d.openMediaFile = func(roots []string, path string, max int64) (*os.File, int64, error) {
		f, size, err := fileroot.Open(roots, path, max)
		if err != nil {
			return nil, 0, err
		}
		if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.files": []byte(`[]`)}); err != nil {
			f.Close()
			return nil, 0, err
		}
		c.d.refreshAccess(ctx)
		return f, size, nil
	}
	c.p.DryRun = true
	out, err := c.d.messageSendMedia(ctx, params(t, c.p))
	if out != nil || writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("dry-run exposed revoked file: %+v %v", out, err)
	}
}

func TestEditMediaDryRunDoesNotExposeRevokedFiles(t *testing.T) {
	c := newMediaCase(t)
	ctx := context.Background()
	c.d.opts.Access = nil
	c.tg.messages = []rpc.Message{{ID: 7, ChatID: 42, MediaType: "photo"}}
	roots, _ := json.Marshal([]string{c.root})
	if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`), "access.files": roots}); err != nil {
		t.Fatal(err)
	}
	c.d.refreshAccess(ctx)
	c.d.openMediaFile = func(roots []string, path string, max int64) (*os.File, int64, error) {
		f, size, err := fileroot.Open(roots, path, max)
		if err != nil {
			return nil, 0, err
		}
		if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.files": []byte(`[]`)}); err != nil {
			f.Close()
			return nil, 0, err
		}
		c.d.refreshAccess(ctx)
		return f, size, nil
	}
	out, err := c.d.messageEditMedia(ctx, params(t, rpc.EditMediaParams{Chat: "42", ID: 7, As: "document", File: c.document, DryRun: true}))
	if out != nil || writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("dry-run exposed revoked file: %+v %v", out, err)
	}
}

func TestMediaReplayDoesNotExposeClosedChat(t *testing.T) {
	c := newMediaCase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.d.opts.Access = nil
	roots, _ := json.Marshal([]string{c.root})
	if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`), "access.files": roots}); err != nil {
		t.Fatal(err)
	}
	c.d.refreshAccess(ctx)
	c.p.IdempotencyKey = "fixture"
	raw := params(t, c.p)
	if _, err := c.d.messageSendMedia(ctx, raw); err != nil {
		t.Fatal(err)
	}
	release, err := c.d.idempotencyLocks.lock(ctx, 42, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	type answer struct {
		v   any
		err error
	}
	done := make(chan answer, 1)
	go func() { v, err := c.d.messageSendMedia(ctx, raw); done <- answer{v, err} }()
	for {
		c.d.idempotencyLocks.mu.Lock()
		entry := c.d.idempotencyLocks.entries[idempotencyKey{chatID: 42, key: "fixture"}]
		waiting := entry != nil && entry.refs == 2
		c.d.idempotencyLocks.mu.Unlock()
		if waiting {
			break
		}
		if ctx.Err() != nil {
			release()
			t.Fatal("request did not wait")
		}
		runtime.Gosched()
	}
	if err := c.db.SetSettings(ctx, map[string]json.RawMessage{"access.block": []byte(`[42]`)}); err != nil {
		release()
		t.Fatal(err)
	}
	c.d.refreshAccess(ctx)
	release()
	got := <-done
	if got.v != nil || writeCode(t, got.err) != rpc.CodeNotFound {
		t.Fatalf("media replay exposed closed chat: %+v %v", got.v, got.err)
	}
	if c.tg.calls != 1 {
		t.Fatalf("unexpected sends: %d", c.tg.calls)
	}
}
