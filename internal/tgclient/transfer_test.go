package tgclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest/cluster"
	"github.com/gotd/td/transport"
	"tg-cli/internal/rpc"
)

type fakeFileInvokers struct {
	dc  int
	inv tg.Invoker
}

func (f *fakeFileInvokers) FileDC(_ context.Context, dc int) (tg.Invoker, error) {
	f.dc = dc
	return f.inv, nil
}

func TestTransferSlotsAndCancellation(t *testing.T) {
	tr := newTransfers()
	release1, err := tr.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release2, err := tr.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := tr.acquire(ctx); done <- err }()
	select {
	case <-done:
		t.Fatal("third transfer admitted")
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release1()
	release2()
	release, err := tr.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestTransferDCAndLongFlood(t *testing.T) {
	f := &fakeFileInvokers{inv: writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(420, "FLOOD_WAIT_30") })}
	tr := newTransfers()
	api, err := tr.fileAPI(context.Background(), f, 4, 10*time.Second)
	if err != nil || f.dc != 4 {
		t.Fatalf("dc=%d err=%v", f.dc, err)
	}
	_, err = api.UploadGetFile(context.Background(), &tg.UploadGetFileRequest{Limit: 4096})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeFloodWait {
		t.Fatalf("error=%v", err)
	}
	if tr.scheduler(4) != tr.scheduler(4) || tr.scheduler(4) == tr.scheduler(5) || tr.downloadWindow(4) != tr.downloadWindow(4) || tr.downloadWindow(4) == tr.downloadWindow(5) {
		t.Fatal("schedulers and windows not scoped per DC")
	}
}

func TestSendMediaLargeFileUsesParallelBigParts(t *testing.T) {
	file := mediaFixture(t, "large.bin", 30<<20)
	var active, peak, calls atomic.Int64
	f := &fakeFileInvokers{inv: writeInvoker(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.UploadSaveBigFilePartRequest)
		if !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		if len(req.Bytes) != 512<<10 {
			return fmt.Errorf("unexpected size %d", len(req.Bytes))
		}
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
		return uploadOK(out)
	})}
	uploaded, err := newTransfers().upload(context.Background(), f, 3, 10*time.Second, file.File)
	if err != nil || uploaded == nil || f.dc != 3 || calls.Load() != 60 || peak.Load() < 4 || peak.Load() > 8 || active.Load() != 0 {
		t.Fatalf("upload=%v err=%v dc=%d calls=%d peak=%d active=%d", uploaded, err, f.dc, calls.Load(), peak.Load(), active.Load())
	}
}

func TestTransferDownloadPhotoParts(t *testing.T) {
	const size = 32 << 20
	var active, peak, calls atomic.Int64
	parts := &fakeFileInvokers{inv: writeInvoker(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.UploadGetFileRequest)
		if !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		if req.Limit != 1<<20 {
			return fmt.Errorf("unexpected part size %d", req.Limit)
		}
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
		return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, req.Limit)})
	})}
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetMessagesRequest); !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, DCID: 4, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", Size: size}}}}}
		return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
	}))
	file, err := os.CreateTemp(t.TempDir(), "download")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, failures, err := downloadResolvedWithTransfer(context.Background(), api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(file) }, newTransfers(), parts, 10*time.Second)
	if err != nil || len(failures) != 0 || parts.dc != 4 || calls.Load() != 32 || peak.Load() < 4 || peak.Load() > 8 || active.Load() != 0 {
		t.Fatalf("failures=%+v err=%v dc=%d calls=%d peak=%d active=%d", failures, err, parts.dc, calls.Load(), peak.Load(), active.Load())
	}
}

func TestTransferDocumentHashMismatch(t *testing.T) {
	data := []byte("data")
	wrong := sha256.Sum256([]byte("else"))
	parts := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.UploadGetFileHashesRequest:
			hashes := []tg.FileHash(nil)
			if req.Offset == 0 {
				hashes = []tg.FileHash{{Offset: 0, Limit: 4, Hash: wrong[:]}}
			}
			return respond(out, &tg.FileHashVector{Elems: hashes})
		case *tg.UploadGetFileRequest:
			return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: data})
		default:
			return fmt.Errorf("unexpected %T", in)
		}
	})}
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, DCID: 4, Size: 4}}}
		return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
	}))
	file, err := os.CreateTemp(t.TempDir(), "download")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, failures, err := downloadResolvedWithTransfer(context.Background(), api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(file) }, newTransfers(), parts, 10*time.Second)
	if err != nil || len(failures) != 1 || failures[0].Message != "downloaded file does not match telegram hashes" {
		t.Fatalf("failures=%+v err=%v", failures, err)
	}
}

