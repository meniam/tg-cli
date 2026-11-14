package tgclient

import (
	"context"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func validateReactParams(p rpc.ReactParams) error {
	if p.ID <= 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	if p.Emoji == "" || len(p.Emoji) > 32 || !utf8.ValidString(p.Emoji) || strings.IndexFunc(p.Emoji, unicode.IsSpace) >= 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "emoji must be nonempty, contain no whitespace, and be at most 32 bytes")
	}
	return nil
}

// ValidatePollParams enforces the agreed CLI limits before any Telegram request.
// The server may apply different limits from its configuration.
func ValidatePollParams(p rpc.PollParams) error {
	if strings.TrimSpace(p.Question) == "" || utf8.RuneCountInString(p.Question) > 255 || !utf8.ValidString(p.Question) {
		return rpc.NewError(rpc.CodeInvalidParams, "poll question must be 1 to 255 characters")
	}
	if len(p.Options) < 2 || len(p.Options) > 10 {
		return rpc.NewError(rpc.CodeInvalidParams, "poll must have 2 to 10 options")
	}
	seen := make(map[string]struct{}, len(p.Options))
	for _, option := range p.Options {
		if strings.TrimSpace(option) == "" || utf8.RuneCountInString(option) > 100 || !utf8.ValidString(option) {
			return rpc.NewError(rpc.CodeInvalidParams, "poll options must be 1 to 100 characters")
		}
		key := strings.TrimSpace(option)
		if _, exists := seen[key]; exists {
			return rpc.NewError(rpc.CodeInvalidParams, "poll options must be unique")
		}
		seen[key] = struct{}{}
	}
	if utf8.RuneCountInString(p.Explanation) > 200 || strings.Count(p.Explanation, "\n") > 2 || !utf8.ValidString(p.Explanation) {
		return rpc.NewError(rpc.CodeInvalidParams, "quiz explanation must be at most 200 characters and two line feeds")
	}
	if !p.Quiz && (p.Correct != 0 || p.Explanation != "") {
		return rpc.NewError(rpc.CodeInvalidParams, "correct answer and explanation require a quiz")
	}
	if p.Quiz && (p.Correct < 1 || p.Correct > len(p.Options)) {
		return rpc.NewError(rpc.CodeInvalidParams, "quiz correct answer must name an existing option")
	}
	if p.Quiz && p.Multiple {
		return rpc.NewError(rpc.CodeInvalidParams, "quiz cannot allow multiple answers")
	}
	if p.ReplyTo < 0 || p.Topic < 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "reply and topic ids must not be negative")
	}
	return nil
}

func (c *Client) React(ctx context.Context, ref chatref.Ref, p rpc.ReactParams) (rpc.ReactResult, error) {
	if p.DryRun {
		return rpc.ReactResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := validateReactParams(p); err != nil {
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
	if p.DryRun {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := ValidatePollParams(p); err != nil {
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
	if err := validateReactParams(p); err != nil {
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
	if err := ctx.Err(); err != nil {
		return rpc.SendResult{}, err
	}
	if err := ValidatePollParams(p); err != nil {
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
