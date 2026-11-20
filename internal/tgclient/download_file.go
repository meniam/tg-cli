package tgclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/fileroot"
	"tg-cli/internal/rpc"
	"tg-cli/internal/throttle"
)

var (
	// ErrFileTooLarge: the file is bigger than the limit of the caller. It is
	// returned before any part of the file is requested whenever the size is
	// known in advance.
	ErrFileTooLarge = errors.New("file exceeds the size limit")
	// ErrFileSizeMismatch: the size Telegram reports differs from the size the
	// caller expected, or the downloaded bytes do not add up to the reported size.
	ErrFileSizeMismatch = errors.New("file size does not match")
)

// FileDownload says where and how much DownloadToFile may write.
type FileDownload struct {
	// Dir is the private working directory of the caller (an absolute path). It
	// is created with mode 0700 when missing and tightened to 0700 when it
	// exists with wider permissions. It is not an owner "downloads" directory.
	Dir string
	// MaxBytes is the largest file the caller accepts; it must be positive.
	MaxBytes int64
	// ExpectedSize is the size the caller learned from message metadata, or
	// zero when unknown. A different size reported by Telegram fails the job.
	ExpectedSize int64
}

// TempFile describes a file DownloadToFile left in the working directory. The
// caller owns it and must remove it.
type TempFile struct {
	Path      string
	Size      int64
	SHA256    string
	MediaType string
	MIME      string
}

// DownloadToFile downloads the media of one message into a new file (mode 0600)
// inside req.Dir. It shares the part pool, the media-transfer slots and the
// Telegram part hash check with `message download` and marks every request as
// background, so the shared throttle (and its FLOOD_WAIT pause) paces it. It
// does not know access lists and does not apply fileroot.MaxFileSize: the
// caller checks the chat and sets the limit.
//
// A file bigger than req.MaxBytes gives ErrFileTooLarge without downloading. On
// any error, a cancelled context included, no file is left behind.
func (c *Client) DownloadToFile(ctx context.Context, ref chatref.Ref, messageID int, req FileDownload) (TempFile, error) {
	ctx = throttle.WithBackground(ctx)
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return TempFile{}, err
	}
	return downloadToFile(ctx, c.tg.API(), p, p.InputPeer(), toChatOf(p).ID, messageID, req, c.transfers, c.tg, c.transferAutoWait())
}

func downloadToFile(ctx context.Context, api *tg.Client, p peers.Peer, input tg.InputPeerClass, chatID int64, messageID int, req FileDownload, tr *transfers, invokers fileInvokers, autoWait time.Duration) (result TempFile, err error) {
	if req.MaxBytes <= 0 || req.ExpectedSize < 0 || !filepath.IsAbs(req.Dir) {
		return TempFile{}, rpc.NewError(rpc.CodeInvalidParams, "invalid file download request")
	}
	if req.ExpectedSize > req.MaxBytes {
		return TempFile{}, fmt.Errorf("%w: %d bytes, limit %d", ErrFileTooLarge, req.ExpectedSize, req.MaxBytes)
	}
	if err := prepareTempDir(req.Dir); err != nil {
		return TempFile{}, err
	}
	dir, err := fileroot.OpenDownloadDir([]string{req.Dir}, req.Dir)
	if err != nil {
		return TempFile{}, fmt.Errorf("open working directory: %w", err)
	}
	defer dir.Close()
	name := fmt.Sprintf("%d_%d.media", chatID, messageID)
	// Leftovers of an earlier crashed attempt of the same message.
	for _, stale := range []string{name, "." + name + ".part"} {
		if err := os.Remove(filepath.Join(req.Dir, stale)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return TempFile{}, fmt.Errorf("remove stale file: %w", err)
		}
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dir.Path(name))
		}
	}()

	var visitErr error
	_, failures, err := downloadResolvedWithTransfer(ctx, api, p, input, chatID, []int{messageID}, func(f rpc.DownloadedFile, fetch func(io.Writer) error) error {
		visitErr = fetchToFile(ctx, dir, name, req, f, fetch, &result)
		return visitErr
	}, tr, invokers, autoWait)
	if err != nil {
		return TempFile{}, err
	}
	if visitErr != nil {
		return TempFile{}, visitErr
	}
	if len(failures) > 0 {
		return TempFile{}, itemFailureError(failures[0])
	}
	return result, nil
}

func fetchToFile(ctx context.Context, dir *fileroot.DownloadDir, name string, req FileDownload, f rpc.DownloadedFile, fetch func(io.Writer) error, result *TempFile) error {
	switch {
	case f.Size <= 0:
		return fmt.Errorf("%w: Telegram reports an empty file", ErrFileSizeMismatch)
	case f.Size > req.MaxBytes:
		return fmt.Errorf("%w: %d bytes, limit %d", ErrFileTooLarge, f.Size, req.MaxBytes)
	case req.ExpectedSize > 0 && f.Size != req.ExpectedSize:
		return fmt.Errorf("%w: Telegram reports %d bytes, expected %d", ErrFileSizeMismatch, f.Size, req.ExpectedSize)
	}
	sum, err := dir.WriteParallel(ctx, name, req.MaxBytes, f.Size, fetch)
	if errors.Is(err, fileroot.ErrDownloadSize) {
		return fmt.Errorf("%w: downloaded bytes differ from %d", ErrFileSizeMismatch, f.Size)
	}
	if err != nil {
		return err
	}
	*result = TempFile{Path: dir.Path(name), Size: f.Size, SHA256: sum, MediaType: f.MediaType, MIME: f.MIME}
	return nil
}

// itemFailureError turns a failure of the shared download code (message not
// found, protected content, no downloadable media) back into an error.
func itemFailureError(f rpc.ItemError) error {
	if f.Code == "not_found" {
		return rpc.NewError(rpc.CodeNotFound, f.Message)
	}
	return rpc.NewError(rpc.CodeTelegram, f.Message)
}

// prepareTempDir makes dir an existing private directory. A symbolic link or a
// regular file in its place is refused.
func prepareTempDir(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create working directory: %w", err)
		}
		return os.Chmod(dir, 0o700)
	case err != nil:
		return fmt.Errorf("inspect working directory: %w", err)
	case !info.IsDir():
		return errors.New("working directory is not a directory")
	case info.Mode().Perm()&0o077 != 0:
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// CleanTempDir removes everything inside the working directory of the voice
// jobs and keeps the directory itself. The daemon calls it at start, when no job
// can own a file there. A missing directory is not an error; a symbolic link or
// a regular file in its place is refused so the cleanup never reaches elsewhere.
func CleanTempDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("working directory is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list working directory: %w", err)
	}
	var errs []error
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
