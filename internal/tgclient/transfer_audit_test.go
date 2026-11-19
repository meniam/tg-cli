package tgclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// audit10Barrier holds the first n callers until n of them are inside, so a
// parallelism lower bound does not depend on how long a mock takes. The
// timeout is a safety net for a regression that never reaches n callers.
type audit10Barrier struct {
	n       int64
	arrived atomic.Int64
	ready   chan struct{}
}

func audit10NewBarrier(n int) *audit10Barrier {
	return &audit10Barrier{n: int64(n), ready: make(chan struct{})}
}

func (b *audit10Barrier) wait(ctx context.Context) error {
	if b.arrived.Add(1) == b.n {
		close(b.ready)
	}
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return fmt.Errorf("only %d of %d parallel callers arrived", b.arrived.Load(), b.n)
	}
}

// audit10Message answers messages.getMessages with one media message.
func audit10Message(media tg.MessageMediaClass) *tg.Client {
	return tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		m := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Media: media}
		return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{m}})
	}))
}

type audit10WriterAt struct {
	mu     sync.Mutex
	writes int
	// block, when set, stalls every WriteAt until it is closed.
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (w *audit10WriterAt) WriteAt(p []byte, _ int64) (int, error) {
	if w.entered != nil {
		w.once.Do(func() { close(w.entered) })
	}
	if w.block != nil {
		<-w.block
	}
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	return len(p), nil
}

func audit10Download(ctx context.Context, api *tg.Client, target io.WriterAt, tr *transfers, parts fileInvokers) ([]rpc.ItemError, error) {
	_, failures, err := downloadResolvedWithTransfer(ctx, api, nil, &tg.InputPeerUser{UserID: 42}, 42, []int{7}, func(_ rpc.DownloadedFile, fetch func(io.Writer) error) error {
		return fetch(audit10WriterAtOnly{target})
	}, tr, parts, 10*time.Second)
	return failures, err
}

// audit10WriterAtOnly exposes io.Writer and io.WriterAt of the target without any other method.
type audit10WriterAtOnly struct{ io.WriterAt }

func (w audit10WriterAtOnly) Write([]byte) (int, error) {
	return 0, errors.New("sequential write is not expected")
}

// TestAudit10_DownloadBufferBoundWithStalledWriter drives the production
// download chain (downloadResolvedWithTransfer) with a writer that never
// returns. gotd keeps five workers, a five block queue and the writer's own
// part in memory, so the source may be asked for at most 11 parts (transfer.go).
func TestAudit10_DownloadBufferBoundWithStalledWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		parts := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			req, ok := in.(*tg.UploadGetFileRequest)
			if !ok {
				return fmt.Errorf("unexpected %T", in)
			}
			calls.Add(1)
			return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, req.Limit)})
		})}
		api := audit10Message(&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, DCID: 4, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", Size: 32 << 20}}}})
		w := &audit10WriterAt{block: make(chan struct{}), entered: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := audit10Download(ctx, api, w, newTransfers(), parts)
			done <- err
		}()
		<-w.entered
		// Every goroutine is durably blocked: no further part can arrive.
		synctest.Wait()
		held := calls.Load()
		cancel()
		close(w.block)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("download error = %v, want context canceled", err)
		}
		if want := int64(2*downloadWorkers + 1); held != want {
			t.Fatalf("source was asked for %d parts while the writer stalled, want %d", held, want)
		}
	})
}

