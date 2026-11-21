package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"tg-cli/internal/access"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

const maxWriteTextLength = 4096
const channelIDBoundary = -1_000_000_000_000

func validWriteText(text string) error {
	if text == "" {
		return rpc.NewError(rpc.CodeInvalidParams, "message text must not be empty")
	}
	if utf8.RuneCountInString(text) > maxWriteTextLength {
		return rpc.NewError(rpc.CodeInvalidParams, "message text must be at most 4096 characters")
	}
	return nil
}

func validWriteSource(p rpc.SendParams) error {
	if p.Parse == "rich-html" || p.Parse == "rich-markdown" {
		if strings.TrimSpace(p.Text) == "" {
			return rpc.NewError(rpc.CodeInvalidParams, "message text must not be empty")
		}
		// Telegram's actual RichMessage limit is unknown until a live check.
		if utf8.RuneCountInString(p.Text) > maxWriteTextLength {
			return rpc.NewError(rpc.CodeInvalidParams, "rich message source must be at most 4096 characters")
		}
		return nil
	}
	if p.Parse != "" {
		if strings.TrimSpace(p.Text) == "" {
			return rpc.NewError(rpc.CodeInvalidParams, "message text must not be empty")
		}
		return nil // CheckText validates the rendered length.
	}
	return validWriteText(p.Text)
}

func formatRequested(p rpc.SendParams) bool { return p.Parse != "" || p.Entities != nil }

func applyTextCheck(result *rpc.SendResult, check rpc.TextCheck) {
	result.Mode = check.Mode
	result.PlainLength = &check.PlainLength
	result.EntityCount = &check.Entities
	result.RichMode = &check.Rich
	result.PremiumFeatures = check.PremiumFeatures
}
func validWriteIDs(ids []int) error {
	if len(ids) > 1000 {
		return rpc.NewError(rpc.CodeInvalidParams, "at most 1000 message ids are allowed")
	}
	if len(ids) == 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "at least one message id is required")
	}
	for _, id := range ids {
		if id <= 0 {
			return rpc.NewError(rpc.CodeInvalidParams, "message ids must be positive")
		}
	}
	return nil
}

// checkNumericPeer makes a missing numeric peer indistinguishable from a closed one.
func (d *daemon) checkNumericPeer(ctx context.Context, ref chatref.Ref) error {
	_, err := d.telegram(ctx).Resolve(ctx, ref)
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpc.CodeNotFound {
		return rpc.ErrUnknownChatID()
	}
	return err
}

// writeReference checks visibility before permission to write.
func (d *daemon) writeReference(ctx context.Context, value string, writing bool) (chatref.Ref, int64, error) {
	ref, err := parseChatNoInvite(value)
	if err != nil {
		return ref, 0, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return ref, 0, err
	}
	resolved, err := d.requireAllowedChat(ctx, ref)
	if err != nil {
		return ref, 0, err
	}
	id := ref.ID
	if ref.Kind != chatref.ID {
		if resolved == nil {
			var chat rpc.Chat
			chat, err = d.telegram(ctx).Resolve(ctx, ref)
			if err != nil {
				return ref, 0, err
			}
			resolved = &chat
		}
		id = resolved.ID
	}
	policy := d.policy()
	if !policy.Allowed(id) {
		return ref, 0, rpc.ErrChatNotFound()
	}
	if writing && !policy.WriteAllowed(id) {
		if ref.Kind == chatref.ID {
			if err := d.checkNumericPeer(ctx, ref); err != nil {
				return ref, 0, err
			}
		}
		return ref, 0, rpc.ErrWriteDisabled()
	}
	return ref, id, nil
}

