package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"unicode/utf8"

	"tg-cli/internal/fileroot"
	"tg-cli/internal/mediameta"
	"tg-cli/internal/rpc"
)

const mediaOutsideRoots = "file is outside the allowed roots"

func validSendMediaParams(p rpc.SendMediaParams) error {
	if p.SelfDestruct != "" {
		if _, err := rpc.ParseSelfDestruct(p.SelfDestruct); err != nil {
			return err
		}
		if (p.As != "photo" && p.As != "video") || len(p.Files) != 1 {
			return rpc.NewError(rpc.CodeInvalidParams, "self-destruct requires one photo or video")
		}
	}
	switch p.As {
	case "photo", "video", "audio", "voice", "document":
	default:
		return rpc.NewError(rpc.CodeInvalidParams, "invalid media type: use photo, video, audio, voice or document")
	}
	if len(p.Files) < 1 || len(p.Files) > 10 || (p.As == "voice" && len(p.Files) != 1) {
		return rpc.NewError(rpc.CodeInvalidParams, "media requires one file or an album of 2 to 10 files; voice requires one file")
	}
	for _, path := range p.Files {
		if !filepath.IsAbs(path) {
			return rpc.NewError(rpc.CodeInvalidParams, mediaOutsideRoots)
		}
	}
	if !utf8.ValidString(p.Caption) {
		return rpc.NewError(rpc.CodeInvalidParams, "caption must be at most 1024 characters")
	}
	if utf8.RuneCountInString(p.Caption) > 1024 {
		return rpc.CaptionTooLong()
	}
	if p.Duration < 0 || p.Width < 0 || p.Height < 0 || p.ReplyTo < 0 || p.Topic < 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "reply, topic and media metadata must not be negative")
	}
	return nil
}

func mediaFileError(err error) *rpc.Error {
	switch {
	case errors.Is(err, fileroot.ErrOutsideRoots), errors.Is(err, fileroot.ErrNotAbsolute):
		return rpc.NewError(rpc.CodeInvalidParams, mediaOutsideRoots)
	case errors.Is(err, fileroot.ErrNotFound):
		return rpc.NewError(rpc.CodeNotFound, "file not found")
	case errors.Is(err, fileroot.ErrEmpty):
		return rpc.NewError(rpc.CodeInvalidParams, "file is empty")
	case errors.Is(err, fileroot.ErrTooLarge):
		return rpc.NewError(rpc.CodeInvalidParams, "file exceeds 100 MiB")
	case errors.Is(err, fileroot.ErrChanged):
		return rpc.NewError(rpc.CodeInvalidParams, "file changed while opening")
	default:
		return rpc.NewError(rpc.CodeInvalidParams, "file must be a regular file")
	}
}

