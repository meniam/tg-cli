package tgclient

import (
	"context"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/thumbnail"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) WebThumbnail(ctx context.Context, chatID int64, id int, limit int, w io.Writer) error {
	p, err := c.resolve(ctx, chatref.Ref{Kind: chatref.ID, ID: chatID})
	if err != nil {
		return err
	}
	if toChatOf(p).ID != chatID {
		return rpc.ErrChatNotFound()
	}
	api := c.tg.API()
	items, err := query.Messages(api).GetMessagesWithEntities(ctx, p.InputPeer(), id)
	if err != nil {
		return mapError(err)
	}
	for _, item := range items {
		m, ok := item.Msg.(*tg.Message)
		if !ok || m.ID != id {
			continue
		}
		if match, protected := downloadProtection(p, chatID, item, m); !match || protected {
			return rpc.ErrChatNotFound()
		}
		loc, dc := thumbnailLocation(m, limit)
		if loc == nil {
			return rpc.NewError(rpc.CodeNotFound, "thumbnail unavailable")
		}
		release, err := c.transfers.acquire(ctx)
		if err != nil {
			return err
		}
		defer release()
		partAPI, err := c.transfers.fileAPI(ctx, c.tg, dc, c.transferAutoWait())
		if err != nil {
			return mapError(err)
		}
		_, err = downloader.NewDownloader().Download(downloadClient{partAPI}, loc).Stream(ctx, w)
		return mapError(err)
	}
	return rpc.ErrChatNotFound()
}

func thumbnailLocation(m *tg.Message, limit int) (tg.InputFileLocationClass, int) {
	switch media := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := media.Photo.(*tg.Photo); ok && media.TTLSeconds == 0 {
			size := thumbnailSize(photo.Sizes, limit)
			if size != "" {
				return &tg.InputPhotoFileLocation{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: size}, photo.DCID
			}
		}
	case *tg.MessageMediaDocument:
		if doc, ok := media.Document.(*tg.Document); ok && media.TTLSeconds == 0 {
			size := thumbnailSize(doc.Thumbs, limit)
			if size != "" {
				return &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference, ThumbSize: size}, doc.DCID
			}
		}
	}
	return nil, 0
}

func thumbnailSize(sizes []tg.PhotoSizeClass, limit int) string {
	var name string
	var area int
	for _, candidate := range sizes {
		size, ok := candidate.(*tg.PhotoSize)
		if ok && size.Size > 0 && size.Size <= limit && size.W > 0 && size.H > 0 && max(size.W, size.H) <= 400 && size.W*size.H > area {
			name, area = size.Type, size.W*size.H
		}
	}
	return name
}

// ExpandWebThumbnail decodes a locally saved Telegram thumbnail.
func (c *Client) ExpandWebThumbnail(data []byte) ([]byte, error) { return thumbnail.Expand(data) }
