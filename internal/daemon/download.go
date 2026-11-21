package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"tg-cli/internal/chatref"
	"tg-cli/internal/fileroot"
	"tg-cli/internal/rpc"
)

type downloadTelegram interface {
	Download(context.Context, chatref.Ref, []int, func(rpc.DownloadedFile, func(io.Writer) error) error) (int64, []rpc.ItemError, error)
}

func (d *daemon) messageDownload(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.DownloadParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if len(p.IDs) == 0 || len(p.IDs) > 20 || !filepath.IsAbs(p.Output) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "provide 1 to 20 message ids and an absolute output directory")
	}
	for _, id := range p.IDs {
		if id <= 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "message ids must be positive")
		}
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	policy := d.policy()
	if !policy.DownloadsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, "downloads are disabled: ask the owner to set access.downloads")
	}
	dir, err := fileroot.OpenDownloadDir(policy.DownloadRoots(), p.Output)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "output must be an existing directory within downloads")
	}
	defer dir.Close()
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	if _, err = d.requireAllowedChat(ctx, ref); err != nil {
		return nil, err
	}
	chat, err := d.telegram(ctx).Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	tg := d.telegram(ctx)
	res := rpc.DownloadResult{ChatID: chat.ID, Files: []rpc.DownloadedFile{}, DryRun: p.DryRun}
	var total int64
	gotID, failures, err := tg.Download(ctx, chatref.Ref{Kind: chatref.ID, ID: chat.ID}, p.IDs, func(f rpc.DownloadedFile, fetch func(io.Writer) error) error {
		if f.Size < 0 || f.Size > fileroot.MaxFileSize {
			return rpc.NewError(rpc.CodeInvalidParams, "file exceeds 100 MiB")
		}
		if f.Size > 500*1024*1024-total {
			return rpc.NewError(rpc.CodeInvalidParams, "files exceed 500 MiB in total")
		}
		ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif", "video/mp4": "mp4", "video/quicktime": "mov", "video/webm": "webm", "audio/ogg": "ogg", "audio/opus": "oga", "audio/mpeg": "mp3", "audio/mp4": "m4a", "application/pdf": "pdf", "application/zip": "zip", "text/plain": "txt"}[f.MIME]
		if ext == "" {
			ext = "bin"
		}
		name := fmt.Sprintf("%d_%d.%s", chat.ID, f.MessageID, ext)
		f.Path = dir.Path(name)
		if !p.DryRun {
			sum, e := dir.WriteParallel(ctx, name, min(fileroot.MaxFileSize, 500*1024*1024-total), f.Size, fetch)
			if e != nil {
				if errors.Is(e, fileroot.ErrDownloadSize) {
					return rpc.NewError(rpc.CodeTelegram, "downloaded size does not match")
				}
				var re *rpc.Error
				if errors.As(e, &re) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
					return e
				}
				return rpc.NewError(rpc.CodeInvalidParams, "cannot write downloaded file")
			}
			f.SHA256 = sum
		}
		total += f.Size
		res.Files = append(res.Files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if gotID != chat.ID {
		return nil, rpc.ErrChatNotFound()
	}
	res.Errors = failures
	res.Partial = len(failures) > 0
	if len(res.Files) == 0 && len(failures) > 0 {
		code := rpc.CodeTelegram
		switch failures[0].Code {
		case "usage":
			code = rpc.CodeInvalidParams
		case "not_found":
			code = rpc.CodeNotFound
		case "flood_wait":
			code = rpc.CodeFloodWait
		}
		if code == rpc.CodeFloodWait {
			return nil, &rpc.Error{Code: code, Message: failures[0].Message, Data: map[string]any{"retry_after_seconds": failures[0].RetryAfterSeconds}}
		}
		return nil, rpc.NewError(code, failures[0].Message)
	}
	return res, nil
}