func (d *daemon) messageSendMedia(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SendMediaParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := validSendMediaParams(p); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	ref, chatID, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.SelfDestruct != "" {
		chat, err := d.telegram(ctx).Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		if chat.Type != "user" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "self-destruct requires a private user chat")
		}
	}
	policy := d.policy()
	if !policy.Allowed(chatID) {
		return nil, rpc.ErrChatNotFound()
	}
	if !policy.WriteAllowed(chatID) {
		return nil, rpc.ErrWriteDisabled()
	}
	if !policy.FilesAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("sending files is disabled: ask the owner to add file roots to files in access.json"))
	}
	roots := policy.FileRoots()
	openFile := d.openMediaFile
	if openFile == nil {
		openFile = fileroot.Open
	}
	files := make([]rpc.MediaFile, 0, len(p.Files))
	var total int64
	for _, path := range p.Files {
		file, size, err := openFile(roots, path, fileroot.MaxFileSize)
		if err != nil {
			return nil, mediaFileError(err)
		}
		defer func() { _ = file.Close() }()
		if size > fileroot.MaxFileSize-total {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "files exceed 100 MiB in total")
		}
		total += size
		files = append(files, rpc.MediaFile{Name: filepath.Base(path), Size: size, File: file})
	}
	for i := range files {
		if err := checkMediaContent(p.As, &files[i], p); err != nil {
			return nil, err
		}
	}
	canonical := p
	canonical.DryRun = false
	var fingerprints [][32]byte
	if p.IdempotencyKey != "" {
		for _, file := range files {
			if _, err := file.File.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			h := sha256.New()
			if _, err := io.Copy(h, file.File); err != nil {
				return nil, err
			}
			var digest [32]byte
			copy(digest[:], h.Sum(nil))
			fingerprints = append(fingerprints, digest)
		}
	}
	session, saved, available, err := d.beginIdempotency(ctx, chatID, p.IdempotencyKey, rpc.MethodMessageSendMedia, struct {
		Params rpc.SendMediaParams
		Files  [][32]byte
	}{canonical, fingerprints}, p.DryRun)
	if err != nil {
		return nil, err
	}
	defer session.unlock()
	if saved != nil {
		var replay rpc.SendMediaResult
		if err := json.Unmarshal(saved, &replay); err != nil {
			return nil, err
		}
		replay.Replayed = true
		return replay, nil
	}
	if p.DryRun {
		types := make([]string, len(files))
		sizes := make([]int64, len(files))
		for i, file := range files {
			types[i], sizes[i] = p.As, file.Size
		}
		result := rpc.SendMediaResult{ChatID: chatID, Messages: []rpc.MessageRef{}, DryRun: true, Action: "send_media", Count: len(files), Types: types, Sizes: sizes, CaptionLength: utf8.RuneCountInString(p.Caption), SelfDestruct: p.SelfDestruct}
		if p.IdempotencyKey != "" {
			result.ReplayAvailable = &available
		}
		return result, nil
	}
	res, err := d.telegram(ctx).SendMedia(ctx, ref, p, files)
	if err != nil {
		var rpcErr *rpc.Error
		if errors.As(err, &rpcErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, rpc.NewError(rpc.CodeTelegram, "media send failed")
	}
	if len(res.Messages) != len(files) {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram did not return all sent message ids")
	}
	res.ChatID = chatID
	res.SelfDestruct = p.SelfDestruct
	stored := make([]rpc.Message, 0, len(res.Messages))
	for i, item := range res.Messages {
		if item.ID <= 0 || item.Date == "" {
			return nil, rpc.NewError(rpc.CodeTelegram, "Telegram did not return a message id and date")
		}
		text := ""
		if i == 0 {
			text = p.Caption
		}
		stored = append(stored, rpc.Message{ChatID: chatID, ID: item.ID, Date: item.Date, Raw: item.Raw, Text: text, MediaType: p.As, Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic})
	}
	d.saveMessages(ctx, rpc.MethodMessageSendMedia, stored)
	if err := session.complete(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

func checkMediaContent(as string, file *rpc.MediaFile, p rpc.SendMediaParams) error {
	switch as {
	case "photo":
		width, height, err := mediameta.Image(file.File, file.Size)
		if err != nil {
			return rpc.NewError(rpc.CodeInvalidParams, "file is not a JPEG or PNG photo; use --as document")
		}
		file.Width, file.Height = width, height
		file.MIME = photoMIME(file.File)
	case "video":
		duration, width, height, err := mediameta.Video(file.File, file.Size)
		if errors.Is(err, mediameta.ErrNotVideo) {
			return rpc.NewError(rpc.CodeInvalidParams, "file is not a supported MP4 video; use --as document")
		}
		if err == nil {
			file.Duration, file.Width, file.Height = duration, width, height
		}
		if chooseMediaValue(p.Duration, file.Duration) <= 0 || chooseMediaValue(p.Width, file.Width) <= 0 || chooseMediaValue(p.Height, file.Height) <= 0 {
			return rpc.NewError(rpc.CodeInvalidParams, "video metadata unavailable: pass --duration --width --height")
		}
		file.MIME = "video/mp4"
	case "voice":
		duration, err := mediameta.Voice(file.File, file.Size)
		if errors.Is(err, mediameta.ErrNotOpus) {
			return rpc.NewError(rpc.CodeInvalidParams, "file is not Ogg Opus voice audio; use --as document")
		}
		if err == nil {
			file.Duration = duration
		}
		if chooseMediaValue(p.Duration, file.Duration) <= 0 {
			return rpc.NewError(rpc.CodeInvalidParams, "voice metadata unavailable: pass --duration")
		}
		file.MIME = "audio/ogg"
	case "audio":
		mimeType, err := mediameta.Audio(file.File, file.Size)
		if err != nil {
			return rpc.NewError(rpc.CodeInvalidParams, "audio format is unknown; use --as document")
		}
		file.MIME = mimeType
	case "document":
		file.MIME = documentMIME(file.File, file.Size, file.Name)
	}
	return nil
}

func chooseMediaValue(explicit, detected int) int {
	if explicit != 0 {
		return explicit
	}
	return detected
}

func photoMIME(file *os.File) string {
	var head [8]byte
	if n, _ := file.ReadAt(head[:], 0); n >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff {
		return "image/jpeg"
	}
	return "image/png"
}

func documentMIME(file *os.File, size int64, name string) string {
	if audioType, err := mediameta.Audio(file, size); err == nil {
		return audioType
	}
	if _, err := mediameta.Voice(file, size); err == nil {
		return "audio/ogg"
	}
	if _, _, err := mediameta.Image(file, size); err == nil {
		return photoMIME(file)
	}
	if _, _, _, err := mediameta.Video(file, size); err == nil {
		return "video/mp4"
	}
	value := mime.TypeByExtension(filepath.Ext(name))
	if value == "" {
		return "application/octet-stream"
	}
	plain, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "application/octet-stream"
	}
	return plain
}
