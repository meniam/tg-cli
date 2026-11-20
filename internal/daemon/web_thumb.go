package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
)

const maxThumb = 128 << 10

type webThumbnailClient interface {
	WebThumbnail(context.Context, int64, int, int, io.Writer) error
}

type thumbnailBuffer struct{ buffer bytes.Buffer }

func (b *thumbnailBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > maxThumb {
		return 0, errors.New("thumbnail exceeds size limit")
	}
	return b.buffer.Write(p)
}

func (d *daemon) webThumb(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.WebThumbnailParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	missing := rpc.NewError(rpc.CodeNotFound, "thumbnail unavailable")
	chat, err := d.webStoredChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	row, err := d.store.GetMessage(ctx, chat.ID, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, missing
	}
	if err != nil {
		return nil, err
	}
	if row.DeletedAt != nil || row.MediaType == nil || !slices.Contains([]string{"photo", "video", "sticker", "document", "animation", "video_note"}, *row.MediaType) {
		return nil, missing
	}
	var msg webRawMessage
	var peer struct {
		NoForwards bool `json:"noforwards"`
	}
	_ = json.Unmarshal(chat.Raw, &peer)
	_ = json.Unmarshal(row.Raw, &msg)
	if peer.NoForwards || msg.NoForwards || msg.Media.TTLSeconds > 0 {
		return nil, missing
	}
	reply := func(data []byte) (any, error) {
		if !d.policy().Allowed(chat.ID) {
			return nil, missing
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || max(cfg.Width, cfg.Height) > 512 {
			return nil, missing
		}
		return rpc.WebThumbnail{Bytes: data}, nil
	}
	if p.Preview {
		decoder, ok := d.opts.Telegram.(interface {
			ExpandWebThumbnail([]byte) ([]byte, error)
		})
		if !ok {
			return nil, missing
		}

		// A missing/no longer populated raw chat cannot establish the peer's
		// forwarding policy. Only show embedded previews with a known peer.
		if len(chat.Raw) == 0 && chat.ID < 0 {
			return nil, missing
		}
		for _, list := range [][]webPhotoSize{msg.Media.Photo.Sizes, msg.Media.Document.Thumbs} {
			for _, size := range list {
				if size.Kind == "photoStrippedSize" && len(size.Bytes) <= 1024 {
					if data, err := decoder.ExpandWebThumbnail(size.Bytes); err == nil {
						return reply(data)
					}
				}
			}
		}
		return nil, missing
	}
	if d.opts.WebCacheBytes < maxThumb || d.opts.WebCacheTTL <= 0 || d.opts.WebCacheRoot == "" {
		return nil, missing
	}
	base := d.opts.WebCacheRoot
	if !slices.Contains(d.policy().DownloadRoots(), base) {
		return nil, missing
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, missing
	}
	defer root.Close()
	profile := d.opts.Profile
	if profile == "" {
		profile = DefaultProfile
	}
	subdir := filepath.Join(".tg-web", profile)
	dir := filepath.Join(subdir, strconv.FormatInt(chat.ID, 10))
	// Include the saved media snapshot in the key so that an edit cannot reuse
	// a thumbnail fetched for older message content.
	key := sha256.Sum256(row.Raw)
	path := filepath.Join(dir, fmt.Sprintf("%d-%x.thumb.jpg", row.ID, key[:8]))
	d.thumbMu.Lock()
	data := readThumbnail(root, path, d.opts.WebCacheTTL)
	d.thumbMu.Unlock()
	if data != nil && slices.Contains(d.policy().DownloadRoots(), base) {
		return reply(data)
	}
	select {
	case d.thumbSlots <- struct{}{}:
		defer func() { <-d.thumbSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	fetch := func(ctx context.Context, client Telegram) ([]byte, error) {
		c, ok := client.(webThumbnailClient)
		if !ok {
			return nil, missing
		}
		var b thumbnailBuffer
		err := c.WebThumbnail(throttle.WithBackground(ctx), chat.ID, row.ID, maxThumb, &b)
		return b.buffer.Bytes(), err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var fetchClient Telegram
	if d.opts.NewTelegram == nil {
		fetchClient = d.opts.Telegram
	} else {
		leasedCtx, client, release, err := d.acquireTelegram(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		ctx, fetchClient = leasedCtx, client
	}
	data, err = fetch(ctx, fetchClient)
	if err != nil {
		return nil, err
	}
	result, err := reply(data)
	if err != nil {
		return nil, err
	}
	d.thumbMu.Lock()
	defer d.thumbMu.Unlock()
	if !d.policy().Allowed(chat.ID) || !slices.Contains(d.policy().DownloadRoots(), base) {
		return nil, missing
	}
	if root.MkdirAll(dir, 0700) == nil && d.trimThumbnails(root, subdir, int64(len(data))) {
		if file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = root.Remove(path)
			}
		}
	}
	return result, nil
}

func readThumbnail(root *os.Root, path string, ttl time.Duration) []byte {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxThumb || time.Since(info.ModTime()) > ttl {
		return nil
	}
	file, err := root.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxThumb+1))
	if err != nil || len(data) > maxThumb {
		return nil
	}
	return data
}

var thumbName = regexp.MustCompile(`^[0-9]+-[0-9a-f]{16}\.thumb\.jpg$`)

func (d *daemon) trimThumbnails(root *os.Root, subdir string, incoming int64) bool {
	type file struct {
		path     string
		size     int64
		modified time.Time
	}
	var files []file
	var total int64
	err := fs.WalkDir(root.FS(), subdir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !thumbName.MatchString(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		files = append(files, file{path, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return false
	}
	slices.SortFunc(files, func(a, b file) int { return a.modified.Compare(b.modified) })
	for _, file := range files {
		if time.Since(file.modified) > d.opts.WebCacheTTL || total+incoming > d.opts.WebCacheBytes {
			if root.Remove(file.path) == nil {
				total -= file.size
			}
		}
	}
	return total+incoming <= d.opts.WebCacheBytes
}

func (d *daemon) webThumbEnabled(_ context.Context, _ json.RawMessage) (any, error) {
	base := d.opts.WebCacheRoot
	enabled := false
	if slices.Contains(d.policy().DownloadRoots(), base) && d.opts.WebCacheBytes >= maxThumb && d.opts.WebCacheTTL > 0 {
		if info, err := os.Stat(base); err == nil && info.IsDir() {
			enabled = true
		}
	}
	return map[string]bool{"enabled": enabled}, nil
}