// lockChatWrite keeps revocations ordered with the Telegram write.
func (d *daemon) lockChatWrite(id int64, readIDs ...int64) (func(), error) {
	policy := d.policy()
	d.accessMu.RLock()
	if d.accessSnapshot != nil {
		policy = *d.accessSnapshot
	}
	if d.legacyAccessPresent() || d.legacyConfigPresent() {
		policy = access.Denied()
	}
	if !policy.Allowed(id) {
		d.accessMu.RUnlock()
		return nil, rpc.ErrChatNotFound()
	}
	for _, readID := range readIDs {
		if !policy.Allowed(readID) {
			d.accessMu.RUnlock()
			return nil, rpc.ErrChatNotFound()
		}
	}
	if !policy.WriteAllowed(id) {
		d.accessMu.RUnlock()
		return nil, rpc.ErrWriteDisabled()
	}
	return d.accessMu.RUnlock, nil
}

func (d *daemon) messageSend(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SendParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	canonical := p
	canonical.DryRun = false
	scheduledAt, scheduleErr := rpc.ScheduleTime(p.At, p.In, time.Now())
	if err := validWriteSource(p); err != nil {
		return nil, err
	}
	if formatRequested(p) {
		if err := d.telegram(ctx).CheckTextLocal(p); err != nil {
			return nil, err
		}
	}
	if p.ReplyTo < 0 || p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "reply and topic ids must not be negative")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	var check rpc.TextCheck
	if p.DryRun || formatRequested(p) {
		check, err = d.telegram(ctx).CheckText(ctx, p)
		if err != nil {
			return nil, err
		}
	}
	if scheduleErr != nil {
		if p.IdempotencyKey == "" {
			return nil, scheduleErr
		}
		_, _, replayAvailable, err := d.beginIdempotency(ctx, id, p.IdempotencyKey, rpc.MethodMessageSend, canonical, true)
		if err != nil {
			return nil, err
		}
		if !replayAvailable {
			return nil, scheduleErr
		}
	}
	session, saved, available, err := d.beginIdempotency(ctx, id, p.IdempotencyKey, rpc.MethodMessageSend, canonical, p.DryRun)
	if err != nil {
		return nil, err
	}
	defer session.unlock()
	if saved != nil {
		releaseAccess, err := d.lockChatWrite(id)
		if err != nil {
			return nil, err
		}
		defer releaseAccess()
		var replay rpc.SendResult
		if err := json.Unmarshal(saved, &replay); err != nil {
			return nil, err
		}
		replay.Replayed = true
		return replay, nil
	}
	if scheduleErr != nil {
		return nil, scheduleErr
	}
	if p.IdempotencyKey != "" && p.In != "" && !p.DryRun {
		record, found, err := d.store.GetIdempotency(ctx, id, p.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, rpc.NewError(rpc.CodeTelegram, "idempotency reservation is missing")
		}
		scheduledAt, err = rpc.ScheduleTime("", p.In, record.CreatedAt.Add(time.Second))
		if err != nil {
			return nil, err
		}
		if scheduledAt.Before(time.Now().Add(10 * time.Second)) {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "reserved schedule time is too close or in the past")
		}
	}
	if !scheduledAt.IsZero() {
		p.At = scheduledAt.Format(time.RFC3339)
		p.In = ""
	}
	if p.DryRun {
		result := rpc.SendResult{ChatID: id, DryRun: true, Action: "send", Count: 1, TextLength: utf8.RuneCountInString(p.Text)}
		if p.IdempotencyKey != "" {
			result.ReplayAvailable = &available
		}
		if formatRequested(p) {
			applyTextCheck(&result, check)
		}
		return result, nil
	}
	releaseAccess, err := d.lockChatWrite(id)
	if err != nil {
		return nil, err
	}
	res, err := d.telegram(ctx).Send(ctx, ref, p)
	releaseAccess()
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	if !res.Scheduled {
		d.saveMessages(ctx, rpc.MethodMessageSend, []rpc.Message{{ChatID: id, ID: res.ID, Date: res.Date, Raw: res.Raw, Text: p.Text, Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic}})
	}
	if err := session.complete(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

func (d *daemon) messageEdit(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.EditParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	checkParams := rpc.SendParams{Text: p.Text, Parse: p.Parse, Entities: p.Entities}
	if err := validWriteSource(checkParams); err != nil {
		return nil, err
	}
	if formatRequested(checkParams) {
		if err := d.telegram(ctx).CheckTextLocal(checkParams); err != nil {
			return nil, err
		}
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	var check rpc.TextCheck
	if p.DryRun || formatRequested(checkParams) {
		check, err = d.telegram(ctx).CheckText(ctx, checkParams)
		if err != nil {
			return nil, err
		}
	}
	if p.DryRun {
		result := rpc.SendResult{ChatID: id, DryRun: true, Action: "edit", Count: 1, TextLength: utf8.RuneCountInString(p.Text)}
		if formatRequested(checkParams) {
			applyTextCheck(&result, check)
		}
		return result, nil
	}
	releaseAccess, err := d.lockChatWrite(id)
	if err != nil {
		return nil, err
	}
	res, err := d.telegram(ctx).Edit(ctx, ref, p)
	releaseAccess()
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	d.saveMessages(ctx, rpc.MethodMessageEdit, []rpc.Message{{ChatID: id, ID: res.ID, Date: res.Date, Raw: res.Raw, Text: p.Text, Out: true}})
	return res, nil
}

func (d *daemon) messageDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.DeleteParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := validWriteIDs(p.IDs); err != nil {
		return nil, err
	}
	if p.Revoke == p.ForMe {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "choose exactly one of revoke or for_me")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if id <= channelIDBoundary && p.ForMe {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "delete for me is not supported in channels")
	}
	if id > channelIDBoundary {
		for _, msgID := range p.IDs {
			chats, err := d.store.ChatsOfMessages(ctx, []int{msgID})
			if err != nil {
				return nil, err
			}
			if len(chats) != 1 || chats[0] != id {
				return nil, rpc.NewError(rpc.CodeNotFound, "message not found in this chat: read it first")
			}
		}
	}
	if p.DryRun {
		return rpc.DeleteResult{ChatID: id, Deleted: []int{}, DryRun: true, Action: "delete", Count: len(p.IDs)}, nil
	}
	user, err := d.telegram(ctx).Self(ctx)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in: run `auth login` first")
	}
	return d.deleteChunks(ctx, ref, id, user.ID, p)
}