// TestAudit10_DocumentPartSizeFollowsHashWindow checks the production choice of
// WithVerify: documents are verified, so the part size follows the hash window
// Telegram reports; photos are not verified and keep the 1 MiB part size.
func TestAudit10_DocumentPartSizeFollowsHashWindow(t *testing.T) {
	const size = 4 << 20
	for _, tc := range []struct {
		name      string
		media     tg.MessageMediaClass
		window    int
		wantParts int64
		wantHash  bool
	}{
		{"document half mebibyte window", &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, DCID: 4, Size: size}}, 512 << 10, size / (512 << 10), true},
		{"document one mebibyte window", &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, DCID: 4, Size: size}}, 1 << 20, size / (1 << 20), true},
		{"photo is not verified", &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, DCID: 4, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", Size: size}}}}, 512 << 10, size / (1 << 20), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256(make([]byte, tc.window))
			var fileCalls, hashCalls atomic.Int64
			parts := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch req := in.(type) {
				case *tg.UploadGetFileHashesRequest:
					hashCalls.Add(1)
					var hashes []tg.FileHash
					for offset := req.Offset; offset < size; offset += int64(tc.window) {
						hashes = append(hashes, tg.FileHash{Offset: offset, Limit: tc.window, Hash: sum[:]})
					}
					return respond(out, &tg.FileHashVector{Elems: hashes})
				case *tg.UploadGetFileRequest:
					fileCalls.Add(1)
					n := min(int64(req.Limit), size-req.Offset)
					return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, n)})
				default:
					return fmt.Errorf("unexpected %T", in)
				}
			})}
			failures, err := audit10Download(context.Background(), audit10Message(tc.media), &audit10WriterAt{}, newTransfers(), parts)
			if err != nil || len(failures) != 0 {
				t.Fatalf("failures=%+v err=%v", failures, err)
			}
			if fileCalls.Load() != tc.wantParts || (hashCalls.Load() > 0) != tc.wantHash {
				t.Fatalf("parts=%d (want %d) hash requests=%d (want any: %v)", fileCalls.Load(), tc.wantParts, hashCalls.Load(), tc.wantHash)
			}
		})
	}
}

// TestAudit10_HashRequestsBypassTheDownloadWindow: the window counts file parts
// only. A FLOOD on UploadGetFileHashes is retried by fileInvoker but must not
// shrink the window, while the same FLOOD on UploadGetFile does.
func TestAudit10_HashRequestsBypassTheDownloadWindow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		call       func(*tg.Client) error
		wantWindow int
	}{
		{"hashes", func(api *tg.Client) error {
			_, err := api.UploadGetFileHashes(context.Background(), &tg.UploadGetFileHashesRequest{Location: &tg.InputDocumentFileLocation{ID: 1}})
			return err
		}, 4},
		{"file part", func(api *tg.Client) error {
			_, err := api.UploadGetFile(context.Background(), &tg.UploadGetFileRequest{Location: &tg.InputDocumentFileLocation{ID: 1}, Limit: 4096})
			return err
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls int
				f := &fakeFileInvokers{inv: writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
					calls++
					if calls == 1 {
						return tgerr.New(420, "FLOOD_WAIT_1")
					}
					switch in.(type) {
					case *tg.UploadGetFileHashesRequest:
						return respond(out, &tg.FileHashVector{})
					default:
						return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}})
					}
				})}
				tr := newTransfers()
				api, err := tr.fileAPI(context.Background(), f, 4, 10*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if err := tc.call(api); err != nil {
					t.Fatal(err)
				}
				window := tr.downloadWindow(4)
				window.mu.Lock()
				got, active := window.window, window.active
				window.mu.Unlock()
				if calls != 2 || got != tc.wantWindow || active != 0 {
					t.Fatalf("calls=%d window=%d active=%d, want window %d", calls, got, active, tc.wantWindow)
				}
			})
		})
	}
}

