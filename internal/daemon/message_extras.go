package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"tg-cli/internal/chatref"
	"tg-cli/internal/fileroot"
	"tg-cli/internal/rpc"
)

type messageExtrasTelegram interface {
	MessageButtons(context.Context, chatref.Ref, int) (rpc.ButtonsResult, error)
	PressButton(context.Context, chatref.Ref, rpc.PressParams) (rpc.PressResult, error)
	SetAutoDelete(context.Context, chatref.Ref, int) error
	InlineQuery(context.Context, chatref.Ref, chatref.Ref, rpc.InlineQueryParams) (rpc.InlineQueryResult, error)
	SendInline(context.Context, chatref.Ref, rpc.SendInlineParams) (rpc.SendResult, *rpc.Message, error)
	EditMedia(context.Context, chatref.Ref, rpc.EditMediaParams, rpc.MediaFile) (rpc.SendResult, error)
	StickerSet(context.Context, string) (rpc.StickerSetResult, error)
	SearchStickerSets(context.Context, rpc.StickerSearchParams) (rpc.StickerSetsResult, error)
	InstalledStickerSets(context.Context) (rpc.StickerSetsResult, error)
	SendSticker(context.Context, chatref.Ref, rpc.SendStickerParams) (rpc.SendStickerResult, error)
}

type inlineQueryEntry struct {
	chatID  int64
	results map[string]bool
	expires time.Time
}

func (d *daemon) registerMessageExtras() {
	d.handle(rpc.MethodMessageButtons, d.messageButtons)
	d.handle(rpc.MethodMessagePress, d.messagePress)
	d.handle(rpc.MethodChatAutoDelete, d.chatAutoDelete)
	d.handle(rpc.MethodMessageInlineQuery, d.messageInlineQuery)
	d.handle(rpc.MethodMessageSendInline, d.messageSendInline)
	d.handle(rpc.MethodMessageEditMedia, d.messageEditMedia)
	d.handle(rpc.MethodStickerSet, d.stickerSet)
	d.handle(rpc.MethodStickerSearch, d.stickerSearch)
	d.handle(rpc.MethodStickerInstalled, d.stickerInstalled)
	d.handle(rpc.MethodMessageSendSticker, d.messageSendSticker)
	d.srv.SetMethodTimeout(rpc.MethodMessageEditMedia, 600*time.Second)
}
func (d *daemon) stickerSearch(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.StickerSearchParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Limit == 0 {
		p.Limit = 20
	}
	if p.Limit < 1 || p.Limit > 100 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "limit must be between 1 and 100")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	return d.opts.Telegram.SearchStickerSets(ctx, p)
}
func (d *daemon) stickerInstalled(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	return d.opts.Telegram.InstalledStickerSets(ctx)
}

func (d *daemon) stickerSet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.StickerSetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	name, err := rpc.StickerShortName(p.Name)
	if err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	return d.opts.Telegram.StickerSet(ctx, name)
}
func (d *daemon) messageSendSticker(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SendStickerParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	name, err := rpc.StickerShortName(p.Set)
	if err != nil {
		return nil, err
	}
	p.Set = name
	if (p.Index == nil) == (p.Emoji == nil) || p.Index != nil && *p.Index < 0 || p.Emoji != nil && *p.Emoji == "" || p.ReplyTo < 0 || p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid sticker selection")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	canonical := p
	canonical.DryRun = false
	session, saved, available, err := d.beginIdempotency(ctx, id, p.IdempotencyKey, rpc.MethodMessageSendSticker, canonical, p.DryRun)
	if err != nil {
		return nil, err
	}
	defer session.unlock()
	if saved != nil {
		var result rpc.SendStickerResult
		if err := json.Unmarshal(saved, &result); err != nil {
			return nil, err
		}
		result.Replayed = true
		return result, nil
	}
	result, err := d.opts.Telegram.SendSticker(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	result.ChatID = id
	if p.DryRun {
		if p.IdempotencyKey != "" {
			result.ReplayAvailable = &available
		}
		return result, nil
	}
	d.saveMessages(ctx, rpc.MethodMessageSendSticker, []rpc.Message{{ChatID: id, ID: result.ID, Date: result.Date, MediaType: "sticker", Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic}})
	if err := session.complete(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *daemon) messageEditMedia(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.EditMediaParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 || (p.As != "photo" && p.As != "video" && p.As != "audio" && p.As != "document") || !filepath.IsAbs(p.File) || p.Duration < 0 || p.Width < 0 || p.Height < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid media edit parameters")
	}
	if p.Caption != nil && (!utf8.ValidString(*p.Caption) || utf8.RuneCountInString(*p.Caption) > 1024) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "caption must be at most 1024 characters")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	old, err := d.opts.Telegram.Message(ctx, ref, p.ID)
	if err != nil {
		return nil, err
	}
	if old.MediaType == "" || old.Service != "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message has no media")
	}
	policy := d.policy()
	if !policy.FilesAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, "sending files is disabled: ask the owner to add file roots to files in access.json")
	}
	open := d.openMediaFile
	if open == nil {
		open = fileroot.Open
	}
	file, size, err := open(policy.FileRoots(), p.File, fileroot.MaxFileSize)
	if err != nil {
		return nil, mediaFileError(err)
	}
	defer file.Close()
	checked := rpc.MediaFile{Name: filepath.Base(p.File), Size: size, File: file}
	mediaParams := rpc.SendMediaParams{As: p.As, Duration: p.Duration, Width: p.Width, Height: p.Height}
	if err = checkMediaContent(p.As, &checked, mediaParams); err != nil {
		return nil, err
	}
	result := rpc.EditMediaResult{ChatID: id, ID: p.ID, Action: "edit_media", MediaType: p.As, Size: size, DryRun: p.DryRun}
	if p.Caption != nil {
		length := utf8.RuneCountInString(*p.Caption)
		result.CaptionLength = &length
	}
	if p.DryRun {
		return result, nil
	}
	response, err := d.opts.Telegram.EditMedia(ctx, ref, p, checked)
	if err != nil {
		return nil, err
	}
	result.ID = response.ID
	result.Date = response.Date
	old.ChatID = id
	old.ID = response.ID
	old.Date = response.Date
	old.MediaType = p.As
	old.Edited = true
	if p.Caption != nil {
		old.Text = *p.Caption
	}
	d.saveMessages(ctx, rpc.MethodMessageEditMedia, []rpc.Message{old})
	return result, nil
}

