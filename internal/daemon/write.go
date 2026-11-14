package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

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
func validWriteIDs(ids []int) error {
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

// writeReference checks visibility before permission to write. Numeric IDs need
// no Telegram lookup, while other references use the same resolution as reads.
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
			chat, err = d.opts.Telegram.Resolve(ctx, ref)
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
		return ref, 0, rpc.ErrWriteDisabled()
	}
	return ref, id, nil
}

func (d *daemon) messageSend(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SendParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := validWriteText(p.Text); err != nil {
		return nil, err
	}
	if p.ReplyTo < 0 || p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "reply and topic ids must not be negative")
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.SendResult{ChatID: id, DryRun: true, Action: "send", Count: 1, TextLength: utf8.RuneCountInString(p.Text)}, nil
	}
	res, err := d.opts.Telegram.Send(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	d.saveMessages(ctx, rpc.MethodMessageSend, []rpc.Message{{ChatID: id, ID: res.ID, Date: res.Date, Text: p.Text, Out: true, ReplyTo: p.ReplyTo, TopicID: p.Topic}})
	return res, nil
}

func (d *daemon) messageEdit(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.EditParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := validWriteText(p.Text); err != nil {
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
		return rpc.SendResult{ChatID: id, DryRun: true, Action: "edit", Count: 1, TextLength: utf8.RuneCountInString(p.Text)}, nil
	}
	res, err := d.opts.Telegram.Edit(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	res.ChatID = id
	d.saveMessages(ctx, rpc.MethodMessageEdit, []rpc.Message{{ChatID: id, ID: res.ID, Date: res.Date, Text: p.Text, Out: true}})
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
	user, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in: run `auth login` first")
	}
	if err := d.writeDeletion(deletionRecord{Time: time.Now().UTC().Format(time.RFC3339), Account: user.ID, Tool: "tg", Phase: "intent", ChatID: id, IDs: p.IDs, Revoke: p.Revoke, Outcome: "pending"}); err != nil {
		return nil, fmt.Errorf("record deletion intent: %w", err)
	}
	result, callErr := d.opts.Telegram.Delete(ctx, ref, p)
	outcome := writeOutcome(callErr)
	if err := d.writeDeletion(deletionRecord{Time: time.Now().UTC().Format(time.RFC3339), Account: user.ID, Tool: "tg", Phase: "result", ChatID: id, IDs: p.IDs, Revoke: p.Revoke, Outcome: outcome}); err != nil {
		d.log.Error("deletion result journal failed")
	}
	if callErr != nil {
		return nil, callErr
	}
	result.ChatID = id
	if _, err := d.store.MarkMessagesDeleted(ctx, id, p.IDs); err != nil {
		d.log.Error("deleted message storage failed")
	}
	return result, nil
}

func (d *daemon) messageForward(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ForwardParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := validWriteIDs(p.IDs); err != nil {
		return nil, err
	}
	from, _, err := d.writeReference(ctx, p.From, false)
	if err != nil {
		return nil, err
	}
	to, id, err := d.writeReference(ctx, p.To, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.ForwardResult{ChatID: id, Messages: []rpc.ForwardedMessage{}, DryRun: true, Action: "forward", Count: len(p.IDs)}, nil
	}
	res, err := d.opts.Telegram.Forward(ctx, from, to, p)
	if err != nil {
		return nil, err
	}
	res.ChatID = id
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