func TestTransferDownloadFloodHandling(t *testing.T) {
	for _, seconds := range []int{3, 30} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			var first atomic.Bool
			var calls atomic.Int64
			parts := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				req := in.(*tg.UploadGetFileRequest)
				calls.Add(1)
				if req.Offset == 1<<20 && first.CompareAndSwap(false, true) {
					return tgerr.New(420, fmt.Sprintf("FLOOD_WAIT_%d", seconds))
				}
				return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, req.Limit)})
			})}
			api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, DCID: 4, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", Size: 2 << 20}}}}}
				return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
			}))
			file, err := os.CreateTemp(t.TempDir(), "download")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			tr := newTransfers()
			_, failures, err := downloadResolvedWithTransfer(context.Background(), api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(file) }, tr, parts, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if seconds == 3 {
				window := tr.downloadWindow(4)
				window.mu.Lock()
				decreases := window.epoch
				window.mu.Unlock()
				if len(failures) != 0 || calls.Load() != 3 || decreases == 0 {
					t.Fatalf("failures=%+v calls=%d window decreases=%d", failures, calls.Load(), decreases)
				}
			} else if len(failures) != 1 || failures[0].Code != "flood_wait" {
				t.Fatalf("failures=%+v", failures)
			}
		})
	}
}

func TestUploadWindowAdaptsAndCancels(t *testing.T) {
	w := newTransfers().uploadWindow(1)
	if w.window != 4 {
		t.Fatal(w.window)
	}
	for i := 0; i < 4; i++ {
		epoch, err := w.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		w.release(epoch, nil)
	}
	if w.window != 5 {
		t.Fatalf("window after successes=%d", w.window)
	}
	epoch, err := w.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w.release(epoch, tgerr.New(420, "FLOOD_WAIT_3"))
	if w.window != 2 {
		t.Fatalf("window after flood=%d", w.window)
	}
	a, _ := w.acquire(context.Background())
	b, _ := w.acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.release(a, nil)
	w.release(b, nil)
}

func TestTransferSmallAndCanceledUpload(t *testing.T) {
	t.Run("small sequential", func(t *testing.T) {
		file := mediaFixture(t, "small.bin", 2<<20)
		var calls, active, peak atomic.Int64
		f := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			if _, ok := in.(*tg.UploadSaveFilePartRequest); !ok {
				return fmt.Errorf("unexpected %T", in)
			}
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			return uploadOK(out)
		})}
		_, err := newTransfers().upload(context.Background(), f, 2, 10*time.Second, file.File)
		if err != nil || calls.Load() != 4 || peak.Load() != 1 {
			t.Fatalf("err=%v calls=%d peak=%d", err, calls.Load(), peak.Load())
		}
	})
	t.Run("canceled", func(t *testing.T) {
		file := mediaFixture(t, "big.bin", 30<<20)
		ctx, cancel := context.WithCancel(context.Background())
		var active atomic.Int64
		f := &fakeFileInvokers{inv: writeInvoker(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
			if _, ok := in.(*tg.UploadSaveBigFilePartRequest); !ok {
				return fmt.Errorf("unexpected %T", in)
			}
			active.Add(1)
			defer active.Add(-1)
			cancel()
			<-ctx.Done()
			return ctx.Err()
		})}
		_, err := newTransfers().upload(ctx, f, 2, 10*time.Second, file.File)
		if !errors.Is(err, context.Canceled) || active.Load() != 0 {
			t.Fatalf("err=%v active=%d", err, active.Load())
		}
	})
}

func TestTransferDownloadCancellationReleasesSlots(t *testing.T) {
	var active atomic.Int64
	started := make(chan struct{}, 8)
	parts := &fakeFileInvokers{inv: writeInvoker(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})}
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, DCID: 4, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", Size: 8 << 20}}}}}
		return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
	}))
	file, err := os.CreateTemp(t.TempDir(), "download")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	tr := newTransfers()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := downloadResolvedWithTransfer(ctx, api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(file) }, tr, parts, 10*time.Second)
		done <- err
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("parts did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not finish")
	}
	if active.Load() != 0 || tr.scheduler(4).Snapshot().InFlight != 0 || len(tr.slots) != 0 {
		t.Fatalf("active=%d scheduler=%+v slots=%d", active.Load(), tr.scheduler(4).Snapshot(), len(tr.slots))
	}
}