func (d *daemon) messageSendInline(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SendInlineParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.QueryID == "" || p.Result == "" || p.ReplyTo < 0 || p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid inline result; repeat inline-query")
	}
	ref, chatID, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	d.inlineMu.Lock()
	entry, ok := d.inlineQueries[p.QueryID]
	d.inlineMu.Unlock()
	if !ok || entry.chatID != chatID || !time.Now().Before(entry.expires) || !entry.results[p.Result] {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "inline result is unknown or expired; repeat inline-query")
	}
	canonical := p
	canonical.DryRun = false
	session, saved, available, err := d.beginIdempotency(ctx, chatID, p.IdempotencyKey, rpc.MethodMessageSendInline, canonical, p.DryRun)
	if err != nil {
		return nil, err
	}
	defer session.unlock()
	if saved != nil {
		var result rpc.SendResult
		if err := json.Unmarshal(saved, &result); err != nil {
			return nil, err
		}
		result.Replayed = true
		return result, nil
	}
	if p.DryRun {
		result := rpc.SendResult{ChatID: chatID, DryRun: true, Action: "send_inline", Count: 1}
		if p.IdempotencyKey != "" {
			result.ReplayAvailable = &available
		}
		return result, nil
	}
	result, message, err := d.opts.Telegram.SendInline(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	result.ChatID = chatID
	if message != nil {
		d.saveMessages(ctx, rpc.MethodMessageSendInline, []rpc.Message{*message})
	}
	if err := session.complete(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *daemon) messageInlineQuery(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.InlineQueryParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chatID, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	botRef, bot, err := d.resolvePerson(ctx, p.Bot, false, false)
	if err != nil {
		return nil, err
	}
	if bot.Type != "bot" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "inline query requires a bot")
	}
	result, err := d.opts.Telegram.InlineQuery(ctx, ref, botRef, p)
	if err != nil {
		return nil, err
	}
	if id, err := strconv.ParseInt(result.QueryID, 10, 64); err != nil || id == 0 {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned an invalid inline query id")
	}
	result.ChatID = chatID
	result.BotID = bot.ID
	ttl := max(60, min(600, result.CacheTime))
	entry := inlineQueryEntry{chatID: chatID, results: make(map[string]bool), expires: time.Now().Add(time.Duration(ttl) * time.Second)}
	for _, item := range result.Results {
		entry.results[item.ID] = true
	}
	d.inlineMu.Lock()
	defer d.inlineMu.Unlock()
	if d.inlineQueries == nil {
		d.inlineQueries = make(map[string]inlineQueryEntry)
	}
	for key, value := range d.inlineQueries {
		if time.Now().After(value.expires) {
			delete(d.inlineQueries, key)
		}
	}
	if len(d.inlineQueries) >= 512 {
		for key := range d.inlineQueries {
			delete(d.inlineQueries, key)
			break
		}
	}
	d.inlineQueries[result.QueryID] = entry
	return result, nil
}
func (d *daemon) messageButtons(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ButtonsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	ref, _, err := d.writeReference(ctx, p.Chat, false)
	if err != nil {
		return nil, err
	}
	return d.opts.Telegram.MessageButtons(ctx, ref, p.ID)
}
func (d *daemon) messagePress(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.PressParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 || (p.Button == nil) == (p.Text == nil) || p.Button != nil && *p.Button < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid message id or button selection")
	}
	ref, _, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	return d.opts.Telegram.PressButton(ctx, ref, p)
}
func (d *daemon) chatAutoDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.AutoDeleteParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	period, err := rpc.ParseAutoDeletePeriod(p.Period)
	if err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	result := rpc.AutoDeleteResult{ChatID: id, TTLPeriod: period, DryRun: p.DryRun}
	if p.DryRun {
		return result, nil
	}
	if err := d.opts.Telegram.SetAutoDelete(ctx, ref, period); err != nil {
		return nil, err
	}
	return result, nil
}
