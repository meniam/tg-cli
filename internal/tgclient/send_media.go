package tgclient

import (
	"context"
	"mime"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/fileroot"
	"tg-cli/internal/rpc"
)

const maxMediaCaptionRunes = 1024

func (c *Client) SendMedia(ctx context.Context, ref chatref.Ref, p rpc.SendMediaParams, files []rpc.MediaFile) (rpc.SendMediaResult, error) {
	accountID, accountErr := c.idempotencyAccount(ctx, p.IdempotencyKey)
	if accountErr != nil {
		return rpc.SendMediaResult{}, accountErr
	}
	p.AccountID = accountID

	if err := validateMedia(p, files); err != nil {
		return rpc.SendMediaResult{}, err
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendMediaResult{}, err
	}
	if p.SelfDestruct != "" {
		if toChatOf(resolved).Type != "user" {
			return rpc.SendMediaResult{}, rpc.NewError(rpc.CodeInvalidParams, "self-destruct requires a private user chat")
		}
	}
	return sendMediaWithTransfers(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p, files, c.transfers, c.tg, c.Connection().DC, c.transferAutoWait())
}

func validateMedia(p rpc.SendMediaParams, files []rpc.MediaFile) error {
	if p.SelfDestruct != "" {
		if _, err := rpc.ParseSelfDestruct(p.SelfDestruct); err != nil {
			return err
		}
		if (p.As != "photo" && p.As != "video") || len(files) != 1 {
			return rpc.NewError(rpc.CodeInvalidParams, "self-destruct requires one photo or video")
		}
	}
	if p.DryRun {
		return rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if len(files) == 0 || len(files) > 10 || (p.As == "voice" && len(files) != 1) {
		return rpc.NewError(rpc.CodeInvalidParams, "media requires one file, or an album of 2 to 10 files")
	}
	switch p.As {
	case "photo", "video", "audio", "voice", "document":
	default:
		return rpc.NewError(rpc.CodeInvalidParams, "invalid media type")
	}
	if !utf8.ValidString(p.Caption) {
		return rpc.NewError(rpc.CodeInvalidParams, "caption must be at most 1024 characters")
	}
	if utf8.RuneCountInString(p.Caption) > maxMediaCaptionRunes {
		return rpc.CaptionTooLong()
	}
	if p.ReplyTo < 0 || p.Topic < 0 || p.Duration < 0 || p.Width < 0 || p.Height < 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "reply, topic and media metadata must not be negative")
	}
	for _, file := range files {
		if file.File == nil || file.Name == "" || filepath.Base(file.Name) != file.Name ||
			file.Size < 1 || file.Size > fileroot.MaxFileSize ||
			file.Duration < 0 || file.Width < 0 || file.Height < 0 {
			return rpc.NewError(rpc.CodeInvalidParams, "invalid media file")
		}
		info, err := file.File.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return rpc.NewError(rpc.CodeInvalidParams, "invalid media file")
		}
		duration := valueOr(p.Duration, file.Duration)
		width := valueOr(p.Width, file.Width)
		height := valueOr(p.Height, file.Height)
		if (p.As == "video" && (duration <= 0 || width <= 0 || height <= 0)) ||
			(p.As == "voice" && duration <= 0) {
			return rpc.NewError(rpc.CodeInvalidParams, "media duration or dimensions are missing")
		}
	}
	return nil
}

func valueOr(explicit, detected int) int {
	if explicit != 0 {
		return explicit
	}
	return detected
}

type mediaSpec struct {
	option message.MultiMediaOption
	raw    tg.InputMediaClass
}

func sendMedia(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.SendMediaParams, files []rpc.MediaFile) (rpc.SendMediaResult, error) {
	return sendMediaWithTransfers(ctx, api, destination, chatID, p, files, nil, nil, 0, 0)
}

func sendMediaWithTransfers(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.SendMediaParams, files []rpc.MediaFile, transfers *transfers, invokers fileInvokers, dc int, autoWait time.Duration) (rpc.SendMediaResult, error) {
	wrappedAPI, wrapErr := idempotentAPI(api, p.AccountID, chatID, p.IdempotencyKey)
	if wrapErr != nil {
		return rpc.SendMediaResult{}, wrapErr
	}
	api = wrappedAPI

	if err := validateMedia(p, files); err != nil {
		return rpc.SendMediaResult{}, err
	}
	u := uploader.NewUploader(api).WithThreads(1)
	specs := make([]mediaSpec, 0, len(files))
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return rpc.SendMediaResult{}, err
		}
		if _, err := file.File.Seek(0, 0); err != nil {
			return rpc.SendMediaResult{}, rpc.NewError(rpc.CodeInvalidParams, "media file cannot be read")
		}
		var uploaded tg.InputFileClass
		var err error
		if transfers == nil {
			uploaded, err = u.FromFile(ctx, file.File)
		} else {
			uploaded, err = transfers.upload(ctx, invokers, dc, autoWait, file.File)
		}
		if err != nil {
			return rpc.SendMediaResult{}, mapError(err)
		}
		caption := ""
		if i == 0 {
			caption = p.Caption
		}
		spec, err := makeMediaSpec(uploaded, p, file, caption)
		if err != nil {
			return rpc.SendMediaResult{}, err
		}
		specs = append(specs, spec)
	}
	builder := message.NewSender(api).To(destination)
	if p.Silent {
		builder.Silent()
	}
	reply := p.ReplyTo
	if reply == 0 {
		reply = p.Topic
	}
	if reply > 0 {
		builder.Reply(reply)
	}
	var updates tg.UpdatesClass
	var err error
	if p.Topic > 1 && p.ReplyTo > 0 && p.ReplyTo != p.Topic {
		updates, err = sendMediaWithTop(ctx, api, destination, p, specs)
	} else if len(specs) == 1 {
		updates, err = builder.Media(ctx, specs[0].option)
	} else {
		updates, err = sendAlbum(ctx, builder, specs)
	}
	if err != nil {
		return rpc.SendMediaResult{}, mapError(err)
	}
	refs, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendMediaResult{}, err
	}
	if len(refs) != len(files) {
		return rpc.SendMediaResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return all sent message ids")
	}
	return rpc.SendMediaResult{ChatID: chatID, Messages: refs}, nil
}

