package tgclient

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/uploader"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) SetAutoDelete(ctx context.Context, ref chatref.Ref, period int) error {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return err
	}
	return setAutoDelete(ctx, c.tg.API(), p.InputPeer(), period)
}
func setAutoDelete(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, period int) error {
	_, err := api.MessagesSetHistoryTTL(ctx, &tg.MessagesSetHistoryTTLRequest{Peer: peer, Period: period})
	if tgerr.Is(err, "TTL_PERIOD_INVALID") {
		return rpc.NewError(rpc.CodeInvalidParams, "auto-delete period was rejected by Telegram")
	}
	return mapActionError(err)
}

func (c *Client) PressButton(ctx context.Context, ref chatref.Ref, p rpc.PressParams) (rpc.PressResult, error) {
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.PressResult{}, err
	}
	elems, err := query.Messages(c.tg.API()).GetMessagesWithEntities(ctx, peer.InputPeer(), p.ID)
	if err != nil {
		if isNotFound(err) {
			return rpc.PressResult{}, messageNotFound()
		}
		return rpc.PressResult{}, mapError(err)
	}
	for _, e := range elems {
		if m, ok := e.Msg.(*tg.Message); ok && m.ID == p.ID {
			return pressMarkup(ctx, c.tg.API(), peer.InputPeer(), int64(peer.TDLibPeerID()), p, m.ReplyMarkup)
		}
	}
	return rpc.PressResult{}, messageNotFound()
}
func pressMarkup(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, chatID int64, p rpc.PressParams, markup tg.ReplyMarkupClass) (rpc.PressResult, error) {
	if (p.Button == nil) == (p.Text == nil) {
		return rpc.PressResult{}, rpc.NewError(rpc.CodeInvalidParams, "choose exactly one of button or text")
	}
	buttons := buttonsOf(chatID, p.ID, markup).Buttons
	matches := make([]int, 0)
	for i, b := range buttons {
		if p.Button != nil && *p.Button == b.Index || p.Text != nil && *p.Text == b.Text {
			matches = append(matches, i)
		}
	}
	if len(matches) != 1 {
		return rpc.PressResult{}, rpc.NewError(rpc.CodeInvalidParams, fmt.Sprintf("button selection matched indices %v", matches))
	}
	button := buttons[matches[0]]
	if !button.Pressable {
		return rpc.PressResult{}, rpc.NewError(rpc.CodeInvalidParams, "button type cannot be pressed: "+button.Type)
	}
	result := rpc.PressResult{ChatID: chatID, MessageID: p.ID, Button: rpc.PressButton{Index: button.Index, Text: button.Text, Type: button.Type}, DryRun: p.DryRun}
	if p.DryRun {
		return result, nil
	}
	m := markup.(*tg.ReplyInlineMarkup)
	callback := m.Rows[button.Row].Buttons[button.Column].Type.(*tg.InlineButtonTypeCallback)
	req := &tg.MessagesGetBotCallbackAnswerRequest{Peer: peer, MsgID: p.ID}
	req.SetData(callback.Data)
	answer, err := api.MessagesGetBotCallbackAnswer(ctx, req)
	if tgerr.Is(err, "BOT_RESPONSE_TIMEOUT") {
		return result, nil
	}
	if err != nil {
		return rpc.PressResult{}, mapActionError(err)
	}
	result.Answered = true
	result.Alert = answer.Alert
	result.Message = answer.Message
	result.URL = answer.URL
	return result, nil
}

func (c *Client) InlineQuery(ctx context.Context, ref, botRef chatref.Ref, p rpc.InlineQueryParams) (rpc.InlineQueryResult, error) {
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.InlineQueryResult{}, err
	}
	bot, err := c.resolveUser(ctx, botRef)
	if err != nil {
		return rpc.InlineQueryResult{}, err
	}
	response, err := c.tg.API().MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{Bot: bot.InputUser(), Peer: peer.InputPeer(), Query: p.Query, Offset: p.Offset})
	if err != nil {
		return rpc.InlineQueryResult{}, mapError(err)
	}
	result := rpc.InlineQueryResult{ChatID: int64(peer.TDLibPeerID()), BotID: bot.ID(), QueryID: strconv.FormatInt(response.QueryID, 10), NextOffset: response.NextOffset, Gallery: response.Gallery, CacheTime: response.CacheTime, Results: []rpc.InlineResult{}}
	for _, raw := range response.Results {
		switch v := raw.(type) {
		case *tg.BotInlineResult:
			result.Results = append(result.Results, rpc.InlineResult{ID: v.ID, Type: v.Type, Title: v.Title, Description: v.Description, URL: v.URL, HasMedia: v.Content != nil})
		case *tg.BotInlineMediaResult:
			result.Results = append(result.Results, rpc.InlineResult{ID: v.ID, Type: v.Type, Title: v.Title, Description: v.Description, HasMedia: v.Photo != nil || v.Document != nil})
		}
	}
	return result, nil
}

