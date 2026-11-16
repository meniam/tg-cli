package tgclient

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

// Five workers plus a five-block queue and the writer hold at most 11 parts.
const downloadWorkers = 5

type fileInvokers interface {
	FileDC(context.Context, int) (tg.Invoker, error)
}

type transfers struct {
	slots      chan struct{}
	mu         sync.Mutex
	schedulers map[int]*downloader.Scheduler
	uploads    map[int]*transferWindow
	downloads  map[int]*transferWindow
}

func newTransfers() *transfers {
	return &transfers{slots: make(chan struct{}, 2), schedulers: make(map[int]*downloader.Scheduler), uploads: make(map[int]*transferWindow), downloads: make(map[int]*transferWindow)}
}

func (t *transfers) acquire(ctx context.Context) (func(), error) {
	select {
	case t.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-t.slots
			return nil, err
		}
		return func() { <-t.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *transfers) scheduler(dc int) *downloader.Scheduler {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.schedulers[dc]
	if s == nil {
		s = downloader.NewScheduler(downloader.SchedulerOptions{Min: 1, Start: 4, Max: 8})
		t.schedulers[dc] = s
	}
	return s
}

func (t *transfers) fileAPI(ctx context.Context, invokers fileInvokers, dc int, autoWait time.Duration) (*tg.Client, error) {
	invoker, err := invokers.FileDC(ctx, dc)
	if err != nil {
		return nil, err
	}
	return tg.NewClient(fileInvoker{next: downloadInvoker{next: invoker, window: t.downloadWindow(dc)}, autoWait: autoWait}), nil
}

type fileInvoker struct {
	next     tg.Invoker
	autoWait time.Duration
}

func (f fileInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if req, ok := input.(*tg.UploadGetFileRequest); ok && (req.Limit <= 0 || req.Limit > 1<<20) {
		return rpc.NewError(rpc.CodeTelegram, "invalid Telegram file hash window")
	}
	for {
		err := f.next.Invoke(ctx, input, output)
		delay, flood := tgerr.AsFloodWait(err)
		if !flood {
			return err
		}
		if f.autoWait == 0 || delay > f.autoWait {
			return mapError(err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	}
}

func (c *Client) transferAutoWait() time.Duration {
	if c.throttle == nil {
		return throttle.DefaultFloodAutoWait
	}
	return c.throttle.FloodAutoWait()
}

func (t *transfers) upload(ctx context.Context, invokers fileInvokers, dc int, autoWait time.Duration, file *os.File) (tg.InputFileClass, error) {
	release, err := t.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	invoker, err := invokers.FileDC(ctx, dc)
	if err != nil {
		return nil, err
	}
	windowed := windowInvoker{next: invoker, window: t.uploadWindow(dc)}
	api := tg.NewClient(fileInvoker{next: windowed, autoWait: autoWait})
	return uploader.NewUploader(api).WithThreads(8).WithPartSize(512<<10).FromFile(ctx, file)
}

type transferWindow struct {
	mu                        sync.Mutex
	window, active, successes int
	epoch                     uint64
	wake                      chan struct{}
}

// downloadWindow controls the effective per-DC window when fileInvoker retries
// a flood internally. gotd's Scheduler remains an independent upper bound.
func (t *transfers) downloadWindow(dc int) *transferWindow {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.downloads[dc]
	if w == nil {
		w = &transferWindow{window: 4, wake: make(chan struct{})}
		t.downloads[dc] = w
	}
	return w
}

type downloadInvoker struct {
	next   tg.Invoker
	window *transferWindow
}

func (i downloadInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	if _, ok := in.(*tg.UploadGetFileRequest); !ok {
		return i.next.Invoke(ctx, in, out)
	}
	return (windowInvoker{next: i.next, window: i.window}).Invoke(ctx, in, out)
}

func (t *transfers) uploadWindow(dc int) *transferWindow {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.uploads[dc]
	if w == nil {
		w = &transferWindow{window: 4, wake: make(chan struct{})}
		t.uploads[dc] = w
	}
	return w
}
func (w *transferWindow) acquire(ctx context.Context) (uint64, error) {
	w.mu.Lock()
	for w.active >= w.window {
		wake := w.wake
		w.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		w.mu.Lock()
	}
	if err := ctx.Err(); err != nil {
		w.mu.Unlock()
		return 0, err
	}
	w.active++
	epoch := w.epoch
	w.mu.Unlock()
	return epoch, nil
}
func (w *transferWindow) release(epoch uint64, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active--
	var re *rpc.Error
	_, flood := tgerr.AsFloodWait(err)
	if flood || (errors.As(err, &re) && re.Code == rpc.CodeFloodWait) {
		if epoch == w.epoch {
			w.epoch++
			w.window = max(1, w.window/2)
			w.successes = 0
		}
	} else if err == nil {
		w.successes++
		if w.successes >= w.window && w.window < 8 {
			w.window++
			w.successes = 0
		}
	}
	close(w.wake)
	w.wake = make(chan struct{})
}

type windowInvoker struct {
	next   tg.Invoker
	window *transferWindow
}

func (i windowInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	epoch, err := i.window.acquire(ctx)
	if err != nil {
		return err
	}
	err = i.next.Invoke(ctx, in, out)
	i.window.release(epoch, err)
	return err
}