func TestSendMediaTransferCancellationDoesNotSend(t *testing.T) {
	file := mediaFixture(t, "big.bin", 30<<20)
	ctx, cancel := context.WithCancel(context.Background())
	var active, sends atomic.Int64
	parts := &fakeFileInvokers{inv: writeInvoker(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
		active.Add(1)
		defer active.Add(-1)
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})}
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error { sends.Add(1); return nil }))
	_, err := sendMediaWithTransfers(ctx, api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "document"}, []rpc.MediaFile{file}, newTransfers(), parts, 2, 10*time.Second)
	if !errors.Is(err, context.Canceled) || sends.Load() != 0 || active.Load() != 0 {
		t.Fatalf("err=%v sends=%d active=%d", err, sends.Load(), active.Load())
	}
}

func TestTransferRefreshesDocumentReference(t *testing.T) {
	data := []byte("data")
	sum := sha256.Sum256(data)
	var metadata, expired atomic.Int64
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		n := metadata.Add(1)
		ref := []byte("old")
		if n > 1 {
			ref = []byte("new")
		}
		m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, DCID: 4, Size: 4, FileReference: ref}}}
		return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
	}))
	parts := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.UploadGetFileHashesRequest:
			if string(req.Location.(*tg.InputDocumentFileLocation).FileReference) == "old" {
				expired.Add(1)
				return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			hashes := []tg.FileHash(nil)
			if req.Offset == 0 {
				hashes = []tg.FileHash{{Offset: 0, Limit: 4, Hash: sum[:]}}
			}
			return respond(out, &tg.FileHashVector{Elems: hashes})
		case *tg.UploadGetFileRequest:
			if string(req.Location.(*tg.InputDocumentFileLocation).FileReference) != "new" {
				return errors.New("old file reference")
			}
			return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: data})
		default:
			return fmt.Errorf("unexpected %T", in)
		}
	})}
	file, err := os.CreateTemp(t.TempDir(), "download")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, failures, err := downloadResolvedWithTransfer(context.Background(), api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error { return fetch(file) }, newTransfers(), parts, 10*time.Second)
	if err != nil || len(failures) != 0 || metadata.Load() != 2 || expired.Load() != 1 {
		t.Fatalf("failures=%+v err=%v metadata=%d expired=%d", failures, err, metadata.Load(), expired.Load())
	}
	got := make([]byte, len(data))
	if n, readErr := file.ReadAt(got, 0); readErr != nil || n != len(data) || string(got) != string(data) {
		t.Fatalf("downloaded data=%q n=%d err=%v", got, n, readErr)
	}
}

type blockedDownloadProbe struct {
	*tg.Client
	calls atomic.Int64
}

func (p *blockedDownloadProbe) UploadGetFile(_ context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	p.calls.Add(1)
	return &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, req.Limit)}, nil
}

type blockingWriterAt struct {
	entered chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func (w *blockingWriterAt) WriteAt(p []byte, _ int64) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.unblock
	return len(p), nil
}
func TestDownloadBufferBoundWithSlowWriter(t *testing.T) {
	p := &blockedDownloadProbe{}
	w := &blockingWriterAt{entered: make(chan struct{}), unblock: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	scheduler := downloader.NewScheduler(downloader.SchedulerOptions{Start: 4, Max: 8})
	go func() {
		_, err := downloader.NewDownloader().WithPartSize(1<<20).Download(p, &tg.InputPhotoFileLocation{ID: 1}).WithSize(32<<20).WithThreads(downloadWorkers).WithScheduler(scheduler).Parallel(ctx, w)
		done <- err
	}()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		cancel()
		close(w.unblock)
		t.Fatal("writer did not start")
	}
	deadline := time.After(time.Second)
	for p.calls.Load() < 2*downloadWorkers+1 {
		select {
		case <-deadline:
			cancel()
			close(w.unblock)
			<-done
			t.Fatalf("only %d blocks reached the stalled writer", p.calls.Load())
		default:
			runtime.Gosched()
		}
	}
	time.Sleep(25 * time.Millisecond)
	calls := p.calls.Load()
	window := scheduler.Snapshot().Window
	t.Logf("held parts=%d window=%d", calls, window)
	cancel()
	close(w.unblock)
	<-done
	if calls > int64(2*window) || calls > 16 {
		t.Fatalf("held %d parts with window %d while writer was blocked", calls, window)
	}
}

