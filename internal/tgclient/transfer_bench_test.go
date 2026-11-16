package tgclient

import (
	"context"
	"crypto/sha256"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"tg-cli/internal/throttle"
)

// BenchmarkTransferBaseline measures the existing sequential transport without sockets.
func BenchmarkTransferBaseline(b *testing.B) {
	for _, kind := range []string{"download", "upload"} {
		b.Run(kind, func(b *testing.B) {
			var calls, active, peak atomic.Int64
			limiter, err := throttle.New(throttle.DefaultConfig(), nil)
			if err != nil {
				b.Fatal(err)
			}
			api := tg.NewClient(writeInvoker(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
				permit, err := limiter.Acquire(throttle.WithBackground(ctx), throttle.General, "", nil)
				if err != nil {
					return err
				}
				defer permit.Release()
				n := active.Add(1)
				defer active.Add(-1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				calls.Add(1)
				select {
				case <-time.After(20 * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
				switch req := in.(type) {
				case *tg.UploadGetFileRequest:
					n := int64(req.Limit)
					if remaining := int64(32<<20) - req.Offset; remaining < n {
						n = max(0, remaining)
					}
					return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, n)})
				case *tg.UploadSaveBigFilePartRequest:
					return uploadOK(out)
				default:
					b.Fatalf("unexpected request %T", in)
					return nil
				}
			}))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				if kind == "download" {
					_, err = downloader.NewDownloader().Download(downloadClient{api}, &tg.InputDocumentFileLocation{ID: 1}).Stream(context.Background(), io.Discard)
				} else {
					_, err = uploader.NewUploader(api).WithThreads(1).Upload(context.Background(), uploader.NewUpload("test.bin", io.LimitReader(zeroReader{}, 30<<20), 30<<20))
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(calls.Load())/float64(b.N), "requests/op")
			b.ReportMetric(float64(peak.Load()), "peak-inflight")
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type hashProbe struct {
	*tg.Client
	window    int
	hashCalls atomic.Int64
	fileCalls atomic.Int64
}

func (p *hashProbe) UploadGetFile(_ context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	p.fileCalls.Add(1)
	return &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, req.Limit)}, nil
}
func (p *hashProbe) UploadGetFileHashes(_ context.Context, req *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	p.hashCalls.Add(1)
	if req.Offset >= 32<<20 {
		return nil, nil
	}
	data := make([]byte, p.window)
	sum := sha256.Sum256(data)
	hashes := make([]tg.FileHash, 0, (32<<20)/p.window)
	for offset := req.Offset; offset < 32<<20; offset += int64(p.window) {
		hashes = append(hashes, tg.FileHash{Offset: offset, Limit: p.window, Hash: sum[:]})
	}
	return hashes, nil
}

type discardAt struct{}

func (discardAt) WriteAt(p []byte, _ int64) (int, error) { return len(p), nil }

func TestHashWindowControlsVerifiedPartSize(t *testing.T) {
	for _, window := range []int{512 << 10, 1 << 20} {
		p := &hashProbe{window: window}
		_, err := downloader.NewDownloader().WithPartSize(1<<20).Download(p, &tg.InputDocumentFileLocation{ID: 1}).WithThreads(8).WithSize(32<<20).WithVerify(true).Parallel(context.Background(), discardAt{})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := p.fileCalls.Load(), int64((32<<20)/window); got != want {
			t.Fatalf("window=%d: parts=%d want=%d", window, got, want)
		}
		t.Logf("window=%d: parts=%d hash requests=%d", window, p.fileCalls.Load(), p.hashCalls.Load())
	}
}

func BenchmarkTransferParallel(b *testing.B) {
	for _, kind := range []string{"download", "upload"} {
		b.Run(kind, func(b *testing.B) {
			var calls, active, peak atomic.Int64
			limiter, err := throttle.New(throttle.DefaultConfig(), nil)
			if err != nil {
				b.Fatal(err)
			}
			inner := telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
				n := active.Add(1)
				defer active.Add(-1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				calls.Add(1)
				select {
				case <-time.After(20 * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
				switch req := in.(type) {
				case *tg.UploadGetFileRequest:
					return respond(out, &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: make([]byte, req.Limit)})
				case *tg.UploadSaveBigFilePartRequest:
					return uploadOK(out)
				default:
					b.Fatalf("unexpected request %T", in)
					return nil
				}
			})
			inv := throttleMiddleware(limiter).Handle(inner)
			var next tg.Invoker = writeInvoker(inv)
			if kind == "upload" {
				next = windowInvoker{next: next, window: newTransfers().uploadWindow(1)}
			} else {
				next = downloadInvoker{next: next, window: newTransfers().downloadWindow(1)}
			}
			api := tg.NewClient(fileInvoker{next: next, autoWait: 10 * time.Second})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if kind == "download" {
					_, err = downloader.NewDownloader().WithPartSize(1<<20).Download(api, &tg.InputPhotoFileLocation{ID: 1}).WithSize(32<<20).WithThreads(downloadWorkers).WithScheduler(downloader.NewScheduler(downloader.SchedulerOptions{Min: 1, Start: 4, Max: 8})).Parallel(context.Background(), discardAt{})
				} else {
					_, err = uploader.NewUploader(api).WithThreads(8).WithPartSize(512<<10).Upload(context.Background(), uploader.NewUpload("test.bin", io.LimitReader(zeroReader{}, 30<<20), 30<<20))
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(calls.Load())/float64(b.N), "requests/op")
			b.ReportMetric(float64(peak.Load()), "peak-inflight")
		})
	}
}