func (d *daemon) messageForward(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ForwardParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.NoCaptions && !p.Copy {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "no_captions requires copy")
	}
	if err := validWriteIDs(p.IDs); err != nil {
		return nil, err
	}
	from, fromID, err := d.writeReference(ctx, p.From, false)
	if err != nil {
		return nil, err
	}
	if from.Kind == chatref.ID && d.policy().Enabled() {
		if err := d.checkNumericPeer(ctx, from); err != nil {
			return nil, err
		}
	}
	to, id, err := d.writeReference(ctx, p.To, true)
	if err != nil {
		return nil, err
	}
	canonical := p
	canonical.DryRun = false
	session, saved, available, err := d.beginIdempotency(ctx, id, p.IdempotencyKey, rpc.MethodMessageForward, canonical, p.DryRun)
	if err != nil {
		return nil, err
	}
	defer session.unlock()
	if saved != nil {
		releaseAccess, err := d.lockChatWrite(id, fromID)
		if err != nil {
			return nil, err
		}
		defer releaseAccess()
		var replay rpc.ForwardResult
		if err := json.Unmarshal(saved, &replay); err != nil {
			return nil, err
		}
		replay.Replayed = true
		return replay, nil
	}
	if p.DryRun {
		result := rpc.ForwardResult{ChatID: id, Messages: []rpc.ForwardedMessage{}, DryRun: true, Action: "forward", Count: len(p.IDs)}
		if p.IdempotencyKey != "" {
			result.ReplayAvailable = &available
		}
		return result, nil
	}
	releaseAccess, err := d.lockChatWrite(id, fromID)
	if err != nil {
		return nil, err
	}
	res, err := d.telegram(ctx).Forward(ctx, from, to, p)
	releaseAccess()
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	if err := session.complete(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

func (d *daemon) messageReact(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ReactParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateReactParams(p); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.ReactResult{ChatID: id, ID: p.ID, Emoji: p.Emoji, Removed: p.Remove, DryRun: true, Action: "react", Count: 1}, nil
	}
	res, err := d.telegram(ctx).React(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	res.ChatID, res.ID, res.Emoji, res.Removed = id, p.ID, p.Emoji, p.Remove
	return res, nil
}

func (d *daemon) messagePin(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.PinParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.PinResult{ChatID: id, ID: p.ID, Pinned: true, DryRun: true, Action: "pin", Count: 1}, nil
	}
	res, err := d.telegram(ctx).Pin(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	res.ChatID, res.ID, res.Pinned = id, p.ID, true
	d.setMessagePinned(ctx, id, p.ID, true)
	return res, nil
}

func (d *daemon) messageUnpin(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.UnpinParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.PinResult{ChatID: id, ID: p.ID, Pinned: false, DryRun: true, Action: "unpin", Count: 1}, nil
	}
	res, err := d.telegram(ctx).Unpin(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	res.ChatID, res.ID, res.Pinned = id, p.ID, false
	d.setMessagePinned(ctx, id, p.ID, false)
	return res, nil
}

func (d *daemon) setMessagePinned(ctx context.Context, chatID int64, id int, pinned bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	if _, err := d.store.SetMessagePinned(ctx, chatID, id, pinned); err != nil {
		d.log.Warn("message pin storage failed")
	}
}

func (d *daemon) messagePoll(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.PollParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidatePollParams(p); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	canonical := p
	canonical.DryRun = false
	session, saved, available, err := d.beginIdempotency(ctx, id, p.IdempotencyKey, rpc.MethodMessagePoll, canonical, p.DryRun)
	if err != nil {
		return nil, err
	}
	defer session.unlock()
	if saved != nil {
		releaseAccess, err := d.lockChatWrite(id)
		if err != nil {
			return nil, err
		}
		defer releaseAccess()
		var replay rpc.SendResult
		if err := json.Unmarshal(saved, &replay); err != nil {
			return nil, err
		}
		replay.Replayed = true
		return replay, nil
	}
	if p.DryRun {
		result := rpc.SendResult{ChatID: id, DryRun: true, Action: "poll", Count: 1, TextLength: utf8.RuneCountInString(p.Question)}
		if p.IdempotencyKey != "" {
			result.ReplayAvailable = &available
		}
		return result, nil
	}
	releaseAccess, err := d.lockChatWrite(id)
	if err != nil {
		return nil, err
	}
	res, err := d.telegram(ctx).SendPoll(ctx, ref, p)
	releaseAccess()
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	d.saveMessages(ctx, rpc.MethodMessagePoll, []rpc.Message{{ChatID: id, ID: res.ID, Date: res.Date, Raw: res.Raw, Text: p.Question, MediaType: "poll", Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic}})
	if err := session.complete(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

type deletionRecord struct {
	Time    string `json:"time"`
	Account int64  `json:"account"`
	Tool    string `json:"tool"`
	Phase   string `json:"phase"`
	ChatID  int64  `json:"chat_id"`
	IDs     []int  `json:"ids"`
	Revoke  bool   `json:"revoke"`
	Outcome string `json:"outcome"`
}

func (d *daemon) writeDeletion(record deletionRecord) (err error) {
	d.deletionsMu.Lock()
	defer d.deletionsMu.Unlock()
	path := d.opts.Paths.Deletions
	if path == "" {
		return errors.New("deletion journal path is empty")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

func writeOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) {
		return "failure"
	}
	switch rpcErr.Code {
	case rpc.CodeNotAuthorized:
		return "not_authorized"
	case rpc.CodeTelegram:
		return "telegram_error"
	case rpc.CodeFloodWait:
		return "flood_wait"
	case rpc.CodeNotFound:
		return "not_found"
	case rpc.CodeWriteDisabled:
		return "write_disabled"
	case rpc.CodeInvalidParams:
		return "usage"
	}
	return "failure"
}