// This uses in-memory pipes only; no network socket is opened.
func TestFilePoolUsesConfiguredResolver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	options := []tg.DCOption{{ID: 2, IPAddress: "192.0.2.2", Port: 443}}
	cl := cluster.NewCluster(cluster.Options{Config: tg.Config{DCOptions: options}})
	server, handlers := cl.DC(2, "server")
	handlers.Result(tg.UploadGetFileRequestTypeID, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte("data")})
	listener := &memoryListener{incoming: make(chan net.Conn), closed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, transport.Listen(listener)) }()
	defer func() {
		cancel()
		_ = listener.Close()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	var dials, middleware atomic.Int64
	resolver := dcs.Plain(dcs.PlainOptions{Dial: func(ctx context.Context, _, addr string) (net.Conn, error) {
		if addr != "192.0.2.2:443" {
			return nil, fmt.Errorf("unexpected destination %q", addr)
		}
		dials.Add(1)
		return listener.dial(ctx)
	}})
	mw := telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
			if _, ok := in.(*tg.UploadGetFileRequest); ok {
				middleware.Add(1)
			}
			return next.Invoke(ctx, in, out)
		}
	})
	client := telegram.NewClient(telegram.TestAppID, telegram.TestAppHash, telegram.Options{DC: 2, DCList: dcs.List{Options: options}, Resolver: resolver, PublicKeys: cl.Keys(), SessionStorage: &session.StorageMemory{}, NoUpdates: true, Middlewares: []telegram.Middleware{mw}, FilePoolSize: 4})
	err := client.Run(ctx, func(ctx context.Context) error {
		api, err := newTransfers().fileAPI(ctx, client, 2, 10*time.Second)
		if err != nil {
			return err
		}
		_, err = api.UploadGetFile(ctx, &tg.UploadGetFileRequest{Location: &tg.InputDocumentFileLocation{ID: 1}, Limit: 4096})
		return err
	})
	if err != nil || dials.Load() < 2 || middleware.Load() != 1 {
		t.Fatalf("err=%v dials=%d middleware=%d", err, dials.Load(), middleware.Load())
	}
}

func TestTransferRejectsOversizedHashWindow(t *testing.T) {
	called := false
	f := fileInvoker{next: writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { called = true; return nil }), autoWait: 10 * time.Second}
	err := f.Invoke(context.Background(), &tg.UploadGetFileRequest{Limit: (1 << 20) + 1}, nil)
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestFileInvokerOwnsFloodRetry(t *testing.T) {
	for _, input := range []bin.Encoder{&tg.UploadSaveBigFilePartRequest{}, &tg.UploadSaveFilePartRequest{}, &tg.UploadGetFileHashesRequest{}, &tg.UploadGetFileRequest{Limit: 4096}} {
		t.Run(fmt.Sprintf("%T", input), func(t *testing.T) {
			var calls int
			window := newTransfers().uploadWindow(2)
			inner := writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
				calls++
				if calls == 1 {
					return tgerr.New(420, "FLOOD_WAIT_1")
				}
				return nil
			})
			f := fileInvoker{next: windowInvoker{next: inner, window: window}, autoWait: 10 * time.Second}
			if err := f.Invoke(context.Background(), input, nil); err != nil || calls != 2 || window.window != 2 {
				t.Fatalf("err=%v calls=%d window=%d", err, calls, window.window)
			}
		})
	}
}

func TestFileInvokerFloodWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := fileInvoker{next: writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		cancel()
		return tgerr.New(420, "FLOOD_WAIT_3")
	}), autoWait: 10 * time.Second}
	if err := f.Invoke(ctx, &tg.UploadSaveBigFilePartRequest{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestUploadLongFloodStopsBeforeSend(t *testing.T) {
	file := mediaFixture(t, "big.bin", 30<<20)
	var calls, sends, active atomic.Int64
	parts := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		if _, ok := in.(*tg.UploadSaveBigFilePartRequest); !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		return tgerr.New(420, "FLOOD_WAIT_30")
	})}
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { sends.Add(1); return nil }))
	tr := newTransfers()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := sendMediaWithTransfers(ctx, api, &tg.InputPeerSelf{}, 42, rpc.SendMediaParams{As: "document"}, []rpc.MediaFile{file}, tr, parts, 2, 10*time.Second)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait || sends.Load() != 0 || calls.Load() == 0 || active.Load() != 0 || len(tr.slots) != 0 {
		t.Fatalf("err=%v sends=%d calls=%d active=%d slots=%d", err, sends.Load(), calls.Load(), active.Load(), len(tr.slots))
	}
}
