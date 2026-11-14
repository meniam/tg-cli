package tgclient

import (
	"context"
	"strconv"

	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) React(ctx context.Context, ref chatref.Ref, p rpc.ReactParams) (rpc.ReactResult, error) {
	if p.DryRun {
		return rpc.ReactResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := rpc.ValidateReactParams(p); err != nil {
		return rpc.ReactResult{}, err
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ReactResult{}, err
	}
	return react(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p)
}

func (c *Client) Pin(ctx context.Context, ref chatref.Ref, p rpc.PinParams) (rpc.PinResult, error) {
	if p.DryRun {
		return rpc.PinResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if p.ID <= 0 {
		return rpc.PinResult{}, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.PinResult{}, err
	}
	return updatePin(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p.ID, p.Silent, false)
}

func (c *Client) Unpin(ctx context.Context, ref chatref.Ref, p rpc.UnpinParams) (rpc.PinResult, error) {
	if p.DryRun {
		return rpc.PinResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if p.ID <= 0 {
		return rpc.PinResult{}, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.PinResult{}, err
	}
	return updatePin(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p.ID, false, true)
}

func (c *Client) SendPoll(ctx context.Context, ref chatref.Ref, p rpc.PollParams) (rpc.SendResult, error) {
	accountID, accountErr := c.idempotencyAccount(ctx, p.IdempotencyKey)
	if accountErr != nil {
		return rpc.SendResult{}, accountErr
	}
	p.AccountID = accountID

	if p.DryRun {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := rpc.ValidatePollParams(p); err != nil {
		return rpc.SendResult{}, err
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return sendPoll(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p)
}

func react(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, chatID int64, p rpc.ReactParams) (rpc.ReactResult, error) {
	if err := ctx.Err(); err != nil {
		return rpc.ReactResult{}, err
	}
	if err := rpc.ValidateReactParams(p); err != nil {
		return rpc.ReactResult{}, err
	}
	reactions := []tg.ReactionClass{}
	if !p.Remove {
		reactions = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: p.Emoji}}
	}
	_, err := api.MessagesSendReaction(ctx, &tg.MessagesSendReactionRequest{Peer: peer, MsgID: p.ID, Reaction: reactions})
	if err != nil {
		return rpc.ReactResult{}, mapActionError(err)
	}
	return rpc.ReactResult{ChatID: chatID, ID: p.ID, Emoji: p.Emoji, Removed: p.Remove}, nil
}

func updatePin(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, chatID int64, id int, silent, unpin bool) (rpc.PinResult, error) {
	if err := ctx.Err(); err != nil {
		return rpc.PinResult{}, err
	}
	if id <= 0 {
		return rpc.PinResult{}, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	_, err := api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{Peer: peer, ID: id, Silent: silent, Unpin: unpin})
	if err != nil {
		return rpc.PinResult{}, mapActionError(err)
	}
	return rpc.PinResult{ChatID: chatID, ID: id, Pinned: !unpin}, nil
}

func sendPoll(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, chatID int64, p rpc.PollParams) (rpc.SendResult, error) {
	wrappedAPI, wrapErr := idempotentAPI(api, p.AccountID, chatID, p.IdempotencyKey)
	if wrapErr != nil {
		return rpc.SendResult{}, wrapErr
	}
	api = wrappedAPI

	if err := ctx.Err(); err != nil {
		return rpc.SendResult{}, err
	}
	if err := rpc.ValidatePollParams(p); err != nil {
		return rpc.SendResult{}, err
	}
	randomID, err := newRandomID()
	if err != nil {
		return rpc.SendResult{}, err
	}
	pollID, err := newRandomID()
	if err != nil {
		return rpc.SendResult{}, err
	}
	answers := make([]tg.PollAnswerClass, len(p.Options))
	for i, option := range p.Options {
		answers[i] = &tg.PollAnswer{Text: tg.TextWithEntities{Text: option}, Option: []byte(strconv.Itoa(i))}
	}
	media := &tg.InputMediaPoll{Poll: tg.Poll{ID: pollID, Question: tg.TextWithEntities{Text: p.Question}, Answers: answers, MultipleChoice: p.Multiple, PublicVoters: p.Public, Quiz: p.Quiz}}
	if p.Quiz {
		media.CorrectAnswers = []int{p.Correct - 1}
		media.Solution = p.Explanation
	}
	req := &tg.MessagesSendMediaRequest{Peer: peer, Media: media, RandomID: randomID, Silent: p.Silent}
	reply := p.ReplyTo
	if reply == 0 {
		reply = p.Topic
	}
	if reply > 0 {
		replyTo := &tg.InputReplyToMessage{ReplyToMsgID: reply}
		if p.Topic > 1 && p.ReplyTo > 0 && p.ReplyTo != p.Topic {
			replyTo.SetTopMsgID(p.Topic)
		}
		req.ReplyTo = replyTo
	}
	updates, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return rpc.SendResult{}, mapActionError(err)
	}
	messages, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return rpc.SendResult{ChatID: chatID, ID: messages[0].ID, Date: messages[0].Date}, nil
}
