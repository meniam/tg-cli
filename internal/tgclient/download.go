package tgclient

import (
	"context"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// Download visits requested messages with a fresh file reference.
func (c *Client) Download(ctx context.Context, ref chatref.Ref, ids []int, visit func(rpc.DownloadedFile, func(io.Writer) error) error) (int64, []rpc.ItemError, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return 0, nil, err
	}
	return downloadResolved(ctx, c.tg.API(), p, p.InputPeer(), toChatOf(p).ID, ids, visit)
}

func downloadResolved(ctx context.Context, api *tg.Client, p peers.Peer, input tg.InputPeerClass, chatID int64, ids []int, visit func(rpc.DownloadedFile, func(io.Writer) error) error) (int64, []rpc.ItemError, error) {
	elems, err := query.Messages(api).GetMessagesWithEntities(ctx, input, ids...)
	if err != nil {
		return chatID, nil, mapError(err)
	}
	byID := make(map[int]struct {
		m         *tg.Message
		protected bool
	})
	for _, elem := range elems {
		m, ok := elem.Msg.(*tg.Message)
		if !ok {
			continue
		}
		match, protected := downloadProtection(p, chatID, elem, m)
		if !match {
			continue
		}

		byID[m.ID] = struct {
			m         *tg.Message
			protected bool
		}{m, protected}
	}
	var failures []rpc.ItemError
	for _, id := range ids {
		item, ok := byID[id]
		var e error
		if !ok {
			e = rpc.NewError(rpc.CodeNotFound, "message not found")
		} else if item.protected {
			e = rpc.NewError(rpc.CodeTelegram, "content is protected: saving media is not allowed")
		} else {
			file, loc, er := downloadLocation(item.m)
			e = er
			if e == nil {
				e = visit(file, func(w io.Writer) error {
					_, err := downloader.NewDownloader().Download(downloadClient{api}, loc).WithRefresh(func(ctx context.Context) (tg.InputFileLocationClass, error) {
						fresh, err := query.Messages(api).GetMessagesWithEntities(ctx, input, id)
						if err != nil {
							return nil, mapError(err)
						}
						for _, elem := range fresh {
							msg, ok := elem.Msg.(*tg.Message)
							if !ok || msg.ID != id {
								continue
							}
							match, protected := downloadProtection(p, chatID, elem, msg)
							if !match || protected {
								return nil, rpc.NewError(rpc.CodeTelegram, "content is protected: saving media is not allowed")
							}
							_, next, err := downloadLocation(msg)
							if err != nil {
								return nil, err
							}
							if !sameDownloadLocation(loc, next) {
								return nil, rpc.NewError(rpc.CodeTelegram, "media changed during download")
							}
							return next, nil
						}
						return nil, rpc.NewError(rpc.CodeNotFound, "message not found")
					}).Stream(ctx, w)
					return mapError(err)
				})
			}
		}
		if ctx.Err() != nil {
			return chatID, failures, ctx.Err()
		}
		if e != nil {
			failures = append(failures, rpc.ItemErrorFrom(id, e))
		}
	}
	return chatID, failures, nil
}

func downloadProtection(p peers.Peer, chatID int64, elem messages.Elem, m *tg.Message) (bool, bool) {
	protected := m.Noforwards
	switch x := m.PeerID.(type) {
	case *tg.PeerUser:
		if x.UserID != chatID {
			return false, false
		}
	case *tg.PeerChat:
		if -x.ChatID != chatID {
			return false, false
		}
		if chat := elem.Entities.Chats()[x.ChatID]; chat != nil {
			protected = protected || chat.Noforwards
		} else {
			protected = true
		}
	case *tg.PeerChannel:
		if -channelIDBase-x.ChannelID != chatID {
			return false, false
		}
		if ch := elem.Entities.Channels()[x.ChannelID]; ch != nil {
			protected = protected || ch.Noforwards
		} else {
			protected = true
		}
	default:
		return false, false
	}
	switch peer := p.(type) {
	case peers.Chat:
		protected = protected || peer.Raw().Noforwards
	case peers.Channel:
		protected = protected || peer.Raw().Noforwards
	}
	return true, protected
}
func sameDownloadLocation(a, b tg.InputFileLocationClass) bool {
	switch x := a.(type) {
	case *tg.InputPhotoFileLocation:
		y, ok := b.(*tg.InputPhotoFileLocation)
		return ok && x.ID == y.ID && x.ThumbSize == y.ThumbSize
	case *tg.InputDocumentFileLocation:
		y, ok := b.(*tg.InputDocumentFileLocation)
		return ok && x.ID == y.ID
	}
	return false
}

func downloadLocation(m *tg.Message) (rpc.DownloadedFile, tg.InputFileLocationClass, error) {
	f := rpc.DownloadedFile{MessageID: m.ID}
	switch media := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok || media.TTLSeconds != 0 {
			break
		}
		var typ string
		var size int
		for _, raw := range photo.Sizes {
			var t string
			var n int
			switch x := raw.(type) {
			case *tg.PhotoSize:
				t, n = x.Type, x.Size
			case *tg.PhotoSizeProgressive:
				t = x.Type
				for _, v := range x.Sizes {
					if v > n {
						n = v
					}
				}
			}
			if n > size {
				typ, size = t, n
			}
		}
		if size == 0 {
			break
		}
		f.MediaType = "photo"
		f.MIME = "image/jpeg"
		f.Size = int64(size)
		return f, &tg.InputPhotoFileLocation{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: typ}, nil
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok || media.TTLSeconds != 0 {
			break
		}
		f.MediaType = mediaType(m.Media)
		f.MIME = doc.MimeType
		f.Size = doc.Size
		for _, attr := range doc.Attributes {
			if a, ok := attr.(*tg.DocumentAttributeFilename); ok {
				f.FileName = a.FileName
			}
		}
		return f, &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}, nil
	}
	return f, nil, rpc.NewError(rpc.CodeInvalidParams, "no downloadable media")
}

// downloadClient stops downloader's automatic FLOOD_WAIT retry so the caller can
// return the wait and remove the incomplete file.
type downloadClient struct{ *tg.Client }

func (c downloadClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	result, err := c.Client.UploadGetFile(ctx, req)
	if _, ok := tgerr.AsFloodWait(err); ok {
		return nil, mapError(err)
	}
	return result, err
}