func (c *Client) SendInline(ctx context.Context, ref chatref.Ref, p rpc.SendInlineParams) (rpc.SendResult, *rpc.Message, error) {
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendResult{}, nil, err
	}
	account, err := c.idempotencyAccount(ctx, p.IdempotencyKey)
	if err != nil {
		return rpc.SendResult{}, nil, err
	}
	return sendInline(ctx, c.tg.API(), peer.InputPeer(), int64(peer.TDLibPeerID()), account, p)
}
func sendInline(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, chatID, account int64, p rpc.SendInlineParams) (rpc.SendResult, *rpc.Message, error) {
	query, err := strconv.ParseInt(p.QueryID, 10, 64)
	if err != nil || query == 0 {
		return rpc.SendResult{}, nil, rpc.NewError(rpc.CodeInvalidParams, "invalid query id; repeat inline-query")
	}
	var random int64
	if p.IdempotencyKey != "" {
		random = deterministicID(account, chatID, p.IdempotencyKey, 0)
	} else {
		random, err = newRandomID()
		if err != nil {
			return rpc.SendResult{}, nil, err
		}
	}
	req := &tg.MessagesSendInlineBotResultRequest{Peer: peer, Silent: p.Silent, RandomID: random, QueryID: query, ID: p.Result}
	reply := p.ReplyTo
	if reply == 0 {
		reply = p.Topic
	}
	if reply > 0 {
		r := &tg.InputReplyToMessage{ReplyToMsgID: reply}
		if p.Topic > 1 && p.ReplyTo > 0 && p.ReplyTo != p.Topic {
			r.SetTopMsgID(p.Topic)
		}
		req.SetReplyTo(r)
	}
	updates, err := api.MessagesSendInlineBotResult(ctx, req)
	if tgerr.Is(err, "QUERY_ID_INVALID") || tgerr.Is(err, "RESULT_ID_INVALID") {
		return rpc.SendResult{}, nil, rpc.NewError(rpc.CodeInvalidParams, "inline result expired or invalid; repeat inline-query")
	}
	if err != nil {
		return rpc.SendResult{}, nil, mapActionError(err)
	}
	refs, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendResult{}, nil, err
	}
	result := rpc.SendResult{ChatID: chatID, ID: refs[0].ID, Date: refs[0].Date}
	var changes []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		changes = u.Updates
	case *tg.UpdatesCombined:
		changes = u.Updates
	case *tg.UpdateShort:
		changes = []tg.UpdateClass{u.Update}
	}
	var stored *rpc.Message
	for _, change := range changes {
		var raw tg.MessageClass
		switch v := change.(type) {
		case *tg.UpdateNewMessage:
			raw = v.Message
		case *tg.UpdateNewChannelMessage:
			raw = v.Message
		}
		if m, ok := raw.(*tg.Message); ok && m.ID == result.ID {
			stored = &rpc.Message{ChatID: chatID, ID: m.ID, Date: formatDate(m.Date), Text: m.Message, MediaType: mediaType(m.Media), Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic}
			break
		}
	}
	if stored == nil {
		stored = &rpc.Message{ChatID: chatID, ID: result.ID, Date: result.Date, Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic}
		switch u := updates.(type) {
		case *tg.UpdateShortMessage:
			stored.Text = u.Message
		case *tg.UpdateShortChatMessage:
			stored.Text = u.Message
		case *tg.UpdateShortSentMessage:
			stored.MediaType = mediaType(u.Media)
		}
	}
	return result, stored, nil
}