func sendAlbum(ctx context.Context, builder *message.RequestBuilder, specs []mediaSpec) (tg.UpdatesClass, error) {
	if len(specs) < 2 || len(specs) > 10 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "album requires 2 to 10 files")
	}
	options := make([]message.MultiMediaOption, len(specs)-1)
	for i := 1; i < len(specs); i++ {
		options[i-1] = specs[i].option
	}
	return builder.Album(ctx, specs[0].option, options...)
}

func makeMediaSpec(uploaded tg.InputFileClass, p rpc.SendMediaParams, file rpc.MediaFile, caption string) (mediaSpec, error) {
	ttl := 0
	if p.SelfDestruct != "" {
		var err error
		ttl, err = rpc.ParseSelfDestruct(p.SelfDestruct)
		if err != nil {
			return mediaSpec{}, err
		}
	}
	var opts []message.StyledTextOption
	if caption != "" {
		opts = []message.StyledTextOption{styling.Plain(caption)}
	}
	duration := valueOr(p.Duration, file.Duration)
	width := valueOr(p.Width, file.Width)
	height := valueOr(p.Height, file.Height)
	if p.As == "photo" {
		return mediaSpec{option: message.UploadedPhoto(uploaded, opts...).TTLSeconds(ttl), raw: &tg.InputMediaUploadedPhoto{File: uploaded, TTLSeconds: ttl}}, nil
	}
	mimeType := file.MIME
	if mimeType == "" {
		mimeType = mimeByFilename(file.Name)
	}
	attributes := []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: file.Name}}
	raw := &tg.InputMediaUploadedDocument{File: uploaded, MimeType: mimeType, Attributes: attributes}
	base := message.UploadedDocument(uploaded, opts...).MIME(mimeType).Filename(file.Name)
	switch p.As {
	case "document":
		raw.ForceFile = true
		return mediaSpec{option: base.ForceFile(true), raw: raw}, nil
	case "video":
		raw.MimeType = "video/mp4"
		raw.TTLSeconds = ttl
		raw.Attributes = append(attributes, &tg.DocumentAttributeVideo{Duration: float64(duration), W: width, H: height, SupportsStreaming: true})
		return mediaSpec{option: base.TTLSeconds(ttl).MIME("video/mp4").Video().DurationSeconds(duration).Resolution(width, height).SupportsStreaming(), raw: raw}, nil
	case "audio":
		if file.MIME == "" && mimeType == "application/octet-stream" {
			raw.MimeType = "audio/mpeg"
			base.MIME("audio/mpeg")
		}
		raw.Attributes = append(attributes, &tg.DocumentAttributeAudio{Duration: duration})
		return mediaSpec{option: base.Audio().DurationSeconds(duration), raw: raw}, nil
	case "voice":
		raw.MimeType = "audio/ogg"
		raw.Attributes = append(attributes, &tg.DocumentAttributeAudio{Voice: true, Duration: duration})
		return mediaSpec{option: base.Voice().DurationSeconds(duration), raw: raw}, nil
	}
	return mediaSpec{}, rpc.NewError(rpc.CodeInvalidParams, "invalid media type")
}

func mimeByFilename(name string) string {
	value := mime.TypeByExtension(filepath.Ext(name))
	if value == "" {
		return "application/octet-stream"
	}
	base, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "application/octet-stream"
	}
	return base
}

// Builder cannot set top_msg_id. This path preserves it when replying inside a topic.
func sendMediaWithTop(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, p rpc.SendMediaParams, specs []mediaSpec) (tg.UpdatesClass, error) {
	reply := &tg.InputReplyToMessage{ReplyToMsgID: p.ReplyTo}
	reply.SetTopMsgID(p.Topic)
	if len(specs) == 1 {
		id, err := newRandomID()
		if err != nil {
			return nil, err
		}
		return api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer: destination, Media: specs[0].raw, Message: p.Caption,
			Silent: p.Silent, ReplyTo: reply, RandomID: id,
		})
	}
	if len(specs) < 2 || len(specs) > 10 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "album requires 2 to 10 files")
	}
	items := make([]tg.InputSingleMedia, 0, len(specs))
	for i, spec := range specs {
		media, err := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: destination, Media: spec.raw})
		if err != nil {
			return nil, err
		}
		input, err := uploadedMediaInput(media)
		if err != nil {
			return nil, err
		}
		id, err := newRandomID()
		if err != nil {
			return nil, err
		}
		item := tg.InputSingleMedia{Media: input, RandomID: id}
		if i == 0 {
			item.Message = p.Caption
		}
		items = append(items, item)
	}
	return api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: destination, MultiMedia: items, Silent: p.Silent, ReplyTo: reply})
}

func uploadedMediaInput(media tg.MessageMediaClass) (tg.InputMediaClass, error) {
	switch value := media.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := value.Photo.AsNotEmpty(); ok {
			return &tg.InputMediaPhoto{ID: photo.AsInput()}, nil
		}
	case *tg.MessageMediaDocument:
		if document, ok := value.Document.AsNotEmpty(); ok {
			return &tg.InputMediaDocument{ID: document.AsInput()}, nil
		}
	}
	return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned unexpected uploaded media")
}