func TestAudit10_TransferWindowBounds(t *testing.T) {
	flood := tgerr.New(420, "FLOOD_WAIT_3")
	step := func(t *testing.T, w *transferWindow, err error) {
		t.Helper()
		epoch, acquireErr := w.acquire(context.Background())
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		w.release(epoch, err)
	}
	t.Run("floor of one", func(t *testing.T) {
		w := newTransfers().uploadWindow(1)
		for _, want := range []int{2, 1, 1, 1} {
			step(t, w, flood)
			if w.window != want {
				t.Fatalf("window = %d, want %d", w.window, want)
			}
		}
	})
	t.Run("ceiling of eight", func(t *testing.T) {
		w := newTransfers().uploadWindow(1)
		for i := 0; i < 200; i++ {
			step(t, w, nil)
		}
		if w.window != 8 {
			t.Fatalf("window = %d, want 8", w.window)
		}
	})
	t.Run("other errors keep the window", func(t *testing.T) {
		w := newTransfers().uploadWindow(1)
		step(t, w, errors.New("boom"))
		if w.window != 4 || w.epoch != 0 || w.successes != 0 {
			t.Fatalf("window=%d epoch=%d successes=%d", w.window, w.epoch, w.successes)
		}
	})
	t.Run("rpc flood error counts as a flood", func(t *testing.T) {
		w := newTransfers().uploadWindow(1)
		step(t, w, rpc.NewError(rpc.CodeFloodWait, "flood"))
		if w.window != 2 {
			t.Fatalf("window = %d, want 2", w.window)
		}
	})
	t.Run("two floods of one epoch halve once", func(t *testing.T) {
		w := newTransfers().uploadWindow(1)
		first, err := w.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		second, err := w.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		w.release(first, flood)
		w.release(second, flood)
		if w.window != 2 || w.epoch != 1 {
			t.Fatalf("window=%d epoch=%d, want 2 and 1", w.window, w.epoch)
		}
		third, err := w.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		w.release(third, flood)
		if w.window != 1 || w.epoch != 2 {
			t.Fatalf("after a flood of a new epoch: window=%d epoch=%d, want 1 and 2", w.window, w.epoch)
		}
	})
	t.Run("release wakes a blocked acquire", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			w := newTransfers().uploadWindow(1)
			step(t, w, flood)
			step(t, w, flood)
			if w.window != 1 {
				t.Fatalf("window = %d, want 1", w.window)
			}
			held, err := w.acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			admitted := make(chan error, 1)
			go func() {
				epoch, err := w.acquire(context.Background())
				if err == nil {
					w.release(epoch, nil)
				}
				admitted <- err
			}()
			synctest.Wait()
			select {
			case err := <-admitted:
				t.Fatalf("second request was admitted (err=%v) while the window of one is full", err)
			default:
			}
			w.release(held, nil)
			if err := <-admitted; err != nil {
				t.Fatal(err)
			}
		})
	})
}

func TestAudit10_FileInvokerFloodAutoWaitDisabledReturnsAtOnce(t *testing.T) {
	var calls int
	f := fileInvoker{next: writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_1")
	}), autoWait: 0}
	err := f.Invoke(context.Background(), &tg.UploadSaveBigFilePartRequest{}, nil)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeFloodWait || calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and flood_wait", err, calls)
	}
}

func TestAudit10_FileDCErrorIsReturned(t *testing.T) {
	boom := errors.New("no file connection")
	f := &fakeFileInvokers{err: boom}
	tr := newTransfers()
	if _, err := tr.fileAPI(context.Background(), f, 4, time.Second); !errors.Is(err, boom) {
		t.Fatalf("fileAPI error = %v", err)
	}
	file := mediaFixture(t, "small.bin", 1<<20)
	if _, err := tr.upload(context.Background(), f, 4, time.Second, file.File); !errors.Is(err, boom) {
		t.Fatalf("upload error = %v", err)
	}
	if len(tr.slots) != 0 {
		t.Fatalf("a failed upload kept %d transfer slots", len(tr.slots))
	}
}

func TestAudit10_TransferAutoWait(t *testing.T) {
	if got := (&Client{}).transferAutoWait(); got != throttle.DefaultFloodAutoWait {
		t.Fatalf("without a limiter: %s, want %s", got, throttle.DefaultFloodAutoWait)
	}
	cfg := throttle.DefaultConfig()
	cfg.FloodAutoWait = 3 * time.Second
	limiter, err := throttle.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := (&Client{throttle: limiter}).transferAutoWait(); got != 3*time.Second {
		t.Fatalf("with a limiter: %s, want 3s", got)
	}
}