func (c *Client) EditMedia(ctx context.Context, ref chatref.Ref, p rpc.EditMediaParams, file rpc.MediaFile) (rpc.SendResult, error) {
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendResult{}, err
	}
	if _, err = file.File.Seek(0, 0); err != nil {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "media file cannot be read")
	}
	uploaded, err := uploader.NewUploader(c.tg.API()).WithThreads(1).FromFile(ctx, file.File)
	if err != nil {
		return rpc.SendResult{}, mapError(err)
	}
	media, err := makeMediaSpec(uploaded, rpc.SendMediaParams{As: p.As, Duration: p.Duration, Width: p.Width, Height: p.Height}, file, "")
	if err != nil {
		return rpc.SendResult{}, err
	}
	req := editMediaRequest(peer.InputPeer(), p.ID, media.raw, p.Caption)
	updates, err := c.tg.API().MessagesEditMessage(ctx, req)
	if err != nil {
		return rpc.SendResult{}, mapActionError(err)
	}
	refs, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return rpc.SendResult{ChatID: int64(peer.TDLibPeerID()), ID: refs[0].ID, Date: refs[0].Date}, nil
}

func editMediaRequest(peer tg.InputPeerClass, id int, media tg.InputMediaClass, caption *string) *tg.MessagesEditMessageRequest {
	req := &tg.MessagesEditMessageRequest{Peer: peer, ID: id}
	req.SetMedia(media)
	if caption != nil {
		req.SetMessage(*caption)
	}
	return req
}

func (c *Client) SendSticker(ctx context.Context, ref chatref.Ref, p rpc.SendStickerParams) (rpc.SendStickerResult, error) {
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendStickerResult{}, err
	}
	set, err := getStickerSet(ctx, c.tg.API(), p.Set)
	if err != nil {
		return rpc.SendStickerResult{}, err
	}
	doc, index, err := selectSticker(set, p)
	if err != nil {
		return rpc.SendStickerResult{}, err
	}
	selected := stickerOf(index, doc)
	if p.DryRun {
		return rpc.SendStickerResult{SendResult: rpc.SendResult{ChatID: int64(peer.TDLibPeerID()), DryRun: true, Action: "send_sticker", Count: 1}, Sticker: &selected}, nil
	}
	account, err := c.idempotencyAccount(ctx, p.IdempotencyKey)
	if err != nil {
		return rpc.SendStickerResult{}, err
	}
	var random int64
	if p.IdempotencyKey != "" {
		random = deterministicID(account, int64(peer.TDLibPeerID()), p.IdempotencyKey, 0)
	} else {
		random, err = newRandomID()
		if err != nil {
			return rpc.SendStickerResult{}, err
		}
	}
	req := &tg.MessagesSendMediaRequest{Peer: peer.InputPeer(), Media: &tg.InputMediaDocument{ID: doc.AsInput()}, RandomID: random, Silent: p.Silent}
	reply := p.ReplyTo
	if reply == 0 {
		reply = p.Topic
	}
	if reply > 0 {
		r := &tg.InputReplyToMessage{ReplyToMsgID: reply}
		if p.Topic > 1 && p.ReplyTo > 0 && p.ReplyTo != p.Topic {
			r.SetTopMsgID(p.Topic)
		}
		req.SetReplyTo(r)
	}
	updates, err := c.tg.API().MessagesSendMedia(ctx, req)
	if err != nil {
		return rpc.SendStickerResult{}, mapActionError(err)
	}
	refs, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendStickerResult{}, err
	}
	return rpc.SendStickerResult{SendResult: rpc.SendResult{ChatID: int64(peer.TDLibPeerID()), ID: refs[0].ID, Date: refs[0].Date}}, nil
}

func selectSticker(set *tg.MessagesStickerSet, p rpc.SendStickerParams) (*tg.Document, int, error) {
	if (p.Index == nil) == (p.Emoji == nil) {
		return nil, 0, rpc.NewError(rpc.CodeInvalidParams, "choose exactly one of index or emoji")
	}
	index := -1
	if p.Index != nil {
		index = *p.Index
	} else {
		for i, raw := range set.Documents {
			doc, ok := raw.(*tg.Document)
			if !ok {
				continue
			}
			match := false
			for _, pack := range set.Packs {
				if pack.Emoticon == *p.Emoji {
					for _, id := range pack.Documents {
						if id == doc.ID {
							match = true
							break
						}
					}
				}
			}
			if match || stickerOf(i, doc).Emoji == *p.Emoji {
				index = i
				break
			}
		}
	}
	if index < 0 || index >= len(set.Documents) {
		return nil, 0, rpc.NewError(rpc.CodeInvalidParams, "sticker was not found in set")
	}
	doc, ok := set.Documents[index].(*tg.Document)
	if !ok {
		return nil, 0, rpc.NewError(rpc.CodeTelegram, "Telegram returned an invalid sticker")
	}
	return doc, index, nil
}
